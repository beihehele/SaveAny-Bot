package admin

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

func TestConsoleLinkedAssets(t *testing.T) {
	s := testServer(t)
	index := perform(s, "GET", "/admin/", "", nil, "", "")
	if index.Code != http.StatusOK {
		t.Fatalf("index status = %d", index.Code)
	}
	links := regexp.MustCompile(`(?:src|href)="(/admin/assets/[^"]+)"`).FindAllStringSubmatch(index.Body.String(), -1)
	if len(links) == 0 {
		t.Fatal("console has no linked assets")
	}
	for _, link := range links {
		t.Run(link[1], func(t *testing.T) {
			response := perform(s, "GET", link[1], "", nil, "", "")
			if response.Code != http.StatusOK || response.Body.Len() == 0 {
				t.Fatalf("asset status = %d, bytes = %d", response.Code, response.Body.Len())
			}
			mime := "text/css"
			if strings.HasSuffix(link[1], ".js") {
				mime = "text/javascript"
			}
			if !strings.HasPrefix(response.Header().Get("Content-Type"), mime) {
				t.Fatalf("asset MIME = %q, want %s", response.Header().Get("Content-Type"), mime)
			}
			if response.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("asset missing nosniff protection")
			}
		})
	}
}

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
		"index.html":    `data-i18n(?:-placeholder|-aria-label)?="([^"]+)"`,
		"app.js":        `\bt\("([^"]+)"\)`,
		"management.js": `\bt\("([^"]+)"\)`,
		"preferences.js": `\bt\("([^"]+)"\)`,
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
