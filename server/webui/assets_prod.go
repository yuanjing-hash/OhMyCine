//go:build webui

package webui

import (
	"embed"
	"io/fs"
)

//go:embed dist
var embedded embed.FS

func Assets() (fs.FS, bool) {
	assets, err := fs.Sub(embedded, "dist")
	return assets, err == nil
}
