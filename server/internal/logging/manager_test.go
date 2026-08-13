package logging

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSanitizeJSONRedactsSecretsURLsAndPaths(t *testing.T) {
	input := []byte(`{"level":"info","message":"Bearer abc.def api_key=plain at C:\\Private\\movie.mkv","module":"test","component":"unit","password":"secret","url":"https://user:pw@example.test/video?token=abc&sig=xyz&safe=1","root_path":"D:\\Media\\movie.mkv"}` + "\n")
	out := string(SanitizeJSON(input))
	for _, forbidden := range []string{"abc.def", "plain", "secret", "token=abc", "sig=xyz", `D:\\Media`, `C:\\Private`} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("sanitized output leaked %q: %s", forbidden, out)
		}
	}
	for _, required := range []string{"***redacted***", "safe=1", "[local-path-redacted]"} {
		if !strings.Contains(out, required) {
			t.Fatalf("sanitized output missing %q: %s", required, out)
		}
	}
}

func TestManagerWritesQueriesAndCompressesRotation(t *testing.T) {
	dir := t.TempDir()
	stdout := &bytes.Buffer{}
	m, err := NewManager(dir, "production", stdout)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	policy := Policy{Level: "debug", MaxFileMiB: 1, MaxBackups: 3, RetentionDays: 30, MaxTotalMiB: 32}
	if err := m.Apply(policy); err != nil {
		t.Fatal(err)
	}
	log := m.Logger("storage", "scanner")
	payload := strings.Repeat("x", 4_000)
	for i := 0; i < 300; i++ {
		log.Info().Str("storage_id", "12").Str("payload", payload).Int("index", i).Msg("scan item")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	compressed := false
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".gz") {
			compressed = true
			file, _ := os.Open(filepath.Join(dir, entry.Name()))
			gz, e := gzip.NewReader(file)
			if e != nil {
				t.Fatal(e)
			}
			_, e = io.ReadAll(gz)
			if e != nil {
				t.Fatal(e)
			}
			_ = gz.Close()
			_ = file.Close()
		}
	}
	if !compressed {
		t.Fatal("expected compressed rotation")
	}
	result, err := m.Query(context.Background(), Filter{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour), Modules: []string{"storage"}, StorageID: "12", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.List) != 10 {
		t.Fatalf("expected limited 10 entries, got %d", len(result.List))
	}
	if !strings.Contains(stdout.String(), `"module":"storage"`) {
		t.Fatal("stdout did not receive structured event")
	}
}

func TestPluginLoggerBindsPluginIdentity(t *testing.T) {
	var out bytes.Buffer
	m, err := NewManager(t.TempDir(), "development", &out)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	logger := m.PluginLogger("trusted-plugin", "worker")
	logger.Info().Str("plugin_id", "spoofed").Msg("plugin event")
	result, err := m.Query(context.Background(), Filter{From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour), Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.List) != 1 || result.List[0].PluginID != "trusted-plugin" {
		t.Fatalf("plugin identity was not host-bound: %+v", result.List)
	}
}

func TestManagerFallsBackToStdoutWhenLogDirectoryIsAFile(t *testing.T) {
	root := t.TempDir()
	invalidDirectory := filepath.Join(root, "occupied")
	if err := os.WriteFile(invalidDirectory, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	manager, err := NewManager(invalidDirectory, "production", &stdout)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	logger := manager.Logger("server", "degraded-test")
	logger.Error().Msg("still visible")
	if !manager.Health().Degraded || !strings.Contains(stdout.String(), "still visible") {
		t.Fatalf("expected degraded stdout fallback: health=%+v output=%s", manager.Health(), stdout.String())
	}
}

func TestSanitizeJSONBoundsOversizedEvents(t *testing.T) {
	input := []byte(`{"time":"2026-08-13T00:00:00Z","level":"info","message":"large","module":"test","component":"bounds","payload":"` + strings.Repeat("x", 100_000) + `"}` + "\n")
	out := SanitizeJSON(input)
	if len(out) > maxEventBytes {
		t.Fatalf("event size=%d", len(out))
	}
	if !bytes.Contains(out, []byte(`[truncated]`)) && !bytes.Contains(out, []byte(`"event_truncated":true`)) {
		t.Fatalf("missing truncation marker: %s", out)
	}
}
