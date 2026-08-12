//go:build windows

package storage

import (
	"golang.org/x/sys/windows"
	"os"
)

func isReparsePoint(path string, info os.FileInfo) bool {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return true
	}
	attributes, err := windows.GetFileAttributes(pointer)
	return err != nil || attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
