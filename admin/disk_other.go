//go:build !linux && !windows

package admin

import "errors"

func availableDiskBytes(string) (uint64, error) {
	return 0, errors.New("disk capacity sampling unsupported on this platform")
}
