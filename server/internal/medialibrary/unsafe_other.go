//go:build !windows

package medialibrary

import "os"

func isUnsafePath(_ string, entry os.DirEntry) bool { return entry.Type()&os.ModeSymlink != 0 }
