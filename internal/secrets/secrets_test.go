package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestKeyFileAndRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.key")
	key, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("key file mode %v, want 0600", info.Mode().Perm())
	}
	again, err := LoadOrCreateKey(path)
	if err != nil || string(again) != string(key) {
		t.Fatalf("reloaded key differs: %v", err)
	}

	box, err := New(key)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := box.Encrypt("usenet-password")
	if err != nil {
		t.Fatal(err)
	}
	if enc == "usenet-password" || enc[:3] != "v1:" {
		t.Errorf("not encrypted: %q", enc)
	}
	other, _ := box.Encrypt("usenet-password")
	if other == enc {
		t.Error("nonce reused")
	}
	if got, err := box.Decrypt(enc); err != nil || got != "usenet-password" {
		t.Errorf("Decrypt = %q, %v", got, err)
	}
	if got, _ := box.Encrypt(""); got != "" {
		t.Error("empty value encrypted")
	}

	otherBox, _ := New(make([]byte, 32))
	if _, err := otherBox.Decrypt(enc); !errors.Is(err, ErrCorrupt) {
		t.Errorf("wrong key: %v", err)
	}
	if _, err := box.Decrypt("plaintext"); !errors.Is(err, ErrCorrupt) {
		t.Errorf("garbage: %v", err)
	}
}

func TestInvalidKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.key")
	_ = os.WriteFile(path, []byte("too short"), 0o600)
	if _, err := LoadOrCreateKey(path); err == nil {
		t.Error("invalid key accepted")
	}
}
