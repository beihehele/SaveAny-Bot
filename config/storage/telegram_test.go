package storage

import "testing"

func TestTelegramConfiguration(t *testing.T) {
	raw := map[string]any{"name": "TG收藏", "type": "telegram", "enable": true, "chat_id": int64(-1001234567890), "topic_id": 2}
	cfgs, err := LoadStorageConfigs([]map[string]any{raw})
	if err != nil || len(cfgs) != 1 {
		t.Fatalf("config: %v %v", cfgs, err)
	}
	cfg := cfgs[0].(*TelegramStorageConfig)
	if cfg.ChatID != -1001234567890 || cfg.TopicID != 2 {
		t.Fatalf("target lost: %+v", cfg)
	}
	for _, key := range []string{"base_path", "api_key", "unknown"} {
		raw[key] = "unsupported"
		if _, err := LoadStorageConfigs([]map[string]any{raw}); err == nil {
			t.Fatalf("accepted unused %s", key)
		}
		delete(raw, key)
	}
	for _, id := range []int64{0, 42, -123} {
		cfg.ChatID = id
		if cfg.Validate() == nil {
			t.Fatalf("accepted chat %d", id)
		}
	}
	cfg.ChatID = -1001234567890
	cfg.TopicID = -1
	if cfg.Validate() == nil {
		t.Fatal("negative topic accepted")
	}
}
