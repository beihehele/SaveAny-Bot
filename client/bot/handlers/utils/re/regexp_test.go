package re

import "testing"

func TestTelegramMessageLinkDomains(t *testing.T) {
	for _, link := range []string{
		"https://t.me/example/123", "http://telegram.me/example/123",
		"https://telegram.me/c/123456789/123", "https://t.me/c/123456789/123/456?single",
	} {
		if got := TgMessageLinkRegexp.FindString(link); got != link {
			t.Errorf("matched %q, want %q", got, link)
		}
	}
	for _, link := range []string{"https://tXme/example/123", "https://telegramXme/example/123", "https://example.com/example/123"} {
		if TgMessageLinkRegexp.MatchString(link) {
			t.Errorf("unexpected match: %q", link)
		}
	}
}
