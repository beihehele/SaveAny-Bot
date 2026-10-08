// Package updater detects and applies verified assets from GitHub releases.
package updater

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/blang/semver"
)

var (
	// ErrAlreadyLatest reports an equal or older release.
	ErrAlreadyLatest = errors.New("current version is already up to date")
	// ErrMajorUpgrade requires the user to follow the migration guide.
	ErrMajorUpgrade = errors.New("major version changes require a manual upgrade")
	// ErrUpdateInProgress prevents concurrent executable replacement.
	ErrUpdateInProgress = errors.New("another update is in progress")
	// ErrUpdateApplied prevents repeated replacement before a process restart.
	ErrUpdateApplied = errors.New("an update has already been applied; restart before updating again")
	// ErrManualUpdate identifies builds or assets unsuitable for self-update.
	ErrManualUpdate = errors.New("this build requires a manual update")
	// ErrReleaseChanged rejects approval for a different or unavailable asset.
	ErrReleaseChanged = errors.New("approved release asset is no longer selected")
)

const (
	checkTimeout     = 30 * time.Second
	updateTimeout    = 5 * time.Minute
	maxMetadataBytes = 4 << 20
	maxAssetBytes    = 512 << 20
)

type asset struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
}

type releaseInfo struct {
	Tag         string    `json:"tag_name"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	Notes       string    `json:"body"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []asset   `json:"assets"`
}

// Release is an immutable snapshot of a detected release and its selected asset.
type Release struct {
	origin        *client
	repository    string
	version       semver.Version
	notes         string
	publishedAt   time.Time
	asset         asset
	checksumAsset *asset
}

// Version returns a copy of the detected semantic version.
func (r *Release) Version() semver.Version {
	v := r.version
	v.Pre = append([]semver.PRVersion(nil), v.Pre...)
	v.Build = append([]string(nil), v.Build...)
	return v
}

// AssetID identifies the exact asset approved by the user.
func (r *Release) AssetID() int64 { return r.asset.ID }

// AssetURL returns the public download URL for display, not for downloading.
func (r *Release) AssetURL() string { return r.asset.URL }

// AssetByteSize returns the expected compressed asset size.
func (r *Release) AssetByteSize() int64 { return r.asset.Size }

// ReleaseNotes returns the release description.
func (r *Release) ReleaseNotes() string { return r.notes }

// PublishedAt returns the publication time.
func (r *Release) PublishedAt() time.Time { return r.publishedAt }

// VerificationError reports whether the release has usable checksum metadata.
func (r *Release) VerificationError() error {
	if r.asset.Digest == "" {
		if r.checksumAsset != nil {
			return nil
		}
		return fmt.Errorf("%w: release has no SHA-256 digest or checksum", ErrManualUpdate)
	}
	if !strings.HasPrefix(r.asset.Digest, "sha256:") {
		return fmt.Errorf("%w: unsupported asset digest", ErrManualUpdate)
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(r.asset.Digest, "sha256:"))
	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("%w: invalid SHA-256 digest", ErrManualUpdate)
	}
	return nil
}

type client struct {
	http              *http.Client
	baseURL           string
	goos, goarch      string
	mu                sync.Mutex
	updating, applied bool
	validateBinary    func(string) error // test seam; nil uses Go build metadata
}

var defaultClient = &client{
	http: &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("too many asset redirects")
		}
		if req.URL.Scheme != "https" {
			return errors.New("asset redirect must use HTTPS")
		}
		return nil
	}},
	baseURL: "https://api.github.com",
	goos:    runtime.GOOS, goarch: runtime.GOARCH,
}

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// CheckEnvironment refuses container and variant builds without matching assets.
func CheckEnvironment(docker bool) error {
	if docker {
		return ErrManualUpdate
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "-tags" && setting.Value != "" {
				return ErrManualUpdate
			}
		}
	}
	return nil
}

// CheckUpgrade refuses major changes, equal versions and downgrades.
func CheckUpgrade(current semver.Version, release *Release) error {
	if release == nil {
		return errors.New("no release selected")
	}
	latest := release.Version()
	if latest.Major != current.Major {
		return ErrMajorUpgrade
	}
	if latest.LTE(current) {
		return ErrAlreadyLatest
	}
	return nil
}

