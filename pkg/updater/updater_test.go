package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blang/semver"
)

func fixtureRelease(goos, suffix string, data []byte) releaseInfo {
	hash := sha256.Sum256(data)
	return releaseInfo{Tag: "v1.2.4", Assets: []asset{{ID: 42, Name: "saveany-bot-v1.2.4-" + goos + "-amd64" + suffix, Size: int64(len(data)), Digest: "sha256:" + hex.EncodeToString(hash[:]), URL: "https://untrusted.invalid/ignored"}}}
}

func testClient(t *testing.T, info releaseInfo, handler http.HandlerFunc) (*client, *Release) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/owner/repo/releases" {
			json.NewEncoder(w).Encode([]releaseInfo{info})
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	c := &client{http: server.Client(), baseURL: server.URL, goos: "linux", goarch: "amd64", validateBinary: func(string) error { return nil }}
	if strings.Contains(info.Assets[0].Name, "windows") {
		c.goos = "windows"
	}
	r, ok, err := c.detectLatest(t.Context(), "owner/repo")
	if err != nil || !ok {
		t.Fatalf("detect: %v %v", ok, err)
	}
	return c, r
}

func writeTarget(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "renamed-program")
	if err := os.WriteFile(file, []byte("original executable"), 0751); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestDetectLatestPaginatesAndUsesSemanticOrder(t *testing.T) {
	var pages atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages.Add(1)
		if r.URL.Query().Get("per_page") != "100" {
			t.Error("missing page size")
		}
		if r.URL.Query().Get("page") == "1" {
			releases := make([]releaseInfo, 100)
			releases[0] = fixtureRelease("linux", ".tar.gz", []byte("archive"))
			json.NewEncoder(w).Encode(releases)
			return
		}
		newer := fixtureRelease("linux", ".tar.gz", []byte("archive"))
		newer.Tag = "v1.10.0"
		newer.Assets[0].Name = "saveany-bot-v1.10.0-linux-amd64.tar.gz"
		draft, prerelease, malformed := newer, newer, newer
		draft.Tag, draft.Draft = "v9.0.0", true
		prerelease.Tag = "v9.0.0-beta.1"
		malformed.Tag = "junk9.0.0"
		json.NewEncoder(w).Encode([]releaseInfo{draft, prerelease, malformed, newer})
	}))
	defer server.Close()
	c := &client{http: server.Client(), baseURL: server.URL, goos: "linux", goarch: "amd64"}
	r, ok, err := c.detectLatest(t.Context(), "owner/repo")
	if err != nil || !ok || r.Version().String() != "1.10.0" || pages.Load() != 2 {
		t.Fatalf("release=%v ok=%v err=%v pages=%d", r, ok, err, pages.Load())
	}
}

func TestDetectLatestHTTPAndMetadataErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		wantError bool
	}{
		{"not found", 404, "", false}, {"rate limited", 403, "", true}, {"invalid JSON", 200, "{}", true},
		{"oversized", 200, strings.Repeat(" ", maxMetadataBytes+1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			defer server.Close()
			c := &client{http: server.Client(), baseURL: server.URL}
			_, found, err := c.detectLatest(t.Context(), "owner/repo")
			if found || (err != nil) != tc.wantError {
				t.Fatalf("found=%v err=%v", found, err)
			}
		})
	}
}

func TestCheckUpgradeRejectsMajorChangesAndDowngrades(t *testing.T) {
	r := &Release{version: semver.MustParse("1.2.4")}
	for _, tc := range []struct {
		current string
		want    error
	}{
		{"1.2.3", nil}, {"1.2.4", ErrAlreadyLatest}, {"1.3.0", ErrAlreadyLatest}, {"2.0.0", ErrMajorUpgrade}, {"0.9.0", ErrMajorUpgrade},
	} {
		if err := CheckUpgrade(semver.MustParse(tc.current), r); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v", tc.current, err)
		}
	}
}

func TestApprovedUpgradeCannotSelectAnotherAssetOrMajorVersion(t *testing.T) {
	r := &Release{version: semver.MustParse("1.2.4"), asset: asset{ID: 42}}
	current := semver.MustParse("1.2.3")
	if err := CheckApprovedUpgrade(current, r, 42); err != nil {
		t.Fatal(err)
	}
	if err := CheckApprovedUpgrade(current, r, 43); !errors.Is(err, ErrReleaseChanged) {
		t.Fatalf("changed asset: %v", err)
	}
	if err := CheckApprovedUpgrade(current, nil, 42); !errors.Is(err, ErrReleaseChanged) {
		t.Fatalf("missing asset: %v", err)
	}
	r.version = semver.MustParse("2.0.0")
	if err := CheckApprovedUpgrade(current, r, 42); !errors.Is(err, ErrMajorUpgrade) {
		t.Fatalf("major change: %v", err)
	}
}

