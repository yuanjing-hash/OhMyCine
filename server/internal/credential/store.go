package credential

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const keySize = 32

// Store encrypts Server-side credentials with an application-owned AES-GCM key.
// Ciphertext envelopes are opaque and may be persisted, but must never be logged.
type Store struct {
	aead cipher.AEAD
}

// Open loads an explicitly provided Base64 key or an owner-only key file. If
// the file does not exist, a new random key is generated atomically.
func Open(keyFile, encodedKey string) (*Store, error) {
	key, err := loadKey(keyFile, encodedKey)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create credential cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create credential gcm: %w", err)
	}
	return &Store{aead: aead}, nil
}

func loadKey(keyFile, encodedKey string) ([]byte, error) {
	if strings.TrimSpace(encodedKey) != "" {
		return decodeKey(encodedKey)
	}
	keyFile = strings.TrimSpace(keyFile)
	if keyFile == "" {
		return nil, errors.New("credential key file is required")
	}
	if raw, err := os.ReadFile(keyFile); err == nil {
		return decodeKey(string(raw))
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read credential key file: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(keyFile), 0o700); err != nil {
		return nil, fmt.Errorf("create credential key directory: %w", err)
	}
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate credential key: %w", err)
	}
	encoded := base64.RawStdEncoding.EncodeToString(key)
	temporary, err := os.CreateTemp(filepath.Dir(keyFile), ".credentials-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("create credential key temporary file: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return nil, fmt.Errorf("restrict credential key temporary file: %w", err)
	}
	if _, err := temporary.WriteString(encoded + "\n"); err != nil {
		_ = temporary.Close()
		return nil, fmt.Errorf("write credential key temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return nil, fmt.Errorf("sync credential key temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return nil, fmt.Errorf("close credential key temporary file: %w", err)
	}
	// A same-directory hard link publishes the fully written key without ever
	// replacing a key created by another Server process. This avoids leaving a
	// truncated destination behind if the process exits while generating it.
	if err := os.Link(temporaryName, keyFile); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("publish credential key file: %w", err)
		}
		raw, readErr := os.ReadFile(keyFile)
		if readErr != nil {
			return nil, fmt.Errorf("read concurrently created credential key: %w", readErr)
		}
		return decodeKey(string(raw))
	}
	return key, nil
}

func decodeKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	key, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil {
		key, err = base64.StdEncoding.DecodeString(value)
	}
	if err != nil || len(key) != keySize {
		return nil, errors.New("credential master key must be Base64-encoded 32 bytes")
	}
	return key, nil
}

func (s *Store) Encrypt(purpose, plaintext string) (string, error) {
	if s == nil || s.aead == nil {
		return "", errors.New("credential store is unavailable")
	}
	if plaintext == "" {
		return "", nil
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate credential nonce: %w", err)
	}
	sealed := s.aead.Seal(nonce, nonce, []byte(plaintext), []byte(purpose))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (s *Store) Decrypt(purpose, envelope string) (string, error) {
	if envelope == "" {
		return "", nil
	}
	if s == nil || s.aead == nil {
		return "", errors.New("credential store is unavailable")
	}
	sealed, err := base64.RawURLEncoding.DecodeString(envelope)
	if err != nil || len(sealed) < s.aead.NonceSize() {
		return "", errors.New("credential envelope is invalid")
	}
	plaintext, err := s.aead.Open(nil, sealed[:s.aead.NonceSize()], sealed[s.aead.NonceSize():], []byte(purpose))
	if err != nil {
		return "", errors.New("credential envelope authentication failed")
	}
	return string(plaintext), nil
}
