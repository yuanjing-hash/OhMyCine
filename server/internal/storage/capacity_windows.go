//go:build windows

package storage

import "golang.org/x/sys/windows"

func diskCapacity(path string) (uint64, uint64, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	var available, total, free uint64
	err = windows.GetDiskFreeSpaceEx(pointer, &available, &total, &free)
	// Match Statfs.Bavail on Unix: report bytes available to the caller rather
	// than volume-wide free bytes, which may include quota-reserved capacity.
	return available, total, err
}