func TestRejectedVersionNeverDownloadsOrChangesTarget(t *testing.T) {
	c, r := testClient(t, fixtureRelease("linux", "", []byte("new")), func(w http.ResponseWriter, req *http.Request) { t.Error("rejected update downloaded an asset") })
	target := writeTarget(t)
	for _, current := range []string{"2.0.0", "1.2.4", "1.3.0"} {
		if _, err := c.apply(t.Context(), semver.MustParse(current), r, target); err == nil {
			t.Fatalf("accepted %s", current)
		}
	}
	got, _ := os.ReadFile(target)
	if string(got) != "original executable" {
		t.Fatal("rejected update changed target")
	}
}

func TestAssetSelectionRefusesVariantsAndAmbiguousMatches(t *testing.T) {
	c := &client{goos: "linux", goarch: "amd64"}
	info := fixtureRelease("linux", ".tar.gz", []byte("archive"))
	variant := info
	variant.Assets = append([]asset(nil), info.Assets...)
	variant.Assets[0].Name = "saveany-bot-micro-v1.2.4-linux-amd64.tar.gz"
	if selected, _, err := c.selectAsset(variant); err != nil || selected != nil {
		t.Fatalf("variant selected: %v %v", selected, err)
	}
	ambiguous := info
	ambiguous.Assets = append(append([]asset(nil), info.Assets...), info.Assets[0])
	if _, _, err := c.selectAsset(ambiguous); err == nil {
		t.Fatal("ambiguous assets selected")
	}
}

func archiveFixture(t *testing.T, format, name string, contents []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if format == ".zip" {
		writer := zip.NewWriter(&buf)
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(contents); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		gz := gzip.NewWriter(&buf)
		writer := tar.NewWriter(gz)
		if err := writer.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(contents))}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(contents); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return buf.Bytes()
}

func TestApplyExactAssetWithBackupAndNoSecondDetection(t *testing.T) {
	for _, tc := range []struct{ goos, suffix, command string }{
		{"linux", ".tar.gz", "saveany-bot"}, {"windows", ".zip", "saveany-bot.exe"}, {"linux", "", ""},
	} {
		t.Run(tc.goos+tc.suffix, func(t *testing.T) {
			contents := []byte("new executable")
			data := contents
			if tc.suffix != "" {
				data = archiveFixture(t, tc.suffix, tc.command, contents)
			}
			var requests atomic.Int32
			c, r := testClient(t, fixtureRelease(tc.goos, tc.suffix, data), func(w http.ResponseWriter, req *http.Request) {
				requests.Add(1)
				if req.URL.Path != "/repos/owner/repo/releases/assets/42" || req.Header.Get("Accept") != "application/octet-stream" {
					t.Errorf("unexpected asset request: %s", req.URL)
				}
				w.Write(data)
			})
			target := writeTarget(t)
			before, _ := os.Stat(target)
			backup, err := c.apply(t.Context(), semver.MustParse("1.2.3"), r, target)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(target)
			old, _ := os.ReadFile(backup)
			after, _ := os.Stat(target)
			if !bytes.Equal(got, contents) || string(old) != "original executable" || requests.Load() != 1 || before.Mode().Perm() != after.Mode().Perm() {
				t.Fatalf("got=%q backup=%q requests=%d", got, old, requests.Load())
			}
			if _, err := c.apply(t.Context(), semver.MustParse("1.2.3"), r, target); !errors.Is(err, ErrUpdateApplied) {
				t.Fatalf("repeat update: %v", err)
			}
		})
	}
}

