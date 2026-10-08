package config

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/spf13/viper"
)

func TestRemoteConfig(t *testing.T) {
	oldCfg, oldUsers, oldStorages, oldAccess := cfg, userIDs, storages, userStorages
	t.Cleanup(func() { cfg, userIDs, storages, userStorages = oldCfg, oldUsers, oldStorages, oldAccess; viper.Reset() })
	for _, code := range []int{200, 404} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			viper.Reset()
			viper.SetConfigFile(filepath.Join(t.TempDir(), "nonexistent.toml"))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code); fmt.Fprint(w, "workers = 7\n") }))
			defer server.Close()
			err := Init(t.Context(), server.URL)
			if code == 200 {
				if err != nil || C().Workers != 7 {
					t.Fatalf("workers=%d err=%v", C().Workers, err)
				}
			} else if err == nil {
				t.Fatal("accepted failed download")
			}
		})
	}
	t.Run("cancelled", func(t *testing.T) {
		viper.Reset()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("cancelled request reached server") }))
		defer server.Close()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := Init(ctx, server.URL); !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v", err)
		}
	})
}

func TestRemoteConfigPreservesLocalFileOnSuccessAndFailure(t *testing.T) {
	oldCfg, oldUsers, oldStorages, oldAccess := cfg, userIDs, storages, userStorages
	t.Cleanup(func() {
		cfg, userIDs, storages, userStorages = oldCfg, oldUsers, oldStorages, oldAccess
		viper.Reset()
	})
	t.Chdir(t.TempDir())
	local := []byte("workers = 2\n")
	if err := os.WriteFile("config.toml", local, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		code      int
		body      string
		truncated bool
		wantError bool
	}{
		{name: "success", code: 200, body: "workers = 7\n"},
		{name: "http failure", code: 503, body: "unavailable", wantError: true},
		{name: "malformed configuration", code: 200, body: "workers = [", wantError: true},
		{name: "truncated response", code: 200, body: "workers = 7\n", truncated: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viper.Reset()
			if err := Init(t.Context(), "config.toml"); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.truncated {
					w.Header().Set("Content-Length", "1000")
				}
				w.WriteHeader(tc.code)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			err := Init(t.Context(), server.URL)
			if (err != nil) != tc.wantError {
				t.Fatalf("error=%v, wantError=%v", err, tc.wantError)
			}
			wantWorkers := 7
			if tc.wantError {
				wantWorkers = 2
			}
			if C().Workers != wantWorkers {
				t.Fatalf("workers=%d, want %d", C().Workers, wantWorkers)
			}
			content, err := os.ReadFile("config.toml")
			if err != nil || string(content) != string(local) {
				t.Fatalf("local configuration changed: %q, error=%v", content, err)
			}
		})
	}
}

func TestInitializationReplacesAccessListsAndPreservesLastValidConfig(t *testing.T) {
	oldCfg, oldUsers, oldStorages, oldAccess := cfg, userIDs, storages, userStorages
	t.Cleanup(func() { cfg, userIDs, storages, userStorages = oldCfg, oldUsers, oldStorages, oldAccess; viper.Reset() })
	writeConfig := func(body string) string {
		p := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(p, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	first := `workers = 2
[[storages]]
name = "old"
type = "local"
enable = true
base_path = "files"
[[users]]
id = 1
blacklist = true
`
	second := `workers = 4
[[storages]]
name = "new"
type = "local"
enable = true
base_path = "files"
[[users]]
id = 2
blacklist = true
`
	viper.Reset()
	if err := Init(t.Context(), writeConfig(first)); err != nil {
		t.Fatal(err)
	}
	if !C().HasStorage(1, "old") {
		t.Fatal("initial permission missing")
	}
	if err := Init(t.Context(), writeConfig(second)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(C().GetUsersID(), []int64{2}) || C().HasStorage(1, "old") || C().HasStorage(2, "old") || !C().HasStorage(2, "new") {
		t.Fatalf("stale access: users=%v access=%v", userIDs, userStorages)
	}
	if err := Init(t.Context(), writeConfig("workers = 99\nproxy = 'unsupported://localhost'\n")); err == nil {
		t.Fatal("accepted invalid proxy")
	}
	if C().Workers != 4 || !C().HasStorage(2, "new") {
		t.Fatal("failed initialization changed live config")
	}
	if err := Init(t.Context(), writeConfig("workers = 1\n")); err != nil {
		t.Fatal(err)
	}
	if len(C().Users) != 0 || len(C().Storages) != 0 || len(userIDs) != 0 || len(userStorages) != 0 {
		t.Fatal("omitted lists retained old values")
	}
}
