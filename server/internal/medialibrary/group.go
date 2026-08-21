package medialibrary

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// RecognitionUnit is a provider-neutral group of file facts that represents
// one TMDB lookup. SourceKey is an opaque, library-scoped stable identifier;
// callers must not expose it as a provider item ID or path.
type RecognitionUnit struct {
	SourceKey        string
	InputFingerprint string
	PackageName      string
	MediaTypeHint    string
	Files            []File
}

// GroupRecognitionUnits groups already-enumerated facts without performing
// network I/O. It deliberately uses only provider-relative structure and the
// existing filename parser's structural hints; final identity remains the
// responsibility of the shared recognizer.
func GroupRecognitionUnits(files []File) []RecognitionUnit {
	type bucket struct {
		key       string
		name      string
		mediaType string
		files     []File
	}
	buckets := make(map[string]*bucket)
	for _, file := range files {
		relative := normalizeFactPath(file.RelativePath)
		parsed := ParseMedia(path.Base(relative), relative)
		key, name := recognitionGroup(relative, parsed)
		bucketKey := parsed.MediaType + "\x00" + key
		current := buckets[bucketKey]
		if current == nil {
			current = &bucket{key: key, name: name, mediaType: parsed.MediaType}
			buckets[bucketKey] = current
		}
		current.files = append(current.files, file)
	}

	units := make([]RecognitionUnit, 0, len(buckets))
	for _, current := range buckets {
		sort.Slice(current.files, func(i, j int) bool {
			return normalizeFactPath(current.files[i].RelativePath) < normalizeFactPath(current.files[j].RelativePath)
		})
		units = append(units, RecognitionUnit{
			SourceKey:        recognitionSourceKey(current.key, current.files),
			InputFingerprint: recognitionInputFingerprint(current.name, current.files),
			PackageName:      current.name,
			MediaTypeHint:    current.mediaType,
			Files:            append([]File(nil), current.files...),
		})
	}
	sort.Slice(units, func(i, j int) bool {
		if units[i].PackageName == units[j].PackageName {
			return units[i].SourceKey < units[j].SourceKey
		}
		return units[i].PackageName < units[j].PackageName
	})
	return units
}

func recognitionGroup(relative string, parsed ParsedMedia) (string, string) {
	directory := path.Dir(relative)
	if outer, ok := discOuterDirectory(relative); ok {
		return "disc:" + outer, path.Base(outer)
	}
	if parsed.MediaType == "tv" {
		seriesDirectory := directory
		base := strings.ToLower(path.Base(directory))
		if strings.HasPrefix(base, "season ") || strings.HasPrefix(base, "season.") || strings.HasPrefix(base, "s0") {
			seriesDirectory = path.Dir(directory)
		}
		if seriesDirectory == "." || seriesDirectory == "/" {
			name := strings.TrimSpace(parsed.SeriesTitle)
			if name == "" {
				name = strings.TrimSuffix(path.Base(relative), path.Ext(relative))
			}
			return "tv-root:" + strings.ToLower(name), name
		}
		return "tv-dir:" + seriesDirectory, path.Base(seriesDirectory)
	}
	if directory == "." || directory == "/" {
		name := strings.TrimSuffix(path.Base(relative), path.Ext(relative))
		return "file:" + relative, name
	}
	return "movie-dir:" + directory, path.Base(directory)
}

func discOuterDirectory(relative string) (string, bool) {
	parts := strings.Split(strings.Trim(relative, "/"), "/")
	for index, part := range parts {
		if strings.EqualFold(part, "BDMV") || strings.EqualFold(part, "VIDEO_TS") {
			if index == 0 {
				return "", false
			}
			return "/" + strings.Join(parts[:index], "/"), true
		}
	}
	return "", false
}

func recognitionSourceKey(groupKey string, files []File) string {
	stableIDs := make([]string, 0, len(files))
	allStable := len(files) > 0
	for _, file := range files {
		if !file.ProviderIDStable || strings.TrimSpace(file.ProviderID) == "" {
			allStable = false
			break
		}
		stableIDs = append(stableIDs, file.ProviderID)
	}
	identity := "path\x00" + groupKey
	if allStable {
		sort.Strings(stableIDs)
		// One stable anchor keeps a work identity unchanged when later episodes
		// or sidecars join the same provider directory. The provider ID remains
		// inside the hash and is never exposed.
		identity = "provider-anchor\x00" + stableIDs[0]
	}
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:20])
}

func recognitionInputFingerprint(packageName string, files []File) string {
	hash := sha256.New()
	hash.Write([]byte(packageName))
	for _, file := range files {
		hash.Write([]byte{'\x00'})
		hash.Write([]byte(normalizeFactPath(file.RelativePath)))
		hash.Write([]byte{'\x00'})
		hash.Write([]byte(strconv.FormatInt(file.Size, 10)))
		hash.Write([]byte{'\x00'})
		hash.Write([]byte(file.ModifiedAt.UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func normalizeFactPath(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), "\\", "/")
	return "/" + strings.TrimLeft(path.Clean("/"+strings.TrimLeft(value, "/")), "/")
}
