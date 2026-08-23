package mediatool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
)

var ffmpegVersionPattern = regexp.MustCompile(`(?m)^ffmpeg version ([^\s]+)`)

// FFmpeg discovers one explicitly configured, isolated, or PATH-provided
// executable and invokes only fixed argument templates.
type FFmpeg struct {
	candidates []string
	mu         sync.Mutex
	path       string
	version    string
}

func Discover(configured string) *FFmpeg {
	candidates := make([]string, 0, 3)
	if value := strings.TrimSpace(configured); value != "" {
		candidates = append(candidates, value)
	}
	name := "ffmpeg"
	if runtime.GOOS == "windows" {
		name = "ffmpeg.exe"
	}
	candidates = append(candidates,
		filepath.Join(".runtime", "windows", "tools", "ffmpeg", "bin", name),
		filepath.Join(".runtime", "tools", "ffmpeg", "bin", name),
		name,
	)
	return &FFmpeg{candidates: candidates}
}

func (f *FFmpeg) Version(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.path != "" && f.version != "" {
		return f.version, nil
	}
	for _, candidate := range f.candidates {
		resolved, err := resolveExecutable(candidate)
		if err != nil {
			continue
		}
		command := exec.CommandContext(ctx, resolved, "-hide_banner", "-version")
		command.Env = minimalEnvironment()
		var output bytes.Buffer
		command.Stdout, command.Stderr = &output, &output
		if err := command.Run(); err != nil || output.Len() > 64*1024 {
			continue
		}
		match := ffmpegVersionPattern.FindStringSubmatch(output.String())
		if len(match) != 2 || len(match[1]) > 128 {
			continue
		}
		f.path, f.version = resolved, match[1]
		return f.version, nil
	}
	return "", &Error{Code: CodeUnavailable, Cause: errors.New("no compatible ffmpeg executable found")}
}

func (f *FFmpeg) MergeDASH(ctx context.Context, video, audio, output string) error {
	if _, err := f.Version(ctx); err != nil {
		return err
	}
	for _, value := range []string{video, audio, output} {
		if !filepath.IsAbs(value) || strings.ContainsAny(value, "\x00\r\n") {
			return &Error{Code: CodeFailed, Cause: errors.New("media tool path is invalid")}
		}
	}
	if samePath(video, audio) || samePath(video, output) || samePath(audio, output) {
		return &Error{Code: CodeFailed, Cause: errors.New("media tool paths must be distinct")}
	}
	f.mu.Lock()
	executable := f.path
	f.mu.Unlock()
	// Arguments are deliberately fixed. In particular, plugins cannot inject
	// protocols, filters, codecs, overwrite flags, or arbitrary output paths.
	command := exec.CommandContext(ctx, executable,
		"-hide_banner", "-nostdin", "-loglevel", "error", "-n",
		"-i", video, "-i", audio, "-map", "0:v:0", "-map", "1:a:0",
		"-c", "copy", output,
	)
	command.Env = minimalEnvironment()
	var stderr limitedBuffer
	stderr.limit = 32 * 1024
	command.Stdout, command.Stderr = &stderr, &stderr
	if err := command.Run(); err != nil {
		// Do not attach stderr: it may include filesystem paths or upstream
		// metadata. Callers log only the stable code and FFmpeg version.
		return &Error{Code: CodeFailed, Cause: fmt.Errorf("ffmpeg exited unsuccessfully: %w", err)}
	}
	return nil
}

func resolveExecutable(candidate string) (string, error) {
	if strings.ContainsAny(candidate, "\x00\r\n") {
		return "", errors.New("invalid executable path")
	}
	resolved, err := exec.LookPath(candidate)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absolute)
	if err != nil || info.IsDir() || !info.Mode().IsRegular() {
		return "", errors.New("ffmpeg executable is not a regular file")
	}
	return filepath.Clean(absolute), nil
}

func minimalEnvironment() []string {
	result := make([]string, 0, 3)
	for _, key := range []string{"PATH", "SystemRoot", "WINDIR"} {
		if value := os.Getenv(key); value != "" {
			result = append(result, key+"="+value)
		}
	}
	return result
}

func samePath(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := b.limit - b.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		_, _ = b.Buffer.Write(value)
	}
	return original, nil
}