// CheckApprovedUpgrade checks both the user's asset approval and version policy.
func CheckApprovedUpgrade(current semver.Version, release *Release, assetID int64) error {
	if release == nil || assetID <= 0 || release.AssetID() != assetID {
		return ErrReleaseChanged
	}
	return CheckUpgrade(current, release)
}

// DetectLatest selects the greatest stable version with an asset for this build.
// It does not trust GitHub's creation-time ordering to be semantic version order.
func DetectLatest(ctx context.Context, repository string) (*Release, bool, error) {
	return defaultClient.detectLatest(ctx, repository)
}

func (c *client) detectLatest(ctx context.Context, repository string) (*Release, bool, error) {
	if !repositoryPattern.MatchString(repository) || strings.Contains(repository, "..") {
		return nil, false, errors.New("invalid GitHub repository")
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	var latest *Release
	for page := 1; page <= 10; page++ {
		endpoint := fmt.Sprintf("%s/repos/%s/releases?per_page=100&page=%d", c.baseURL, repository, page)
		resp, err := c.get(ctx, endpoint, "application/vnd.github+json")
		if err != nil {
			return nil, false, err
		}
		if resp.StatusCode == http.StatusNotFound && page == 1 {
			resp.Body.Close()
			return nil, false, nil
		}
		data, err := readResponse(resp, maxMetadataBytes)
		if err != nil {
			return nil, false, fmt.Errorf("read releases: %w", err)
		}
		var releases []releaseInfo
		if err := json.Unmarshal(data, &releases); err != nil {
			return nil, false, fmt.Errorf("decode releases: %w", err)
		}
		for _, info := range releases {
			v, err := semver.Parse(strings.TrimPrefix(info.Tag, "v"))
			if err != nil || info.Draft || info.Prerelease || len(v.Pre) != 0 {
				continue
			}
			if latest != nil && !v.GT(latest.Version()) {
				continue
			}
			selected, checksum, err := c.selectAsset(info)
			if err != nil {
				return nil, false, err
			}
			if selected == nil {
				continue
			}
			latest = &Release{origin: c, repository: repository, version: v, notes: info.Notes, publishedAt: info.PublishedAt, asset: *selected, checksumAsset: checksum}
		}
		if len(releases) < 100 {
			return latest, latest != nil, ctx.Err()
		}
	}
	return nil, false, errors.New("release listing exceeds 1000 entries; update manually")
}

func (c *client) selectAsset(info releaseInfo) (*asset, *asset, error) {
	// These are the formats produced by the repository's go-release-action.
	prefix := "saveany-bot-" + info.Tag + "-" + c.goos + "-" + c.goarch
	var selected, checksum *asset
	for _, a := range info.Assets {
		if a.Name != prefix+".tar.gz" && a.Name != prefix+".zip" && a.Name != prefix && !(c.goos == "windows" && a.Name == prefix+".exe") {
			continue
		}
		if selected != nil {
			return nil, nil, errors.New("multiple matching release assets; update manually")
		}
		if a.ID <= 0 || a.Size <= 0 || a.Size > maxAssetBytes {
			return nil, nil, errors.New("invalid release asset ID or size")
		}
		copy := a
		selected = &copy
	}
	if selected != nil {
		for _, a := range info.Assets {
			if a.Name == selected.Name+".sha256" {
				if checksum != nil || a.ID <= 0 || a.Size <= 0 || a.Size > 4096 {
					return nil, nil, errors.New("invalid checksum asset")
				}
				copy := a
				checksum = &copy
			}
		}
	}
	return selected, checksum, nil
}

func (c *client) get(ctx context.Context, endpoint, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "SaveAny-Bot-updater")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitHub request: %w", err)
	}
	return resp, nil
}

func readResponse(resp *http.Response, limit int64) ([]byte, error) {
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("GitHub response exceeds size limit")
	}
	return data, nil
}
