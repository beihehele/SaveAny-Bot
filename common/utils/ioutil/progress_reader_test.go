package ioutil

import (
	"bytes"
	"io"
	"testing"
)

func TestProgressReaderTracksSeekPosition(t *testing.T) {
	var reported int64
	r := NewProgressReader(bytes.NewReader([]byte("abcdef")), 6, func(read, total int64) {
		reported = read
		if total != 6 {
			t.Errorf("total = %d, want 6", total)
		}
	})
	for _, tc := range []struct {
		name         string
		offset       int64
		whence, read int
		want         int64
	}{
		{"initial", 0, io.SeekStart, 6, 6},
		{"rewind", 0, io.SeekStart, 3, 3},
		{"relative", -1, io.SeekCurrent, 2, 4},
		{"from end", -1, io.SeekEnd, 1, 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := r.Seek(tc.offset, tc.whence); err != nil {
				t.Fatal(err)
			}
			if _, err := io.ReadFull(r, make([]byte, tc.read)); err != nil {
				t.Fatal(err)
			}
			if r.BytesRead() != tc.want || reported != tc.want {
				t.Fatalf("bytes=%d callback=%d, want %d", r.BytesRead(), reported, tc.want)
			}
		})
	}
	before := r.BytesRead()
	if _, err := r.Seek(-1, io.SeekStart); err == nil {
		t.Fatal("negative seek succeeded")
	}
	if r.BytesRead() != before {
		t.Fatal("failed seek changed progress")
	}
}
