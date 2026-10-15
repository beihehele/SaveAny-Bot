//go:build windows

package fsutil

import "golang.org/x/sys/windows"

// AvailableDiskBytes reports space available to the current process.
func AvailableDiskBytes(path string) (uint64, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var available uint64
	if err := windows.GetDiskFreeSpaceEx(name, &available, nil, nil); err != nil {
		return 0, err
	}
	return available, nil
}
