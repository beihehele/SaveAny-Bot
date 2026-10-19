package storage

import (
	"fmt"
	"math"
	"strings"

	"github.com/gotd/td/constant"

	storenum "github.com/krau/SaveAny-Bot/pkg/enums/storage"
)

// TelegramStorageConfig names one fixed collection group/channel and optional topic.
type TelegramStorageConfig struct {
	BaseConfig `mapstructure:",squash"`
	ChatID     int64 `toml:"chat_id" mapstructure:"chat_id" json:"chat_id"`
	TopicID    int   `toml:"topic_id" mapstructure:"topic_id" json:"topic_id"`
}

func (c *TelegramStorageConfig) GetType() storenum.StorageType { return storenum.Telegram }
func (c *TelegramStorageConfig) GetName() string               { return c.Name }
func (c *TelegramStorageConfig) Validate() error {
	if strings.TrimSpace(c.Name) == "" || !constant.TDLibPeerID(c.ChatID).IsChannel() || c.TopicID < 0 || c.TopicID > math.MaxInt32 {
		return fmt.Errorf("telegram storage requires a name, a channel/supergroup chat_id (-100...) and a topic_id between 0 and 2147483647")
	}
	return nil
}
