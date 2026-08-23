package packagefs

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yuanjing-hash/ohmycine/server/internal/plugins/contract"
)

func TestExtractVerifiedRejectsTraversalSymlinkAndDuplicatePaths(t *testing.T) {
	manifest := contract.Manifest{Entry: "plugin.wasm"}
	digest := strings.Repeat("a", 64)
	for name, files := range map[string][]zipFixtureEntry{
		"traversal": {{name: "../plugin.wasm", body: []byte("wasm")}},
		"symlink":   {{name: "plugin.wasm", body: []byte("target"), mode: os.ModeSymlink | 0o777}},
		"duplicate": {{name: "plugin.wasm", body: []byte("one")}, {name: "PLUGIN.WASM", body: []byte("two")}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := ExtractVerified(filepath.Join(t.TempDir(), "plugins"), digest, manifest, zipFixture(t, files)); err == nil {
				t.Fatal("expected unsafe package rejection")
			}
		})
	}
}

func TestExtractVerifiedAndQuarantineStayInsideManagedRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "plugins")
	digest := strings.Repeat("b", 64)
	manifest := contract.Manifest{Entry: "bin/plugin.wasm", PackageSHA256: digest}
	installed, _, err := ExtractVerified(root, digest, manifest, zipFixture(t, []zipFixtureEntry{{name: "bin/plugin.wasm", body: []byte("wasm")}}))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(installed) != digest {
		t.Fatalf("installed=%s", installed)
	}
	quarantine, err := QuarantinePackages(root, []string{installed})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(installed); !os.IsNotExist(err) {
		t.Fatalf("package still executable: %v", err)
	}
	if err := RestoreQuarantine(root, quarantine); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(installed, "bin", "plugin.wasm")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(root), digest)
	if _, err := QuarantinePackages(root, []string{outside}); err == nil {
		t.Fatal("outside package path accepted")
	}
}

func TestValidateManagedPackageRejectsDigestAndUnexpectedLink(t *testing.T) {
	root := filepath.Join(t.TempDir(), "plugins")
	digest := strings.Repeat("d", 64)
	manifest := contract.Manifest{Entry: "plugin.wasm", PackageSHA256: digest}
	installed, treeSHA256, err := ExtractVerified(root, digest, manifest, zipFixture(t, []zipFixtureEntry{{name: "plugin.wasm", body: []byte("wasm")}}))
	if err != nil {
		t.Fatal(err)
	}
	wrongManifest := manifest
	wrongManifest.PackageSHA256 = strings.Repeat("e", 64)
	if err := ValidateManagedPackage(root, installed, wrongManifest, treeSHA256); err == nil {
		t.Fatal("managed package accepted a mismatched manifest digest")
	}
	if err := os.Symlink(filepath.Join(installed, "plugin.wasm"), filepath.Join(installed, "unexpected-link")); err == nil {
		if err := ValidateManagedPackage(root, installed, manifest, treeSHA256); err == nil {
			t.Fatal("managed package accepted an unexpected link")
		}
	}
}

func TestValidateManagedPackageRejectsModifiedWASM(t *testing.T) {
	root := filepath.Join(t.TempDir(), "plugins")
	digest := strings.Repeat("f", 64)
	manifest := contract.Manifest{Entry: "plugin.wasm", PackageSHA256: digest}
	installed, treeSHA256, err := ExtractVerified(root, digest, manifest, zipFixture(t, []zipFixtureEntry{{name: "plugin.wasm", body: []byte("trusted-wasm")}}))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installed, "plugin.wasm"), []byte("tampered-wasm"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateManagedPackage(root, installed, manifest, treeSHA256); err == nil {
		t.Fatal("managed package accepted modified WASM content")
	}
	if _, _, err := ExtractVerified(root, digest, manifest, zipFixture(t, []zipFixtureEntry{{name: "plugin.wasm", body: []byte("trusted-wasm")}})); err == nil {
		t.Fatal("extract reused a tampered content-addressed package")
	}
}

func TestExtractVerifiedRequiresDeclaredRasterArtwork(t *testing.T) {
	root := filepath.Join(t.TempDir(), "plugins")
	digest := strings.Repeat("9", 64)
	manifest := contract.Manifest{Entry: "plugin.wasm", LibraryArtwork: "assets/library.png", PackageSHA256: digest}
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0}
	installed, treeSHA256, err := ExtractVerified(root, digest, manifest, zipFixture(t, []zipFixtureEntry{
		{name: "plugin.wasm", body: []byte("wasm")},
		{name: "assets/library.png", body: png},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateManagedPackage(root, installed, manifest, treeSHA256); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(installed, "assets", "library.png"), []byte("<svg>active</svg>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateManagedPackage(root, installed, manifest, treeSHA256); err == nil {
		t.Fatal("managed package accepted modified active artwork")
	}

	missingDigest := strings.Repeat("8", 64)
	missing := manifest
	missing.PackageSHA256 = missingDigest
	if _, _, err := ExtractVerified(root, missingDigest, missing, zipFixture(t, []zipFixtureEntry{{name: "plugin.wasm", body: []byte("wasm")}})); err == nil {
		t.Fatal("package accepted a missing declared artwork")
	}
}

func TestReconcileStagingRestoresReferencedAndDeletesCommittedUninstall(t *testing.T) {
	root := filepath.Join(t.TempDir(), "plugins")
	digest := strings.Repeat("c", 64)
	manifest := contract.Manifest{Entry: "plugin.wasm", PackageSHA256: digest}
	installed, _, err := ExtractVerified(root, digest, manifest, zipFixture(t, []zipFixtureEntry{{name: "plugin.wasm", body: []byte("wasm")}}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := QuarantinePackages(root, []string{installed}); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileStaging(root, map[string]struct{}{digest: {}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(installed); err != nil {
		t.Fatalf("referenced package was not restored: %v", err)
	}
	if _, err := QuarantinePackages(root, []string{installed}); err != nil {
		t.Fatal(err)
	}
	if err := ReconcileStaging(root, map[string]struct{}{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(installed); !os.IsNotExist(err) {
		t.Fatalf("committed uninstall package remains: %v", err)
	}
}

type zipFixtureEntry struct {
	name string
	body []byte
	mode os.FileMode
}

func zipFixture(t *testing.T, entries []zipFixtureEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		if entry.mode != 0 {
			header.SetMode(entry.mode)
		}
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}
