package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
)

func TestAdminConfigurationAndEnvironment(t *testing.T) {
	oldCfg, oldUsers, oldStorages, oldAccess := cfg, userIDs, storages, userStorages
	t.Cleanup(func() { cfg, userIDs, storages, userStorages = oldCfg, oldUsers, oldStorages, oldAccess; viper.Reset() })
	viper.Reset()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("workers=2\n[admin]\nenable=true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SAVEANY_ADMIN_PASSWORD", "isolated-test-password")
	t.Setenv("SAVEANY_ADMIN_PORT", "18081")
	if err := Init(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	admin := C().Admin
	if !admin.Enable || admin.Port != 18081 || admin.Host != "127.0.0.1" || admin.SessionTTL != 12*time.Hour || admin.Password != "isolated-test-password" {
		t.Fatal("admin config did not load defaults/environment")
	}
	if err := admin.Validate(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(C())
	if err != nil || strings.Contains(string(encoded), "isolated-test-password") {
		t.Fatal("generic config JSON leaked admin credentials")
	}
	for _, change := range []func(*AdminConfig){
		func(c *AdminConfig) { c.Password = "" },
		func(c *AdminConfig) { c.PasswordHash = "invalid" },
		func(c *AdminConfig) { c.Host = "" },
		func(c *AdminConfig) { c.Port = 65536 },
		func(c *AdminConfig) { c.SessionTTL = time.Second },
		func(c *AdminConfig) { c.Password = "short" },
	} {
		invalid := admin
		change(&invalid)
		if invalid.Validate() == nil {
			t.Fatal("accepted invalid enabled admin configuration")
		}
		invalid.Enable = false
		if err := invalid.Validate(); err != nil {
			t.Fatal("disabled admin affected existing configuration", err)
		}
	}
}
