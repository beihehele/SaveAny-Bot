//go:build !linux && !windows

package fsutil

import "errors"

func AvailableDiskBytes(string) (uint64, error) {
	return 0, errors.New("disk capacity sampling unsupported on this platform")
}
