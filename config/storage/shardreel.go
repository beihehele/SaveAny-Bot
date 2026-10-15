package storage

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	storenum "github.com/krau/SaveAny-Bot/pkg/enums/storage"
)

// ShardReelStorageConfig configures a personal persistent upload outbox.
type ShardReelStorageConfig struct {
	BaseConfig      `mapstructure:",squash"`
	URL             string `toml:"url" mapstructure:"url" json:"url"`
	APIKey          string `toml:"api_key" mapstructure:"api_key" json:"-"`
	SpoolPath       string `toml:"spool_path" mapstructure:"spool_path" json:"spool_path"`
	MaxPendingBytes int64  `toml:"max_pending_bytes" mapstructure:"max_pending_bytes" json:"max_pending_bytes"`
}

func (c *ShardReelStorageConfig) GetType() storenum.StorageType { return storenum.Shardreel }
func (c *ShardReelStorageConfig) GetName() string               { return c.Name }
func (c *ShardReelStorageConfig) Validate() error {
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("shardreel requires an HTTP(S) URL without credentials, query or fragment")
	}
	if strings.TrimSpace(c.Name) == "" || strings.ContainsAny(c.Name, "/\\\x00") || c.Name == "." || c.Name == ".." || len(c.APIKey) < 32 {
		return fmt.Errorf("shardreel requires a name and an API key of at least 32 bytes")
	}
	if c.SpoolPath == "" {
		c.SpoolPath = filepath.Join("data", "shardreel", c.Name)
	}
	if c.MaxPendingBytes == 0 {
		c.MaxPendingBytes = 20 << 30
	}
	if c.MaxPendingBytes < 1 {
		return fmt.Errorf("shardreel max_pending_bytes must be positive")
	}
	return nil
}
