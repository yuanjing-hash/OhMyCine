//go:build !webui

package webui

import "io/fs"

func Assets() (fs.FS, bool) { return nil, false }
