package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestParseManifestAcceptsSharedFixture(t *testing.T) {
	manifest, err := ParseManifest(readSharedFixture(t))
	if err != nil {
		t.Fatalf("ParseManifest() error = %v", err)
	}
	if manifest.ID != "org.ohmycine.fixture.static-site" {
		t.Fatalf("manifest ID = %q", manifest.ID)
	}
}

func TestParseManifestRejectsUnsafeOrUnknownInput(t *testing.T) {
	fixture := readSharedFixture(t)
	tests := map[string]func(map[string]any){
		"unknown field":           func(value map[string]any) { value["serverInternals"] = true },
		"unknown capability":      func(value map[string]any) { value["capabilities"] = []any{"pt.site"} },
		"entry traversal":         func(value map[string]any) { value["entry"] = "../plugin.wasm" },
		"entry windows traversal": func(value map[string]any) { value["entry"] = `..\plugin.wasm` },
		"artwork traversal":       func(value map[string]any) { value["libraryArtwork"] = "../cover.png" },
		"artwork active content":  func(value map[string]any) { value["libraryArtwork"] = "assets/cover.svg" },
		"http source":             func(value map[string]any) { value["source"] = "http://example.test/plugin" },
		"duplicate permission": func(value map[string]any) {
			value["permissions"] = []any{map[string]any{"kind": "download.plan"}, map[string]any{"kind": "download.plan"}}
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal(fixture, &value); err != nil {
				t.Fatal(err)
			}
			mutate(value)
			data, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ParseManifest(data); err == nil {
				t.Fatal("ParseManifest() accepted invalid manifest")
			}
		})
	}
}

func TestParseManifestRejectsOversizedInput(t *testing.T) {
	data := []byte(`{"schemaVersion":1,"padding":"` + strings.Repeat("x", MaxManifestBytes) + `"}`)
	if _, err := ParseManifest(data); err == nil {
		t.Fatal("ParseManifest() accepted oversized input")
	}
}

func readSharedFixture(t *testing.T) []byte {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(current), "..", "..", "..", ".."))
	data, err := os.ReadFile(filepath.Join(root, "plugin-sdk", "fixtures", "static-site", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}
