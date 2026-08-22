package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHostRequiresExplicitAPIVersionABI(t *testing.T) {
	host := NewHost(context.Background())
	t.Cleanup(func() { _ = host.Close(context.Background()) })

	valid := writeWASM(t, apiVersionWASM(1))
	if err := host.Validate(context.Background(), valid); err != nil {
		t.Fatalf("valid module rejected: %v", err)
	}
	wrong := writeWASM(t, apiVersionWASM(2))
	if err := host.Validate(context.Background(), wrong); ErrorCode(err) != CodeInvalidModule {
		t.Fatalf("wrong API version error=%v code=%s", err, ErrorCode(err))
	}
	empty := writeWASM(t, []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00})
	if err := host.Validate(context.Background(), empty); ErrorCode(err) != CodeInvalidModule {
		t.Fatalf("missing ABI error=%v code=%s", err, ErrorCode(err))
	}
}

func TestHostStartsAndStopsWithoutWASIOrFilesystem(t *testing.T) {
	host := NewHost(context.Background())
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	entry := writeWASM(t, apiVersionWASM(1))
	if err := host.Start(context.Background(), "org.ohmycine.fixture", entry, 1); err != nil {
		t.Fatal(err)
	}
	if err := host.Stop("org.ohmycine.fixture"); err != nil {
		t.Fatal(err)
	}
}

func writeWASM(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plugin.wasm")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func apiVersionWASM(version byte) []byte {
	name := []byte("omc_api_version")
	module := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	module = append(module, 0x01, 0x05, 0x01, 0x60, 0x00, 0x01, 0x7f)
	module = append(module, 0x03, 0x02, 0x01, 0x00)
	module = append(module, 0x07, byte(1+1+len(name)+2), 0x01, byte(len(name)))
	module = append(module, name...)
	module = append(module, 0x00, 0x00)
	module = append(module, 0x0a, 0x06, 0x01, 0x04, 0x00, 0x41, version, 0x0b)
	return module
}
