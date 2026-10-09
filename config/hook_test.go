package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/viper"
)

func TestHookExecTimeoutConfiguration(t *testing.T) {
	oldCfg, oldUsers, oldStorages, oldAccess := cfg, userIDs, storages, userStorages
	t.Cleanup(func() {
		cfg, userIDs, storages, userStorages = oldCfg, oldUsers, oldStorages, oldAccess
		viper.Reset()
	})
	for _, tc := range []struct {
		name, body, env string
		want            time.Duration
		invalid         bool
	}{
		{name: "default", want: DefaultHookExecTimeout},
		{name: "configured", body: "[hook.exec]\ntimeout='2m'\n", want: 2 * time.Minute},
		{name: "environment", env: "45s", want: 45 * time.Second},
		{name: "zero", body: "[hook.exec]\ntimeout='0s'\n", invalid: true},
		{name: "negative", body: "[hook.exec]\ntimeout='-1s'\n", invalid: true},
		{name: "malformed", body: "[hook.exec]\ntimeout='forever'\n", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viper.Reset()
			t.Setenv("SAVEANY_HOOK_EXEC_TIMEOUT", tc.env)
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			before := cfg
			err := Init(t.Context(), path)
			if tc.invalid {
				if err == nil || cfg != before {
					t.Fatalf("invalid timeout accepted or published: %v", err)
				}
				return
			}
			if err != nil || C().Hook.Exec.Timeout != tc.want {
				t.Fatalf("timeout=%v want=%v error=%v", C().Hook.Exec.Timeout, tc.want, err)
			}
		})
	}
}
