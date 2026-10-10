package config

import (
	"fmt"

	"github.com/spf13/viper"
)

// validateSupportedFeatures only diagnoses obsolete configuration; it provides no
// removed implementation and never modifies the user's file.
func validateSupportedFeatures(v *viper.Viper) error {
	for _, key := range []string{"aria2.enable", "parser.plugin_enable", "parsers.plugin_enable"} {
		if v.GetBool(key) {
			return fmt.Errorf("%s enables a removed feature; this build supports Telegram and local storage only", key)
		}
	}
	for _, key := range []string{"ytdlp", "ytdlp.max_height", "ytdlp.format", "ytdlp.recode"} {
		if v.IsSet(key) {
			return fmt.Errorf("ytdlp configuration is no longer supported; remove that section and its environment overrides")
		}
	}
	return nil
}
