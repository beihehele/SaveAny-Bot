package telegram

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateSplitZip(t *testing.T) {
	payload := bytes.Repeat([]byte("SaveAny-Bot split archive fixture\n"), 512)
	for _, partSize := range []int64{1024, 4096, 65536} {
		t.Run(fmt.Sprintf("part_size_%d", partSize), func(t *testing.T) {
			base := filepath.Join(t.TempDir(), "nested", "archive")
			// Each case starts with a fresh reader, including the single-part case.
			if err := CreateSplitZip(t.Context(), bytes.NewReader(payload), int64(len(payload)), "sample.dat", base, partSize); err != nil {
				t.Fatal(err)
			}
			parts, err := filepath.Glob(base + ".zip*")
			if err != nil {
				t.Fatal(err)
			}
			if len(parts) == 0 {
				t.Fatal("no archive parts were created")
			}
			if partSize < int64(len(payload)) && len(parts) < 2 {
				t.Fatal("archive was not split")
			}
			if partSize > int64(len(payload))*2 && len(parts) != 1 {
				t.Fatalf("expected a single archive, got %d parts", len(parts))
			}
			var combined bytes.Buffer
			for i, path := range parts {
				want := fmt.Sprintf("%s.zip.%03d", base, i+1)
				if len(parts) == 1 {
					want = base + ".zip"
				}
				if path != want {
					t.Fatalf("part name=%q want %q", path, want)
				}
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if len(data) == 0 || int64(len(data)) > partSize {
					t.Fatalf("invalid part size %d, maximum %d", len(data), partSize)
				}
				if i < len(parts)-1 && int64(len(data)) != partSize {
					t.Fatalf("non-final part size=%d want %d", len(data), partSize)
				}
				combined.Write(data)
			}
			archive, err := zip.NewReader(bytes.NewReader(combined.Bytes()), int64(combined.Len()))
			if err != nil {
				t.Fatalf("reassembled archive is invalid: %v", err)
			}
			if len(archive.File) != 1 || archive.File[0].Name != "sample.dat" {
				t.Fatalf("unexpected archive entries: %v", archive.File)
			}
			reader, err := archive.File[0].Open()
			if err != nil {
				t.Fatal(err)
			}
			restored, readErr := io.ReadAll(reader)
			closeErr := reader.Close()
			if readErr != nil {
				t.Fatal(readErr)
			}
			if closeErr != nil {
				t.Fatal(closeErr)
			}
			if !bytes.Equal(restored, payload) {
				t.Fatal("archive contents changed after splitting and reassembly")
			}
		})
	}
}
