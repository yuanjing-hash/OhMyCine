//go:build !windows

package storage

import "golang.org/x/sys/unix"

func diskCapacity(path string) (uint64, uint64, error) {
	var stats unix.Statfs_t
	if err := unix.Statfs(path, &stats); err != nil {
		return 0, 0, err
	}
	return stats.Bavail * uint64(stats.Bsize), stats.Blocks * uint64(stats.Bsize), nil
}
