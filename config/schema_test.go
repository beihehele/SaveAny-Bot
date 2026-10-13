package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	storconfig "github.com/krau/SaveAny-Bot/config/storage"
	"github.com/spf13/viper"
)

func TestConfigurationAcceptsOnlyCurrentFields(t *testing.T) {
	oldCfg, oldUsers, oldStorages, oldAccess := cfg, userIDs, storages, userStorages
	t.Cleanup(func() {
		cfg, userIDs, storages, userStorages = oldCfg, oldUsers, oldStorages, oldAccess
		viper.Reset()
	})
	for _, text := range []string{"unknown = false\n", "[telegram]\nunknown = true\n", "[[storages]]\nname='local'\ntype='local'\nenable=false\nunknown='unused'\n"} {
		viper.Reset()
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		if err := Init(t.Context(), path); err == nil || !strings.Contains(err.Error(), "unknown") {
			t.Fatalf("undefined config field accepted: %v", err)
		}
	}
}

func TestStorageDefinitionsUseOnlyCurrentTypes(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		if _, err := storconfig.LoadStorageConfigs([]map[string]any{{"name": "invalid", "type": "unsupported", "enable": enabled}}); err == nil {
			t.Fatalf("undefined storage type accepted with enable=%t", enabled)
		}
	}
	definitions := []map[string]any{
		{"name": "inactive", "type": "local", "enable": false},
		{"name": "active", "type": "local", "enable": true, "base_path": t.TempDir()},
	}
	configs, err := storconfig.LoadStorageConfigs(definitions)
	if err != nil || len(configs) != 1 || configs[0].GetName() != "active" {
		t.Fatalf("current Local definitions changed: %v %v", configs, err)
	}
}
