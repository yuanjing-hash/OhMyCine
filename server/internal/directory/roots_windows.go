//go:build windows

package directory

import (
	"context"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func nativeRoots(ctx context.Context) ([]Root, error) {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil, &AdapterError{Kind: ErrorUnavailable}
	}
	items := make([]Root, 0, 8)
	for index := 0; index < 26; index++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if mask&(1<<index) == 0 {
			continue
		}
		path := fmt.Sprintf("%c:\\", 'A'+index)
		pointer, pointerErr := windows.UTF16PtrFromString(path)
		kind := "unknown"
		if pointerErr == nil {
			switch windows.GetDriveType(pointer) {
			case windows.DRIVE_FIXED:
				kind = "fixed"
			case windows.DRIVE_REMOVABLE:
				kind = "removable"
			case windows.DRIVE_REMOTE:
				kind = "network"
			case windows.DRIVE_CDROM:
				kind = "optical"
			case windows.DRIVE_RAMDISK:
				kind = "ramdisk"
			}
		}
		item := Root{Path: path, Name: path, Kind: kind, Selectable: true, Enterable: true}
		info, statErr := os.Lstat(path)
		if statErr != nil || !info.IsDir() || isUnsafeLink(path, info) {
			item.Selectable = false
			item.Enterable = false
			item.Reason = "root_unavailable"
		}
		items = append(items, item)
	}
	return items, nil
}
