package fsutil

import (
	"errors"
	"os"

	"github.com/gabriel-vasile/mimetype"
)

func DetectFileExt(fp string) string {
	mt, err := mimetype.DetectFile(fp)
	if err != nil {
		return ""
	}
	return mt.Extension()
}

type File struct {
	*os.File
}

func (f *File) Remove() error {
	return os.Remove(f.Name())
}

func (f *File) CloseAndRemove() error {
	closeErr := f.Close()
	if errors.Is(closeErr, os.ErrClosed) {
		closeErr = nil
	}
	return errors.Join(closeErr, f.Remove())
}

// CreateTempFile creates an exclusively owned cache file. Call CloseAndRemove
// when finished; resource filenames must not determine shared cache paths.
func CreateTempFile(dir, pattern string) (*File, error) {
	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return nil, err
	}
	return &File{File: file}, nil
}
