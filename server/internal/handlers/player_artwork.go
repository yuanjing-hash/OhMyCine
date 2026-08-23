package handlers

import (
	"bytes"
	"embed"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yuanjing-hash/ohmycine/server/internal/services"
)

//go:embed artwork/*.png
var playerLibraryArtworkFS embed.FS

var playerLibraryArtworkNames = map[string]string{
	"library-local.png":   "artwork/library-local.png",
	"library-cloud.png":   "artwork/library-cloud.png",
	"category-cinema.png": "artwork/category-cinema.png",
}

func (a *API) PlayerLibraryArtwork(c *gin.Context) {
	path, ok := playerLibraryArtworkNames[c.Param("name")]
	if !ok {
		c.Status(http.StatusNotFound)
		return
	}
	data, err := playerLibraryArtworkFS.ReadFile(path)
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("Cache-Control", "public, max-age=86400")
	c.Header("Content-Type", "image/png")
	c.Header("X-Content-Type-Options", "nosniff")
	http.ServeContent(c.Writer, c.Request, c.Param("name"), time.Time{}, bytes.NewReader(data))
}

func (a *API) PluginLibraryArtwork(c *gin.Context) {
	if a.pluginRepositories == nil {
		c.Status(http.StatusNotFound)
		return
	}
	artwork, err := a.pluginRepositories.OpenLibraryArtwork(c.Param("digest"))
	if err != nil {
		if services.ErrorCode(err) == services.CodeNotFound {
			c.Status(http.StatusNotFound)
			return
		}
		writeError(c, a.log, err)
		return
	}
	defer artwork.File.Close()
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Header("Content-Type", artwork.ContentType)
	c.Header("Content-Length", strconv.FormatInt(artwork.Size, 10))
	c.Header("X-Content-Type-Options", "nosniff")
	http.ServeContent(c.Writer, c.Request, artwork.Name, artwork.ModifiedAt, artwork.File)
}
