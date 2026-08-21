package medialibrary

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	storagefs "github.com/yuanjing-hash/ohmycine/server/internal/storage"
	cloudpkg "github.com/yuanjing-hash/ohmycine/server/pkg/cloud"
)

const (
	MaxEntries           = 250000
	MaxDepth             = 64
	ProviderScanPageSize = 1000
)

type File struct {
	RelativePath string
	ProviderID   string
	// ProviderIDStable is true only when the provider guarantees identity
	// across rename/move. Local scan fingerprints intentionally set it false.
	ProviderIDStable bool
	Size             int64
	ModifiedAt       time.Time
	MediaType        string
	Title            string
	WorkKey          string
	SeriesTitle      string
	Season           *int
	Episode          *int
}
type SourceAsset struct {
	RelativePath     string
	ProviderID       string
	ParentProviderID string
	Name             string
	Extension        string
	Size             int64
	ModifiedAt       time.Time
	HashHint         string
}
type Result struct {
	Files   []File
	Assets  []SourceAsset
	Partial bool
}

type providerDirectory struct {
	ID           string
	RelativePath string
	Depth        int
}

// ScanProvider builds a provider-relative file tree from stable provider IDs.
// Providers with a recursive tree endpoint use it for full scans. Other
// providers retain the conservative sequential BFS fallback.
func ScanProvider(ctx context.Context, driver cloudpkg.Driver, rootID string, recursive bool, extensions, assetExtensions, ignores []string) (Result, error) {
	if driver == nil || strings.TrimSpace(rootID) == "" {
		return Result{}, errors.New("provider scanner is not configured")
	}
	extensionsSet := map[string]struct{}{}
	for _, ext := range extensions {
		extensionsSet[strings.ToLower(ext)] = struct{}{}
	}
	assetExtensionsSet := extensionSet(assetExtensions)
	if recursive {
		if bulk, ok := driver.(cloudpkg.BulkTreeDriver); ok {
			return scanProviderTree(ctx, bulk, rootID, extensionsSet, assetExtensionsSet, ignores)
		}
	}
	result := Result{Files: make([]File, 0)}
	queue := []providerDirectory{{ID: rootID}}
	visited := map[string]struct{}{rootID: {}}
	seenItems := 0
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		current := queue[0]
		queue = queue[1:]
		items, err := listProviderDirectory(ctx, driver, current.ID)
		if err != nil {
			return result, err
		}
		for _, item := range items {
			seenItems++
			if seenItems > MaxEntries {
				result.Partial = true
				return sortedResult(result), nil
			}
			relativePath := current.RelativePath + "/" + item.Name
			if shouldIgnore(relativePath, ignores) {
				continue
			}
			if item.IsDir {
				if !recursive || current.Depth+1 >= MaxDepth {
					continue
				}
				if _, exists := visited[item.ID]; exists {
					continue
				}
				visited[item.ID] = struct{}{}
				queue = append(queue, providerDirectory{ID: item.ID, RelativePath: relativePath, Depth: current.Depth + 1})
				continue
			}
			extension := strings.ToLower(filepath.Ext(item.Name))
			if _, ok := extensionsSet[extension]; ok {
				result.Files = append(result.Files, File{RelativePath: relativePath, ProviderID: item.ID, ProviderIDStable: true, Size: item.Size, ModifiedAt: item.ModifiedAt.UTC()})
			} else if _, ok := assetExtensionsSet[extension]; ok {
				result.Assets = append(result.Assets, SourceAsset{RelativePath: relativePath, ProviderID: item.ID, ParentProviderID: current.ID, Name: item.Name, Extension: extension, Size: item.Size, ModifiedAt: item.ModifiedAt.UTC(), HashHint: item.SHA1})
			}
		}
	}
	return sortedResult(result), nil
}

