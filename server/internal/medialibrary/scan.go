package medialibrary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	storagefs "github.com/yuanjing-hash/ohmycine/server/internal/storage"
)

const (
	MaxEntries = 250000
	MaxDepth   = 64
)

var episodePattern = regexp.MustCompile(`(?i)(?:^|[. _-])S(\d{1,2})E(\d{1,3})(?:[. _-]|$)`)

type File struct {
	RelativePath string
	ProviderID   string
	Size         int64
	ModifiedAt   time.Time
	MediaType    string
	Title        string
	Season       *int
	Episode      *int
}
type Result struct {
	Files   []File
	Partial bool
}

func ScanLocal(ctx context.Context, storageRoot, relativeRoot string, recursive bool, extensions, ignores []string) (Result, error) {
	root, err := ResolveRoot(storageRoot, relativeRoot)
	if err != nil {
		return Result{}, err
	}
	extensionsSet := map[string]struct{}{}
	for _, ext := range extensions {
		extensionsSet[strings.ToLower(ext)] = struct{}{}
	}
	result := Result{Files: make([]File, 0)}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		providerRel := "/" + filepath.ToSlash(rel)
		if shouldIgnore(providerRel, ignores) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		depth := strings.Count(filepath.ToSlash(rel), "/") + 1
		if depth > MaxDepth {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || isUnsafePath(path, entry) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if !recursive && filepath.Dir(rel) == "." {
				return filepath.SkipDir
			}
			return nil
		}
		if _, ok := extensionsSet[strings.ToLower(filepath.Ext(entry.Name()))]; !ok {
			return nil
		}
		if len(result.Files) >= MaxEntries {
			result.Partial = true
			return filepath.SkipAll
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mediaType, title, season, episode := ParseFilename(entry.Name(), providerRel)
		identity := sha256.Sum256([]byte(providerRel + "\x00" + info.ModTime().UTC().Format(time.RFC3339Nano) + "\x00" + strconv.FormatInt(info.Size(), 10)))
		result.Files = append(result.Files, File{RelativePath: providerRel, ProviderID: hex.EncodeToString(identity[:16]), Size: info.Size(), ModifiedAt: info.ModTime().UTC(), MediaType: mediaType, Title: title, Season: season, Episode: episode})
		return nil
	})
	if err != nil && !errors.Is(err, filepath.SkipAll) {
		return Result{}, err
	}
	sort.Slice(result.Files, func(i, j int) bool { return result.Files[i].RelativePath < result.Files[j].RelativePath })
	return result, nil
}

// InspectLocalFile validates and parses one watcher event target without
// walking the whole library tree. The boolean is false for directories,
// ignored paths, and non-media extensions.
func InspectLocalFile(ctx context.Context, libraryRoot, path string, extensions, ignores []string) (File, bool, error) {
	if err := ctx.Err(); err != nil {
		return File{}, false, err
	}
	constrained, err := storagefs.Constrain(libraryRoot, path)
	if err != nil {
		return File{}, false, err
	}
	rel, err := filepath.Rel(libraryRoot, constrained)
	if err != nil || rel == "." {
		return File{}, false, err
	}
	current := libraryRoot
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			return File{}, false, statErr
		}
		entry := dirEntryInfo{info}
		if IsUnsafeDirectory(current, entry) {
			return File{}, false, &storagefs.PathError{Code: storagefs.CodePathReparsePoint}
		}
	}
	info, err := os.Stat(constrained)
	if err != nil {
		return File{}, false, err
	}
	if info.IsDir() {
		return File{}, false, nil
	}
	providerRel := "/" + filepath.ToSlash(rel)
	if shouldIgnore(providerRel, ignores) {
		return File{}, false, nil
	}
	extensionsSet := map[string]struct{}{}
	for _, ext := range extensions {
		extensionsSet[strings.ToLower(ext)] = struct{}{}
	}
	if _, ok := extensionsSet[strings.ToLower(filepath.Ext(info.Name()))]; !ok {
		return File{}, false, nil
	}
	mediaType, title, season, episode := ParseFilename(info.Name(), providerRel)
	identity := sha256.Sum256([]byte(providerRel + "\x00" + info.ModTime().UTC().Format(time.RFC3339Nano) + "\x00" + strconv.FormatInt(info.Size(), 10)))
	return File{RelativePath: providerRel, ProviderID: hex.EncodeToString(identity[:16]), Size: info.Size(), ModifiedAt: info.ModTime().UTC(), MediaType: mediaType, Title: title, Season: season, Episode: episode}, true, nil
}

