package packagefs

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/contract"
)

const (
	MaxArchiveEntries    = 256
	MaxUncompressedBytes = 128 * 1024 * 1024
	MaxEntryBytes        = 64 * 1024 * 1024
	MaxArtworkBytes      = 4 * 1024 * 1024
)

var windowsReservedNames = map[string]struct{}{
	"con": {}, "prn": {}, "aux": {}, "nul": {},
	"com1": {}, "com2": {}, "com3": {}, "com4": {}, "com5": {}, "com6": {}, "com7": {}, "com8": {}, "com9": {},
	"lpt1": {}, "lpt2": {}, "lpt3": {}, "lpt4": {}, "lpt5": {}, "lpt6": {}, "lpt7": {}, "lpt8": {}, "lpt9": {},
}

// ExtractVerified installs a verified .omcp zip into a content-addressed,
// Server-owned directory. The caller must already have checked archiveDigest
// against both Registry and Manifest.
func ExtractVerified(root, archiveDigest string, manifest contract.Manifest, archive []byte) (string, string, error) {
	if _, err := contract.DecodeSHA256(archiveDigest); err != nil {
		return "", "", err
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve plugin root: %w", err)
	}
	packagesRoot := filepath.Join(absoluteRoot, "packages")
	stagingRoot := filepath.Join(absoluteRoot, "staging")
	for _, directory := range []string{absoluteRoot, packagesRoot, stagingRoot} {
		if err := ensureOwnedDirectory(directory); err != nil {
			return "", "", err
		}
	}
	destination := filepath.Join(packagesRoot, archiveDigest)
	temporary := filepath.Join(stagingRoot, "extract-"+uuid.NewString())
	if err := os.Mkdir(temporary, 0o700); err != nil {
		return "", "", fmt.Errorf("create plugin staging directory: %w", err)
	}
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.RemoveAll(temporary)
		}
	}()
	if err := extractArchive(temporary, archive); err != nil {
		return "", "", err
	}
	if err := validateInstalledEntry(temporary, manifest.Entry); err != nil {
		return "", "", err
	}
	if err := validateInstalledArtwork(temporary, manifest.LibraryArtwork); err != nil {
		return "", "", err
	}
	extractedTreeSHA256, err := managedTreeSHA256(temporary)
	if err != nil {
		return "", "", err
	}
	if info, statErr := os.Lstat(destination); statErr == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || isReparsePoint(info) {
			return "", "", errors.New("plugin package destination is not a safe directory")
		}
		if err := ValidateManagedPackage(absoluteRoot, destination, manifest, extractedTreeSHA256); err != nil {
			return "", "", err
		}
		return destination, extractedTreeSHA256, nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", "", statErr
	}
	if err := os.Rename(temporary, destination); err != nil {
		if info, statErr := os.Lstat(destination); statErr == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 && !isReparsePoint(info) {
			if validateErr := ValidateManagedPackage(absoluteRoot, destination, manifest, extractedTreeSHA256); validateErr == nil {
				return destination, extractedTreeSHA256, nil
			}
		}
		return "", "", fmt.Errorf("publish plugin package: %w", err)
	}
	removeTemporary = false
	if err := ValidateManagedPackage(absoluteRoot, destination, manifest, extractedTreeSHA256); err != nil {
		return "", "", err
	}
	return destination, extractedTreeSHA256, nil
}

// ValidateManagedPackage revalidates the exact content-addressed package
// directory before a persisted package is executed after preview, rollback, or
// Server restart. It rejects path substitution, links, Windows reparse points,
// special files, and a manifest/directory digest mismatch.
func ValidateManagedPackage(root, packagePath string, manifest contract.Manifest, expectedTreeSHA256 string) error {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil || !filepath.IsAbs(root) {
		return errors.New("plugin root is invalid")
	}
	cleaned := filepath.Clean(packagePath)
	packagesRoot := filepath.Join(absoluteRoot, "packages")
	if filepath.Dir(cleaned) != packagesRoot {
		return errors.New("plugin package path is outside the managed packages root")
	}
	digest := filepath.Base(cleaned)
	if _, err := contract.DecodeSHA256(digest); err != nil || manifest.PackageSHA256 != digest {
		return errors.New("plugin package path does not match the verified digest")
	}
	if _, err := contract.DecodeSHA256(expectedTreeSHA256); err != nil {
		return errors.New("plugin package tree digest is invalid")
	}
	if err := ensureOwnedDirectory(absoluteRoot); err != nil {
		return err
	}
	if err := ensureOwnedDirectory(packagesRoot); err != nil {
		return err
	}
	if err := validateSafeTree(cleaned); err != nil {
		return err
	}
	if err := validateInstalledEntry(cleaned, manifest.Entry); err != nil {
		return err
	}
	if err := validateInstalledArtwork(cleaned, manifest.LibraryArtwork); err != nil {
		return err
	}
	actualTreeSHA256, err := managedTreeSHA256(cleaned)
	if err != nil {
		return err
	}
	if actualTreeSHA256 != expectedTreeSHA256 {
		return errors.New("plugin package tree digest mismatch")
	}
	return nil
}

