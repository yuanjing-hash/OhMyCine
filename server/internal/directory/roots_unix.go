//go:build !windows

package directory

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func nativeRoots(ctx context.Context) ([]Root, error) {
	paths := map[string]string{"/": "filesystem"}
	if file, err := os.Open("/proc/self/mountinfo"); err == nil {
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) > 4 {
				mount := decodeMountPath(fields[4])
				if filepath.IsAbs(mount) {
					paths[filepath.Clean(mount)] = "mount"
				}
			}
		}
		_ = file.Close()
	}
	keys := make([]string, 0, len(paths))
	for path := range paths {
		keys = append(keys, path)
	}
	sort.Strings(keys)
	items := make([]Root, 0, len(keys))
	for _, path := range keys {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || isUnsafeLink(path, info) {
			continue
		}
		items = append(items, Root{Path: path, Name: path, Kind: paths[path], Selectable: true, Enterable: true})
	}
	return items, nil
}
