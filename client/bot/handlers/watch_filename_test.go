package handlers

import (
	"testing"
)

func TestUniqueAlbumFileName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		id   int
		want string
	}{
		{name: "with ext", in: "hello.mp4", id: 42, want: "hello_42.mp4"},
		{name: "already unique", in: "hello_42.mp4", id: 42, want: "hello_42.mp4"},
		{name: "no ext", in: "hello", id: 7, want: "hello_7"},
		{name: "empty", in: "", id: 1, want: ""},
		{name: "zero id", in: "hello.mp4", id: 0, want: "hello.mp4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := uniqueAlbumFileName(tt.in, tt.id); got != tt.want {
				t.Fatalf("got %q want %q", got, tt.want)
			}
		})
	}
}

func TestAlbumCaptionText(t *testing.T) {
	if got := albumCaptionText(nil); got != "" {
		t.Fatalf("got %q", got)
	}
}
