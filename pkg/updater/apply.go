package updater

import (
	"context"
	"debug/buildinfo"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/blang/semver"
	"github.com/charmbracelet/log"
)

// Apply downloads the exact detected asset, verifies it, then replaces the
// current executable. A successful update retains the old binary for recovery.
// Caller cancellation is honored until commit starts; commit/rollback finishes
// even if the caller disconnects while the executable is being renamed.
func Apply(ctx context.Context, current semver.Version, release *Release) (string, error) {
	if err := CheckEnvironment(false); err != nil {
		return "", err
	}
	target, err := os.Executable()
	if err != nil {
		return "", err
	}
	return defaultClient.apply(ctx, current, release, target)
}

func (c *client) apply(ctx context.Context, current semver.Version, release *Release, target string) (string, error) {
	if release == nil || release.origin != c {
		return "", errors.New("invalid release snapshot")
	}
	if err := CheckUpgrade(current, release); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, updateTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	c.mu.Lock()
	if c.updating || c.applied {
		err := ErrUpdateInProgress
		if c.applied {
			err = ErrUpdateApplied
		}
		c.mu.Unlock()
		return "", err
	}
	c.updating = true
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.updating = false; c.mu.Unlock() }()

	expected, err := c.checksum(ctx, release)
	if err != nil {
		return "", err
	}
	target, err = filepath.EvalSymlinks(target)
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("update target is not a regular file")
	}
	dir := filepath.Dir(target)
	archive, err := os.CreateTemp(dir, ".saveany-update-download-*")
	if err != nil {
		return "", err
	}
	defer removeTemp(ctx, archive.Name())
	defer archive.Close()
	if err := c.download(ctx, release, archive, expected); err != nil {
		return "", err
	}
	staged, err := os.CreateTemp(dir, ".saveany-update-new-*")
	if err != nil {
		return "", err
	}
	defer removeTemp(ctx, staged.Name())
	if err := c.extract(ctx, archive, release.asset.Name, staged); err != nil {
		return "", errors.Join(err, staged.Close())
	}
	if err := staged.Chmod(info.Mode().Perm()); err != nil {
		return "", errors.Join(err, staged.Close())
	}
	if err := staged.Sync(); err != nil {
		return "", errors.Join(err, staged.Close())
	}
	if err := staged.Close(); err != nil {
		return "", err
	}
	validate := c.validateBinary
	if validate == nil {
		validate = c.checkBinary
	}
	if err := validate(staged.Name()); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	backup, err := commitBinary(target, staged.Name(), os.Rename)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	c.applied = true
	c.mu.Unlock()
	return backup, nil
}

func (c *client) checkBinary(path string) error {
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return fmt.Errorf("invalid Go executable: %w", err)
	}
	if info.Path != "github.com/krau/SaveAny-Bot" {
		return errors.New("release executable is not SaveAny-Bot")
	}
	settings := make(map[string]string)
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if settings["GOOS"] != c.goos || settings["GOARCH"] != c.goarch || settings["-tags"] != "" {
		return errors.New("release executable does not match the default build platform")
	}
	return nil
}

func removeTemp(ctx context.Context, path string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.FromContext(ctx).Warnf("Failed to remove update temporary file %s: %v", path, err)
	}
}

func commitBinary(target, staged string, rename func(string, string) error) (string, error) {
	reserved, err := os.CreateTemp(filepath.Dir(target), ".saveany-update-old-*")
	if err != nil {
		return "", err
	}
	backup := reserved.Name()
	if err := errors.Join(reserved.Close(), os.Remove(backup)); err != nil {
		return "", err
	}
	if err := rename(target, backup); err != nil {
		return "", fmt.Errorf("back up executable: %w", err)
	}
	if err := rename(staged, target); err != nil {
		if rollback := rename(backup, target); rollback != nil {
			return "", fmt.Errorf("commit failed: %w; rollback failed: %v; recover original executable from %s", err, rollback, backup)
		}
		return "", fmt.Errorf("commit failed; original executable restored: %w", err)
	}
	return backup, nil
}
