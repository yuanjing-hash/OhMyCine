package services

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yuanjing-hash/ohmycine/server/internal/models"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/contract"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/packagefs"
	"gorm.io/gorm"
)

type PluginLibraryArtwork struct {
	File        *os.File
	Name        string
	ContentType string
	Size        int64
	ModifiedAt  time.Time
}

// OpenLibraryArtwork exposes only the active package's declared inert raster
// asset. The content-addressed public URL contains no credentials or package
// path, and every read revalidates the managed tree before opening the file.
func (s *PluginRepositoryService) OpenLibraryArtwork(packageSHA256 string) (PluginLibraryArtwork, error) {
	if _, err := contract.DecodeSHA256(packageSHA256); err != nil {
		return PluginLibraryArtwork{}, appError(CodeNotFound, "插件媒体库封面不存在", nil)
	}
	var pluginPackage models.PluginPackage
	err := s.db.Table("plugin_packages").
		Select("plugin_packages.*").
		Joins("JOIN plugin_installations ON plugin_installations.active_package_id = plugin_packages.id AND plugin_installations.status = ?", models.PluginInstallationEnabled).
		Where("plugin_packages.package_sha256 = ?", packageSHA256).
		First(&pluginPackage).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return PluginLibraryArtwork{}, appError(CodeNotFound, "插件媒体库封面不存在", err)
		}
		return PluginLibraryArtwork{}, err
	}
	manifest, err := contract.ParseManifest([]byte(pluginPackage.ManifestJSON))
	if err != nil || manifest.LibraryArtwork == "" || manifest.PackageSHA256 != packageSHA256 {
		return PluginLibraryArtwork{}, appError(CodeNotFound, "插件媒体库封面不存在", err)
	}
	if err := packagefs.ValidateManagedPackage(s.pluginRoot, pluginPackage.PackagePath, manifest, pluginPackage.ExtractedTreeSHA256); err != nil {
		return PluginLibraryArtwork{}, appError(CodePluginPackageInvalid, "插件媒体库封面不可用", err)
	}
	target := filepath.Join(pluginPackage.PackagePath, filepath.FromSlash(manifest.LibraryArtwork))
	file, err := os.Open(target)
	if err != nil {
		return PluginLibraryArtwork{}, appError(CodePluginAssetUnavailable, "插件媒体库封面不可用", err)
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > packagefs.MaxArtworkBytes {
		_ = file.Close()
		return PluginLibraryArtwork{}, appError(CodePluginAssetUnavailable, "插件媒体库封面不可用", err)
	}
	contentType := ""
	switch strings.ToLower(filepath.Ext(target)) {
	case ".png":
		contentType = "image/png"
	case ".jpg", ".jpeg":
		contentType = "image/jpeg"
	case ".webp":
		contentType = "image/webp"
	}
	if contentType == "" {
		_ = file.Close()
		return PluginLibraryArtwork{}, appError(CodePluginAssetUnavailable, "插件媒体库封面不可用", nil)
	}
	return PluginLibraryArtwork{File: file, Name: filepath.Base(target), ContentType: contentType, Size: info.Size(), ModifiedAt: info.ModTime()}, nil
}