func managedTreeSHA256(root string) (string, error) {
	if err := validateSafeTree(root); err != nil {
		return "", err
	}
	type treeEntry struct {
		path string
		dir  bool
	}
	entries := make([]treeEntry, 0, 16)
	err := filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current == root {
			return nil
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		entries = append(entries, treeEntry{path: filepath.ToSlash(relative), dir: entry.IsDir()})
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	hash := sha256.New()
	var length [8]byte
	for _, entry := range entries {
		kind := byte('f')
		if entry.dir {
			kind = 'd'
		}
		_, _ = hash.Write([]byte{kind})
		binary.BigEndian.PutUint64(length[:], uint64(len(entry.path)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(entry.path))
		if entry.dir {
			continue
		}
		file, err := os.Open(filepath.Join(root, filepath.FromSlash(entry.path)))
		if err != nil {
			return "", err
		}
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() || isReparsePoint(info) {
			_ = file.Close()
			return "", errors.New("managed plugin tree changed during validation")
		}
		binary.BigEndian.PutUint64(length[:], uint64(info.Size()))
		_, _ = hash.Write(length[:])
		if _, err := io.Copy(hash, file); err != nil {
			_ = file.Close()
			return "", err
		}
		if err := file.Close(); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// ComputeManagedTreeSHA256 is used by schema upgrades to enroll packages that
// were installed before the extracted-tree digest became persistent. It uses
// the same full safety and byte-level digest as runtime validation.
func ComputeManagedTreeSHA256(root string) (string, error) {
	return managedTreeSHA256(root)
}

func extractArchive(destination string, archive []byte) error {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return fmt.Errorf("open plugin package: %w", err)
	}
	if len(reader.File) == 0 || len(reader.File) > MaxArchiveEntries {
		return errors.New("plugin package entry count is invalid")
	}
	seen := make(map[string]struct{}, len(reader.File))
	var total uint64
	for _, entry := range reader.File {
		cleaned, err := safeArchivePath(entry.Name)
		if err != nil {
			return err
		}
		key := strings.ToLower(cleaned)
		if _, exists := seen[key]; exists {
			return errors.New("plugin package contains duplicate paths")
		}
		seen[key] = struct{}{}
		mode := entry.Mode()
		if mode&os.ModeSymlink != 0 || (!entry.FileInfo().IsDir() && mode&os.ModeType != 0) {
			return errors.New("plugin package contains a link or special file")
		}
		if entry.UncompressedSize64 > MaxEntryBytes || total > MaxUncompressedBytes-entry.UncompressedSize64 {
			return errors.New("plugin package uncompressed content is too large")
		}
		total += entry.UncompressedSize64
		target := filepath.Join(destination, filepath.FromSlash(cleaned))
		if !withinRoot(destination, target) {
			return errors.New("plugin package path escapes staging root")
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := extractFile(entry, target); err != nil {
			return err
		}
	}
	return nil
}

func extractFile(entry *zip.File, target string) error {
	source, err := entry.Open()
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	destination, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(destination, io.LimitReader(source, MaxEntryBytes+1))
	closeErr := destination.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written != int64(entry.UncompressedSize64) || written > MaxEntryBytes {
		return errors.New("plugin package entry size mismatch")
	}
	return nil
}

func safeArchivePath(value string) (string, error) {
	if value == "" || len(value) > 512 || strings.Contains(value, "\\") || strings.ContainsRune(value, '\x00') || strings.HasPrefix(value, "/") {
		return "", errors.New("plugin package contains an unsafe path")
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned != strings.TrimSuffix(value, "/") || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", errors.New("plugin package contains an unsafe path")
	}
	for _, segment := range strings.Split(cleaned, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.HasSuffix(segment, " ") || strings.HasSuffix(segment, ".") || strings.ContainsAny(segment, `<>:"|?*`) {
			return "", errors.New("plugin package contains a platform-unsafe path")
		}
		base := strings.ToLower(strings.SplitN(segment, ".", 2)[0])
		if _, reserved := windowsReservedNames[base]; reserved {
			return "", errors.New("plugin package contains a reserved Windows path")
		}
	}
	return cleaned, nil
}

func validateInstalledEntry(root, entry string) error {
	cleaned, err := safeArchivePath(entry)
	if err != nil {
		return errors.New("plugin manifest entry is unsafe")
	}
	target := filepath.Join(root, filepath.FromSlash(cleaned))
	if !withinRoot(root, target) {
		return errors.New("plugin entry escapes package root")
	}
	info, err := os.Lstat(target)
	if err != nil {
		return errors.New("plugin entry is missing")
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || isReparsePoint(info) || info.Size() == 0 || info.Size() > MaxEntryBytes {
		return errors.New("plugin entry is not a safe regular file")
	}
	return nil
}

func validateInstalledArtwork(root, artwork string) error {
	if artwork == "" {
		return nil
	}
	cleaned, err := safeArchivePath(artwork)
	if err != nil {
		return errors.New("plugin manifest libraryArtwork is unsafe")
	}
	target := filepath.Join(root, filepath.FromSlash(cleaned))
	if !withinRoot(root, target) {
		return errors.New("plugin library artwork escapes package root")
	}
	info, err := os.Lstat(target)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || isReparsePoint(info) || info.Size() == 0 || info.Size() > MaxArtworkBytes {
		return errors.New("plugin library artwork is not a safe regular file")
	}
	file, err := os.Open(target)
	if err != nil {
		return errors.New("plugin library artwork cannot be opened")
	}
	defer func() { _ = file.Close() }()
	header := make([]byte, 12)
	read, err := io.ReadFull(file, header)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return errors.New("plugin library artwork cannot be read")
	}
	header = header[:read]
	switch strings.ToLower(filepath.Ext(target)) {
	case ".png":
		if len(header) < 8 || !bytes.Equal(header[:8], []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}) {
			return errors.New("plugin library artwork content does not match PNG")
		}
	case ".jpg", ".jpeg":
		if len(header) < 3 || header[0] != 0xff || header[1] != 0xd8 || header[2] != 0xff {
			return errors.New("plugin library artwork content does not match JPEG")
		}
	case ".webp":
		if len(header) < 12 || string(header[:4]) != "RIFF" || string(header[8:12]) != "WEBP" {
			return errors.New("plugin library artwork content does not match WebP")
		}
	default:
		return errors.New("plugin library artwork type is unsupported")
	}
	return nil
}

func ensureOwnedDirectory(directory string) error {
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(directory, 0o700)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || isReparsePoint(info) {
		return errors.New("plugin root contains an unsafe directory")
	}
	return nil
}

func withinRoot(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

// QuarantinePackages atomically moves exact content-addressed package
// directories out of the executable packages root. The returned directory can
// be restored if the database transaction fails, or removed after commit.
func QuarantinePackages(root string, packagePaths []string) (string, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil || !filepath.IsAbs(root) {
		return "", errors.New("plugin root is invalid")
	}
	packagesRoot := filepath.Join(absoluteRoot, "packages")
	stagingRoot := filepath.Join(absoluteRoot, "staging")
	if err := ensureOwnedDirectory(packagesRoot); err != nil {
		return "", err
	}
	if err := ensureOwnedDirectory(stagingRoot); err != nil {
		return "", err
	}
	quarantine := filepath.Join(stagingRoot, "uninstall-"+uuid.NewString())
	if err := os.Mkdir(quarantine, 0o700); err != nil {
		return "", err
	}
	moved := make([]string, 0, len(packagePaths))
	for _, packagePath := range packagePaths {
		cleaned := filepath.Clean(packagePath)
		if filepath.Dir(cleaned) != packagesRoot {
			_ = restoreQuarantine(packagesRoot, quarantine, moved)
			return "", errors.New("plugin package path is outside the managed packages root")
		}
		if _, err := contract.DecodeSHA256(filepath.Base(cleaned)); err != nil {
			_ = restoreQuarantine(packagesRoot, quarantine, moved)
			return "", errors.New("plugin package path is not content addressed")
		}
		if err := validateSafeTree(cleaned); err != nil {
			_ = restoreQuarantine(packagesRoot, quarantine, moved)
			return "", err
		}
		target := filepath.Join(quarantine, filepath.Base(cleaned))
		if err := os.Rename(cleaned, target); err != nil {
			_ = restoreQuarantine(packagesRoot, quarantine, moved)
			return "", err
		}
		moved = append(moved, filepath.Base(cleaned))
	}
	return quarantine, nil
}

func RestoreQuarantine(root, quarantine string) error {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil || !filepath.IsAbs(root) {
		return errors.New("plugin root is invalid")
	}
	stagingRoot := filepath.Join(absoluteRoot, "staging")
	cleaned := filepath.Clean(quarantine)
	if filepath.Dir(cleaned) != stagingRoot || !strings.HasPrefix(filepath.Base(cleaned), "uninstall-") {
		return errors.New("plugin quarantine path is invalid")
	}
	entries, err := os.ReadDir(cleaned)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return restoreQuarantine(filepath.Join(absoluteRoot, "packages"), cleaned, names)
}

func RemoveQuarantine(root, quarantine string) error {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil || !filepath.IsAbs(root) {
		return errors.New("plugin root is invalid")
	}
	cleaned := filepath.Clean(quarantine)
	if filepath.Dir(cleaned) != filepath.Join(absoluteRoot, "staging") || !strings.HasPrefix(filepath.Base(cleaned), "uninstall-") {
		return errors.New("plugin quarantine path is invalid")
	}
	if err := validateSafeTree(cleaned); err != nil {
		return err
	}
	return os.RemoveAll(cleaned)
}

func restoreQuarantine(packagesRoot, quarantine string, names []string) error {
	var firstErr error
	for index := len(names) - 1; index >= 0; index-- {
		name := names[index]
		if _, err := contract.DecodeSHA256(name); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := os.Rename(filepath.Join(quarantine, name), filepath.Join(packagesRoot, name)); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr == nil {
		_ = os.Remove(quarantine)
	}
	return firstErr
}

func validateSafeTree(root string) error {
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || isReparsePoint(info) {
		return errors.New("managed plugin path is not a safe directory")
	}
	return filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current == root {
			return nil
		}
		entryInfo, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 || isReparsePoint(entryInfo) || (!entryInfo.IsDir() && !entryInfo.Mode().IsRegular()) {
			return errors.New("managed plugin path contains a link or special file")
		}
		return nil
	})
}

// RemoveManagedPackage deletes one exact content-addressed package after the
// same root and tree checks used by uninstall quarantine.
func RemoveManagedPackage(root, packagePath string) error {
	quarantine, err := QuarantinePackages(root, []string{packagePath})
	if err != nil {
		return err
	}
	return RemoveQuarantine(root, quarantine)
}

// ReconcileStaging recovers interrupted package operations. Extraction
// directories are always incomplete and removed. An uninstall quarantine is
// restored when every digest is still referenced by the database, and removed
// when none are referenced. A mixed state fails closed.
func ReconcileStaging(root string, referenced map[string]struct{}) error {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil || !filepath.IsAbs(root) {
		return errors.New("plugin root is invalid")
	}
	stagingRoot := filepath.Join(absoluteRoot, "staging")
	if err := ensureOwnedDirectory(stagingRoot); err != nil {
		return err
	}
	entries, err := os.ReadDir(stagingRoot)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), "extract-") && !strings.HasPrefix(entry.Name(), "uninstall-") {
			continue
		}
		target := filepath.Join(stagingRoot, entry.Name())
		if err := validateSafeTree(target); err != nil {
			return err
		}
		if strings.HasPrefix(entry.Name(), "extract-") {
			if err := os.RemoveAll(target); err != nil {
				return err
			}
			continue
		}
		children, err := os.ReadDir(target)
		if err != nil {
			return err
		}
		referencedCount := 0
		for _, child := range children {
			if _, err := contract.DecodeSHA256(child.Name()); err != nil {
				return errors.New("plugin quarantine contains an invalid package name")
			}
			if _, ok := referenced[child.Name()]; ok {
				referencedCount++
			}
		}
		switch {
		case referencedCount == 0:
			if err := RemoveQuarantine(root, target); err != nil {
				return err
			}
		case referencedCount == len(children):
			if err := RestoreQuarantine(root, target); err != nil {
				return err
			}
		default:
			return errors.New("plugin quarantine has mixed database references")
		}
	}
	return nil
}
