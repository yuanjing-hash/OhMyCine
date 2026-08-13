//go:build !windows

package directory

import "os"

func isUnsafeLink(_ string, info os.FileInfo) bool { return info.Mode()&os.ModeSymlink != 0 }