func TestFailedVerificationAndCancellationPreserveOriginal(t *testing.T) {
	for _, kind := range []string{"digest", "short body", "long body", "missing checksum", "HTTP", "invalid binary", "cancel before commit", "missing executable", "traversal", "gzip CRC"} {
		t.Run(kind, func(t *testing.T) {
			data := []byte("new executable")
			suffix := ""
			if kind == "missing executable" || kind == "traversal" || kind == "gzip CRC" {
				suffix = ".tar.gz"
				name := "saveany-bot"
				if kind == "missing executable" {
					name = "README.md"
				}
				if kind == "traversal" {
					name = "../saveany-bot"
				}
				data = archiveFixture(t, suffix, name, data)
				if kind == "gzip CRC" {
					data[len(data)-8] ^= 1
				}
			}
			info := fixtureRelease("linux", suffix, data)
			if kind == "digest" {
				info.Assets[0].Digest = "sha256:" + strings.Repeat("0", 64)
			}
			if kind == "missing checksum" {
				info.Assets[0].Digest = ""
			}
			c, r := testClient(t, info, func(w http.ResponseWriter, req *http.Request) {
				if kind == "HTTP" {
					w.WriteHeader(500)
					return
				}
				body := data
				if kind == "short body" {
					body = data[:len(data)-1]
				}
				if kind == "long body" {
					body = append(append([]byte{}, data...), '!')
				}
				w.Write(body)
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if kind == "invalid binary" {
				c.validateBinary = func(string) error { return errors.New("invalid binary") }
			}
			if kind == "cancel before commit" {
				c.validateBinary = func(string) error { cancel(); return nil }
			}
			target := writeTarget(t)
			if _, err := c.apply(ctx, semver.MustParse("1.2.3"), r, target); err == nil {
				t.Fatal("bad update succeeded")
			}
			got, _ := os.ReadFile(target)
			files, _ := os.ReadDir(filepath.Dir(target))
			if string(got) != "original executable" || len(files) != 1 {
				t.Fatalf("original changed or temporary files leaked: %q %v", got, files)
			}
		})
	}
}

func TestApplyChecksumSidecar(t *testing.T) {
	data := []byte("new executable")
	info := fixtureRelease("linux", "", data)
	hash := sha256.Sum256(data)
	checksum := []byte(hex.EncodeToString(hash[:]) + "\n")
	info.Assets[0].Digest = ""
	info.Assets = append(info.Assets, asset{ID: 43, Name: info.Assets[0].Name + ".sha256", Size: int64(len(checksum))})
	c, r := testClient(t, info, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/repos/owner/repo/releases/assets/43" {
			w.Write(checksum)
			return
		}
		w.Write(data)
	})
	if _, err := c.apply(t.Context(), semver.MustParse("1.2.3"), r, writeTarget(t)); err != nil {
		t.Fatal(err)
	}
}

func TestApplySerializesUpdatesAndCancelsDownload(t *testing.T) {
	started := make(chan struct{})
	c, r := testClient(t, fixtureRelease("linux", "", []byte("new")), func(w http.ResponseWriter, req *http.Request) { close(started); <-req.Context().Done() })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	target := writeTarget(t)
	done := make(chan error, 1)
	go func() { _, err := c.apply(ctx, semver.MustParse("1.2.3"), r, target); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("download not started")
	}
	if _, err := c.apply(t.Context(), semver.MustParse("1.2.3"), r, target); !errors.Is(err, ErrUpdateInProgress) {
		t.Fatalf("parallel update: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("download ignored cancellation")
	}
	got, _ := os.ReadFile(target)
	if string(got) != "original executable" {
		t.Fatal("cancelled download replaced target")
	}
}

func TestCommitFailureRestoresOriginalOrPreservesRecoveryBackup(t *testing.T) {
	for _, failRollback := range []bool{false, true} {
		t.Run(fmt.Sprint(failRollback), func(t *testing.T) {
			target := writeTarget(t)
			staged := filepath.Join(filepath.Dir(target), "new")
			if err := os.WriteFile(staged, []byte("new"), 0755); err != nil {
				t.Fatal(err)
			}
			calls := 0
			_, err := commitBinary(target, staged, func(from, to string) error {
				calls++
				if calls == 2 || (failRollback && calls == 3) {
					return errors.New("injected rename failure")
				}
				return os.Rename(from, to)
			})
			if err == nil {
				t.Fatal("commit succeeded")
			}
			if !failRollback {
				got, _ := os.ReadFile(target)
				if string(got) != "original executable" {
					t.Fatal("rollback lost original")
				}
			} else {
				backups, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".saveany-update-old-*"))
				if len(backups) != 1 || !strings.Contains(err.Error(), backups[0]) {
					t.Fatalf("recovery path missing: %v", err)
				}
				got, _ := os.ReadFile(backups[0])
				if string(got) != "original executable" {
					t.Fatal("recovery backup lost original")
				}
			}
		})
	}
}

func TestBinaryBuildMetadataChecks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module github.com/krau/SaveAny-Bot\n\ngo 1.25.0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	target := filepath.Join(dir, "fixture.exe")
	cmd := exec.CommandContext(ctx, "go", "build", "-o", target, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0", "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, output)
	}
	c := &client{goos: runtime.GOOS, goarch: runtime.GOARCH}
	if err := c.checkBinary(target); err != nil {
		t.Fatal(err)
	}
	c.goarch = "wrong-architecture"
	if err := c.checkBinary(target); err == nil {
		t.Fatal("wrong architecture accepted")
	}
	if testExecutable, err := os.Executable(); err != nil {
		t.Fatal(err)
	} else if err := (&client{goos: runtime.GOOS, goarch: runtime.GOARCH}).checkBinary(testExecutable); err == nil {
		t.Fatal("unrelated test executable accepted")
	}
}
