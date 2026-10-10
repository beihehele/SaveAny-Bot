package admin

import (
	"encoding/json"
	"regexp"
	"testing"
)

func TestConsoleTranslations(t *testing.T) {
	translations := make(map[string]map[string]string)
	for _, locale := range []string{"en", "zh-Hans"} {
		content, err := assets.ReadFile("static/locales/" + locale + ".json")
		if err != nil {
			t.Fatal(err)
		}
		var words map[string]string
		if err := json.Unmarshal(content, &words); err != nil {
			t.Fatal(err)
		}
		translations[locale] = words
	}
	for locale, words := range translations {
		for key, value := range words {
			if value == "" || translations["en"][key] == "" || translations["zh-Hans"][key] == "" {
				t.Errorf("missing translation for %s/%s", locale, key)
			}
		}
	}
	for file, pattern := range map[string]string{
		"index.html": `data-i18n(?:-placeholder|-aria-label)?="([^"]+)"`,
		"app.js":     `\bt\("([^"]+)"\)`,
	} {
		content, err := assets.ReadFile("static/" + file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range regexp.MustCompile(pattern).FindAllSubmatch(content, -1) {
			if translations["en"][string(match[1])] == "" {
				t.Errorf("%s uses untranslated key %q", file, match[1])
			}
		}
	}
}
