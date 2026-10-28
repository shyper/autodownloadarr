// Package secrets encrypts credentials (Usenet passwords, API keys, tokens)
// before they are stored in the database. The key lives in a file next to
// the database, so a leaked database alone does not reveal them.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	keySize = 32
	prefix  = "v1:"
)

// ErrCorrupt is returned for values that cannot be decrypted.
var ErrCorrupt = errors.New("secrets: cannot decrypt value")

// Box encrypts and decrypts values with AES-256-GCM.
type Box struct {
	aead cipher.AEAD
}

// LoadOrCreateKey reads the key file at path, creating it with a random key
// (mode 0600) if it does not exist yet.
func LoadOrCreateKey(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(data)))
		if err != nil || len(key) != keySize {
			return nil, fmt.Errorf("secrets: invalid key file %s", path)
		}
		return key, nil
	case !errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("secrets: read key: %w", err)
	}

	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("secrets: create key: %w", err)
	}
	_, err = f.WriteString(base64.StdEncoding.EncodeToString(key) + "\n")
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, fmt.Errorf("secrets: write key: %w", err)
	}
	return key, nil
}

// New returns a Box for a 32-byte key.
func New(key []byte) (*Box, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("secrets: key must be %d bytes", keySize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Encrypt returns an encoded ciphertext. The empty string stays empty so
// that "not set" needs no special handling.
func (b *Box) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := b.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return prefix + base64.RawStdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt.
func (b *Box) Decrypt(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	raw, ok := strings.CutPrefix(value, prefix)
	if !ok {
		return "", ErrCorrupt
	}
	sealed, err := base64.RawStdEncoding.DecodeString(raw)
	if err != nil || len(sealed) < b.aead.NonceSize() {
		return "", ErrCorrupt
	}
	n := b.aead.NonceSize()
	plain, err := b.aead.Open(nil, sealed[:n], sealed[n:], nil)
	if err != nil {
		return "", ErrCorrupt
	}
	return string(plain), nil
}
