package config

import (
	"os"
	"path/filepath"
	"testing"

	storcfg "github.com/krau/SaveAny-Bot/config/storage"
	"github.com/spf13/viper"
)

func TestTelegramStorageRequiresUserbotAndGuardsWatchSources(t *testing.T) {
	previous, ids, names, access := cfg, userIDs, storages, userStorages
	t.Cleanup(func() { cfg, userIDs, storages, userStorages = previous, ids, names, access; viper.Reset() })
	for _, enabled := range []bool{false, true} {
		viper.Reset()
		file := filepath.Join(t.TempDir(), "config.toml")
		text := "[[storages]]\nname='TG收藏'\ntype='telegram'\nenable=true\nchat_id=-1001234567890\n"
		if enabled {
			text = "[telegram.userbot]\nenable=true\n" + text
		}
		if err := os.WriteFile(file, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		err := Init(t.Context(), file)
		if (err == nil) != enabled {
			t.Fatalf("userbot enabled=%t: %v", enabled, err)
		}
		if enabled && (!C().IsCollectionTarget(-1001234567890) || C().IsCollectionTarget(-1001111111111)) {
			t.Fatal("collection target guard mismatch")
		}
	}
	c := Config{Storages: []storcfg.StorageConfig{&storcfg.TelegramStorageConfig{ChatID: -10022}}}
	if !c.IsCollectionTarget(-10022) {
		t.Fatal("missing target")
	}
}