func scanProviderTree(ctx context.Context, driver cloudpkg.BulkTreeDriver, rootID string, extensionsSet, assetExtensionsSet map[string]struct{}, ignores []string) (Result, error) {
	tree, err := driver.ListTree(ctx, rootID, MaxEntries)
	if err != nil {
		return Result{}, err
	}
	result := Result{Files: make([]File, 0, min(len(tree.Entries), MaxEntries)), Partial: tree.Partial}
	for _, entry := range tree.Entries {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		path := entry.RelativePath
		if entry.IsDir || !strings.HasPrefix(path, "/") || shouldIgnore(path, ignores) {
			continue
		}
		extension := strings.ToLower(filepath.Ext(entry.Name))
		if _, video := extensionsSet[extension]; !video {
			if _, asset := assetExtensionsSet[extension]; !asset {
				continue
			}
		}
		if len(result.Files)+len(result.Assets) >= MaxEntries {
			result.Partial = true
			break
		}
		if _, ok := extensionsSet[extension]; ok {
			result.Files = append(result.Files, File{RelativePath: path, ProviderID: entry.ID, ProviderIDStable: true, Size: entry.Size, ModifiedAt: entry.ModifiedAt.UTC()})
		} else {
			result.Assets = append(result.Assets, SourceAsset{RelativePath: path, ProviderID: entry.ID, ParentProviderID: entry.ParentID, Name: entry.Name, Extension: extension, Size: entry.Size, ModifiedAt: entry.ModifiedAt.UTC(), HashHint: entry.SHA1})
		}
	}
	return sortedResult(result), nil
}

func listProviderDirectory(ctx context.Context, driver cloudpkg.Driver, directoryID string) ([]cloudpkg.Item, error) {
	items := make([]cloudpkg.Item, 0)
	for offset := int64(0); ; {
		page, err := driver.List(ctx, directoryID, cloudpkg.PageRequest{Offset: offset, Limit: ProviderScanPageSize})
		if err != nil {
			return nil, err
		}
		if len(page.Items) == 0 && page.HasMore {
			return nil, errors.New("provider returned a non-progressing page")
		}
		items = append(items, page.Items...)
		if !page.HasMore {
			return items, nil
		}
		offset += int64(len(page.Items))
	}
}

func sortedResult(result Result) Result {
	sort.Slice(result.Files, func(i, j int) bool { return result.Files[i].RelativePath < result.Files[j].RelativePath })
	sort.Slice(result.Assets, func(i, j int) bool { return result.Assets[i].RelativePath < result.Assets[j].RelativePath })
	return result
}

func ScanLocal(ctx context.Context, storageRoot, relativeRoot string, recursive bool, extensions, assetExtensions, ignores []string) (Result, error) {
	root, err := ResolveRoot(storageRoot, relativeRoot)
	if err != nil {
		return Result{}, err
	}
	extensionsSet := map[string]struct{}{}
	for _, ext := range extensions {
		extensionsSet[strings.ToLower(ext)] = struct{}{}
	}
	assetExtensionsSet := extensionSet(assetExtensions)
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
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		_, video := extensionsSet[extension]
		_, asset := assetExtensionsSet[extension]
		if !video && !asset {
			return nil
		}
		if len(result.Files)+len(result.Assets) >= MaxEntries {
			result.Partial = true
			return filepath.SkipAll
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		identity := sha256.Sum256([]byte(providerRel + "\x00" + info.ModTime().UTC().Format(time.RFC3339Nano) + "\x00" + strconv.FormatInt(info.Size(), 10)))
		providerID := hex.EncodeToString(identity[:16])
		if video {
			result.Files = append(result.Files, File{RelativePath: providerRel, ProviderID: providerID, Size: info.Size(), ModifiedAt: info.ModTime().UTC()})
		} else {
			result.Assets = append(result.Assets, SourceAsset{RelativePath: providerRel, ProviderID: providerID, Name: entry.Name(), Extension: extension, Size: info.Size(), ModifiedAt: info.ModTime().UTC(), HashHint: providerID})
		}
		return nil
	})
	if err != nil && !errors.Is(err, filepath.SkipAll) {
		return Result{}, err
	}
	return sortedResult(result), nil
}

func extensionSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if !strings.HasPrefix(value, ".") {
			value = "." + value
		}
		result[value] = struct{}{}
	}
	return result
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
	identity := sha256.Sum256([]byte(providerRel + "\x00" + info.ModTime().UTC().Format(time.RFC3339Nano) + "\x00" + strconv.FormatInt(info.Size(), 10)))
	return File{RelativePath: providerRel, ProviderID: hex.EncodeToString(identity[:16]), Size: info.Size(), ModifiedAt: info.ModTime().UTC()}, true, nil
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
	parsed := ParseMedia(name, path)
	return parsed.MediaType, parsed.Title, parsed.Season, parsed.Episode
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
