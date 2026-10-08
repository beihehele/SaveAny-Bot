package parser

import "testing"

func TestResourceIDStable(t *testing.T) {
	first := Resource{URL: "https://example.com/file", Filename: "file.bin",
		Hash:    map[string]string{"sha256": "abc", "md5": "def", "sha1": "ghi"},
		Headers: map[string]string{"Authorization": "token", "Referer": "page", "User-Agent": "bot"}}
	second := first
	second.Hash = map[string]string{"sha1": "ghi", "md5": "def", "sha256": "abc"}
	second.Headers = map[string]string{"User-Agent": "bot", "Referer": "page", "Authorization": "token"}
	want := first.ID()
	for range 100 {
		if first.ID() != want || second.ID() != want {
			t.Fatal("resource ID changed with map iteration or insertion order")
		}
	}
	second.Headers["Authorization"] = "different"
	if second.ID() == want {
		t.Fatal("different request headers must affect resource identity")
	}
}
