package credential

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestStoreGeneratesAndReusesKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "credentials.key")
	first, err := Open(path, "")
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := first.Encrypt("downloader:test:password", "very-secret")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(envelope, "very-secret") {
		t.Fatal("ciphertext contains plaintext")
	}
	second, err := Open(path, "")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := second.Decrypt("downloader:test:password", envelope)
	if err != nil || plain != "very-secret" {
		t.Fatalf("decrypt = %q, %v", plain, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".credentials-*.tmp")); err != nil || len(matches) != 0 {
		t.Fatalf("temporary key files remain: %v, %v", matches, err)
	}
}

func TestStoreConcurrentKeyGenerationPublishesOneCompleteKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.key")
	const workers = 8
	stores := make([]*Store, workers)
	errorsFound := make([]error, workers)
	var start sync.WaitGroup
	start.Add(1)
	var done sync.WaitGroup
	for index := range stores {
		done.Add(1)
		go func(index int) {
			defer done.Done()
			start.Wait()
			stores[index], errorsFound[index] = Open(path, "")
		}(index)
	}
	start.Done()
	done.Wait()
	for _, err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}
	envelope, err := stores[0].Encrypt("concurrent", "secret")
	if err != nil {
		t.Fatal(err)
	}
	for index, store := range stores[1:] {
		plaintext, err := store.Decrypt("concurrent", envelope)
		if err != nil || plaintext != "secret" {
			t.Fatalf("store %d did not load the published key: %q, %v", index+1, plaintext, err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeKey(string(raw)); err != nil {
		t.Fatalf("published key is incomplete: %v", err)
	}
}

func TestStoreBindsPurposeAndValidatesKey(t *testing.T) {
	key := base64.RawStdEncoding.EncodeToString(make([]byte, keySize))
	store, err := Open("", key)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := store.Encrypt("one", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Decrypt("two", envelope); err == nil {
		t.Fatal("expected AAD mismatch")
	}
	if _, err := Open("", base64.RawStdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("expected invalid key length")
	}
}
