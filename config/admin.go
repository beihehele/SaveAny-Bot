package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/krau/SaveAny-Bot/pkg/adminauth"
)

// AdminConfig configures the independently authenticated management server.
type AdminConfig struct {
	Enable       bool          `toml:"enable" mapstructure:"enable"`
	Host         string        `toml:"host" mapstructure:"host"`
	Port         int           `toml:"port" mapstructure:"port"`
	Password     string        `toml:"password" mapstructure:"password" json:"-"`
	PasswordHash string        `toml:"password_hash" mapstructure:"password_hash" json:"-"`
	SessionTTL   time.Duration `toml:"session_ttl" mapstructure:"session_ttl"`
	SecureCookie bool          `toml:"secure_cookie" mapstructure:"secure_cookie"`
}

// Validate rejects insecure or malformed enabled admin configurations.
func (c AdminConfig) Validate() error {
	if !c.Enable {
		return nil
	}
	if strings.TrimSpace(c.Host) == "" || c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("admin requires a host and port between 1 and 65535")
	}
	if c.SessionTTL < 5*time.Minute || c.SessionTTL > 24*time.Hour {
		return fmt.Errorf("admin.session_ttl must be between 5m and 24h")
	}
	if (c.Password == "") == (c.PasswordHash == "") {
		return fmt.Errorf("admin requires exactly one of password or password_hash")
	}
	if c.Password != "" && (len(c.Password) < 12 || len(c.Password) > 1024) {
		return fmt.Errorf("admin password must contain 12 to 1024 bytes")
	}
	if c.PasswordHash != "" {
		if _, err := adminauth.Parse(c.PasswordHash); err != nil {
			return err
		}
	}
	return nil
}
