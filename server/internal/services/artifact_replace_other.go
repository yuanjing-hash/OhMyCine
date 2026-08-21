//go:build !windows

package services

import "os"

func replaceArtifactFile(source, target string) error { return os.Rename(source, target) }