// IsUnsafeDirectory exposes the same symlink/Reparse Point policy used by the
// scanner so event watchers cannot register a broader tree than scans accept.
func IsUnsafeDirectory(path string, entry os.DirEntry) bool {
	return entry.Type()&os.ModeSymlink != 0 || isUnsafePath(path, entry)
}

func ResolveRoot(storageRoot, relativeRoot string) (string, error) {
	rel := strings.Trim(strings.ReplaceAll(strings.TrimSpace(relativeRoot), "\\", "/"), "/")
	candidate := storageRoot
	if rel != "" {
		candidate = filepath.Join(storageRoot, filepath.FromSlash(rel))
	}
	resolved, err := storagefs.Constrain(storageRoot, candidate)
	if err != nil {
		return "", err
	}
	current := storageRoot
	if rel != "" {
		for _, part := range strings.Split(rel, "/") {
			current = filepath.Join(current, part)
			info, err := os.Lstat(current)
			if err != nil {
				return "", err
			}
			if info.Mode()&os.ModeSymlink != 0 || isUnsafePath(current, dirEntryInfo{info}) {
				return "", &storagefs.PathError{Code: storagefs.CodePathReparsePoint}
			}
		}
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", &storagefs.PathError{Code: storagefs.CodePathNotDirectory}
	}
	return resolved, nil
}

func NormalizeRelativeRoot(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || value == "/" {
		return "/", nil
	}
	if strings.Contains(value, "\x00") || value == ".." || strings.HasPrefix(value, "../") || strings.Contains(value, "/../") || strings.HasSuffix(value, "/..") {
		return "", errors.New("invalid relative root")
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash("/" + strings.TrimLeft(value, "/"))))
	if clean == "/.." || strings.HasPrefix(clean, "/../") {
		return "", errors.New("relative root escapes storage")
	}
	return clean, nil
}
func ParseFilename(name, path string) (string, string, *int, *int) {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	title := strings.TrimSpace(strings.NewReplacer(".", " ", "_", " ").Replace(base))
	match := episodePattern.FindStringSubmatch(" " + base + " ")
	if len(match) == 3 {
		season := atoi(match[1])
		episode := atoi(match[2])
		cleaned := strings.TrimSpace(episodePattern.ReplaceAllString(" "+title+" ", " "))
		if cleaned != "" {
			title = cleaned
		}
		return "tv", title, &season, &episode
	}
	lower := strings.ToLower(filepath.ToSlash(path))
	if strings.Contains(lower, "/season ") || strings.Contains(lower, "/tv/") || strings.Contains(lower, "/剧集/") {
		return "tv", title, nil, nil
	}
	return "movie", title, nil, nil
}
func atoi(value string) int {
	n := 0
	for _, r := range value {
		n = n*10 + int(r-'0')
	}
	return n
}
func shouldIgnore(path string, patterns []string) bool {
	lower := strings.ToLower(path)
	for _, pattern := range patterns {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern != "" && strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

type dirEntryInfo struct{ os.FileInfo }

func (d dirEntryInfo) Type() os.FileMode          { return d.Mode().Type() }
func (d dirEntryInfo) Info() (os.FileInfo, error) { return d.FileInfo, nil }
func (d dirEntryInfo) Name() string               { return d.FileInfo.Name() }
func (d dirEntryInfo) IsDir() bool                { return d.FileInfo.IsDir() }
