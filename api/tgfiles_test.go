package api

import "testing"

func TestTelegramMessageLinkDomains(t *testing.T) {
	for _, domain := range []string{"t.me", "telegram.me"} {
		for _, scheme := range []string{"http", "https"} {
			for _, suffix := range []string{"/c/123456789/123", "/c/123456789/456/123"} {
				link := scheme + "://" + domain + suffix
				if !isValidMessageLink(link) {
					t.Fatalf("rejected supported link %q", link)
				}
				chatID, msgID, err := ParseMessageLink(t.Context(), link)
				if err != nil || chatID != -1000123456789 || msgID != 123 {
					t.Fatalf("ParseMessageLink(%q) = (%d, %d, %v)", link, chatID, msgID, err)
				}
			}
		}
	}
	for _, link := range []string{"https://telegram.me.evil/c/123/1", "https://t.me.evil/c/123/1", "https://example.com/c/123/1"} {
		if isValidMessageLink(link) {
			t.Errorf("accepted unsupported host %q", link)
		}
	}
}
