package mediatool

import (
	"context"
	"errors"
	"testing"
)

func TestFFmpegRejectsUnsafeMergePathsBeforeExecution(t *testing.T) {
	tool := &FFmpeg{path: "missing", version: "test"}
	if err := tool.MergeDASH(context.Background(), "relative.mp4", "C:\\audio.m4a", "C:\\out.mp4"); ErrorCode(err) != CodeFailed {
		t.Fatalf("expected safe path failure, got %v", err)
	}
}

func TestErrorCodeDefaultsSafely(t *testing.T) {
	if ErrorCode(errors.New("raw")) != CodeFailed {
		t.Fatal("raw media tool error leaked an unstable code")
	}
}
