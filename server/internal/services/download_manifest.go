package services

import (
	"errors"
	pathpkg "path"
	"sort"
	"strings"

	"github.com/yuanjing-hash/ohmycine/server/internal/medialibrary"
	downloadpkg "github.com/yuanjing-hash/ohmycine/server/pkg/downloader"
)

const minimumAutomaticTransferVideoBytes int64 = 16 * 1024 * 1024

// selectDownloadPackageManifest turns a provider's complete output into the
// media package that is allowed to enter automatic organization. The same
// selection is used for local downloaders and native cloud offline downloads.
func selectDownloadPackageManifest(manifest downloadpkg.Manifest, mediaType string) (downloadpkg.Manifest, error) {
	videos := make([]downloadpkg.File, 0, len(manifest.Files))
	for _, file := range manifest.Files {
		if isVideoFile(file.RelativePath) && file.Size > 0 {
			videos = append(videos, file)
		}
	}
	if len(videos) == 0 {
		return downloadpkg.Manifest{}, errors.New("manifest contains no video")
	}
	sort.SliceStable(videos, func(left, right int) bool { return videos[left].Size > videos[right].Size })
	anchor := videos[0]
	if anchor.Size < minimumAutomaticTransferVideoBytes {
		return downloadpkg.Manifest{}, errors.New("manifest contains no plausible primary video")
	}

	accepted := map[string]struct{}{}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	switch mediaType {
	case "movie":
		accepted[normalizedManifestPath(anchor.RelativePath)] = struct{}{}
	case "tv":
		minimumEpisodeBytes := anchor.Size / 20
		if minimumEpisodeBytes < minimumAutomaticTransferVideoBytes {
			minimumEpisodeBytes = minimumAutomaticTransferVideoBytes
		}
		for _, file := range videos {
			if file.Size < minimumEpisodeBytes {
				continue
			}
			_, _, _, episode := medialibrary.ParseFilename(pathpkg.Base(normalizedManifestPath(file.RelativePath)), "/"+strings.TrimLeft(normalizedManifestPath(file.RelativePath), "/"))
			if episode != nil {
				accepted[normalizedManifestPath(file.RelativePath)] = struct{}{}
			}
		}
		if len(accepted) == 0 {
			accepted[normalizedManifestPath(anchor.RelativePath)] = struct{}{}
		}
	default:
		return downloadpkg.Manifest{}, errors.New("manifest media type is not trustworthy")
	}

	acceptedByDirectory := map[string][]string{}
	for videoPath := range accepted {
		directory := strings.ToLower(pathpkg.Dir(videoPath))
		stem := strings.ToLower(strings.TrimSuffix(pathpkg.Base(videoPath), pathpkg.Ext(videoPath)))
		acceptedByDirectory[directory] = append(acceptedByDirectory[directory], stem)
	}

	selected := make([]downloadpkg.File, 0, len(accepted)+4)
	for _, file := range manifest.Files {
		normalized := normalizedManifestPath(file.RelativePath)
		if _, ok := accepted[normalized]; ok {
			selected = append(selected, file)
			continue
		}
		if sidecarBelongsToAcceptedMedia(normalized, acceptedByDirectory) {
			selected = append(selected, file)
		}
	}
	if len(selected) == 0 {
		return downloadpkg.Manifest{}, errors.New("manifest selection is empty")
	}
	manifest.Files = selected
	return manifest, nil
}

func normalizedManifestPath(value string) string {
	return pathpkg.Clean(strings.TrimLeft(strings.ReplaceAll(value, "\\", "/"), "/"))
}

func sidecarBelongsToAcceptedMedia(relativePath string, acceptedByDirectory map[string][]string) bool {
	extension := strings.ToLower(pathpkg.Ext(relativePath))
	if !isAutomaticTransferSubtitleFile(relativePath) && extension != ".jpg" {
		return false
	}
	stems := acceptedByDirectory[strings.ToLower(pathpkg.Dir(relativePath))]
	if len(stems) == 0 {
		return false
	}
	stem := strings.ToLower(strings.TrimSuffix(pathpkg.Base(relativePath), pathpkg.Ext(relativePath)))
	for _, videoStem := range stems {
		if stem == videoStem || strings.HasPrefix(stem, videoStem+".") || strings.HasPrefix(stem, videoStem+"-") || strings.HasPrefix(stem, videoStem+" ") {
			return true
		}
	}
	if extension == ".jpg" && len(stems) == 1 {
		switch stem {
		case "poster", "folder", "cover", "fanart", "backdrop":
			return true
		}
	}
	return false
}
