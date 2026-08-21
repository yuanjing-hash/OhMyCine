package services

import (
	pathpkg "path"
	"strings"
)

// Keep provider-neutral media extension policy in one place. The STRM source
// asset defaults intentionally remain narrower than the subtitle formats that
// automatic organization can preserve.
var (
	defaultVideoExtensions = []string{
		".mp4", ".mkv", ".ts", ".iso", ".rmvb", ".avi", ".mov", ".mpeg", ".mpg",
		".wmv", ".3gp", ".asf", ".m4v", ".flv", ".m2ts", ".tp", ".f4v",
	}
	defaultSourceAssetExtensions        = []string{"srt", "ssa", "ass", "jpg"}
	automaticTransferSubtitleExtensions = map[string]struct{}{
		".ass": {},
		".idx": {},
		".srt": {},
		".ssa": {},
		".sub": {},
		".sup": {},
		".vtt": {},
	}
	videoExtensionSet = extensionSet(defaultVideoExtensions)
)

func extensionSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[strings.ToLower(value)] = struct{}{}
	}
	return result
}

func isVideoFile(value string) bool {
	_, ok := videoExtensionSet[strings.ToLower(pathpkg.Ext(value))]
	return ok
}

func isAutomaticTransferSubtitleFile(value string) bool {
	_, ok := automaticTransferSubtitleExtensions[strings.ToLower(pathpkg.Ext(value))]
	return ok
}

func isAutomaticTransferSidecarFile(value string) bool {
	return isAutomaticTransferSubtitleFile(value) || strings.EqualFold(pathpkg.Ext(value), ".jpg")
}
