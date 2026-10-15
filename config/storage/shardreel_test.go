package storage

import "testing"

func TestShardReelConfigurationUsesCurrentStrictSchema(t *testing.T) {
	raw := map[string]any{"name": "drive", "type": "shardreel", "enable": true, "url": "http://127.0.0.1:8080", "api_key": "isolated-storage-integration-key-2026"}
	configs, err := LoadStorageConfigs([]map[string]any{raw})
	if err != nil || len(configs) != 1 {
		t.Fatalf("configs=%v err=%v", configs, err)
	}
	cfg := configs[0].(*ShardReelStorageConfig)
	if cfg.Name != "drive" || cfg.SpoolPath == "" || cfg.MaxPendingBytes != 20<<30 {
		t.Fatalf("config=%+v", cfg)
	}
	raw["misspelled_option"] = true
	if _, err := LoadStorageConfigs([]map[string]any{raw}); err == nil {
		t.Fatal("unknown option accepted")
	}
}
