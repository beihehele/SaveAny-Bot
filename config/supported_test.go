package config

import (
	"strings"
	"testing"

	storcfg "github.com/krau/SaveAny-Bot/config/storage"
	"github.com/spf13/viper"
)

func TestRemovedFeaturesFailClosed(t *testing.T) {
	for _, key := range []string{"aria2.enable", "parser.plugin_enable", "parsers.plugin_enable"} {
		t.Run(key, func(t *testing.T) {
			v := viper.New()
			v.Set(key, true)
			if err := validateSupportedFeatures(v); err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("enabled obsolete setting was accepted: %v", err)
			}
			v.Set(key, false)
			if err := validateSupportedFeatures(v); err != nil {
				t.Fatal("inactive historical setting blocked migration", err)
			}
		})
	}
	v := viper.New()
	v.Set("ytdlp.recode", "mp4")
	if err := validateSupportedFeatures(v); err == nil {
		t.Fatal("obsolete video configuration was silently accepted")
	}
}

func TestOnlyLocalStorageEnabled(t *testing.T) {
	for _, typ := range []string{"telegram", "alist", "webdav", "minio", "s3", "rclone"} {
		t.Run(typ, func(t *testing.T) {
			v := viper.New()
			v.Set("storages", []map[string]any{{"name": "legacy", "type": typ, "enable": true}})
			if _, err := storcfg.LoadStorageConfigs(v); err == nil {
				t.Fatal("removed backend accepted")
			}
			v.Set("storages", []map[string]any{{"name": "legacy", "type": typ, "enable": false}})
			storages, err := storcfg.LoadStorageConfigs(v)
			if err != nil || len(storages) != 0 {
				t.Fatal("inactive historical backend created or rejected", err)
			}
		})
	}
}
