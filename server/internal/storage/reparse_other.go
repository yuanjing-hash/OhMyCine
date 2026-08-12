//go:build !windows

package storage

import "os"

func isReparsePoint(_ string, info os.FileInfo) bool { return info.Mode()&os.ModeSymlink != 0 }
