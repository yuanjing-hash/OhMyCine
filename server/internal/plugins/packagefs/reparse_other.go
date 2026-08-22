//go:build !windows

package packagefs

import "os"

func isReparsePoint(os.FileInfo) bool { return false }
