package secrets

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalAuthenticatedEncryptionAndPrivateKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets", "master.key")
	s, e := NewLocal(path, false)
	if e != nil {
		t.Fatal(e)
	}
	first, e := s.Seal("first", []byte("test-secret"))
	if e != nil {
		t.Fatal(e)
	}
	second, e := s.Seal("first", []byte("test-secret"))
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Equal(first, second) || bytes.Contains(first, []byte("test-secret")) {
		t.Fatal("randomized encryption missing")
	}
	for _, change := range []string{"payload", "nonce", "record"} {
		t.Run(change, func(t *testing.T) {
			mutated := append([]byte{}, first...)
			record := "first"
			switch change {
			case "payload":
				mutated[len(mutated)-1] ^= 1
			case "nonce":
				mutated[0] ^= 1
			case "record":
				record = "second"
			}
			if _, e = s.Open(record, mutated); e == nil {
				t.Fatal("authentication failed open")
			}
		})
	}
	reopened, e := NewLocal(path, true)
	if e != nil {
		t.Fatal(e)
	}
	plain, e := reopened.Open("first", first)
	if e != nil || string(plain) != "test-secret" {
		t.Fatal("restart decryption failed")
	}
	if e = os.Chmod(path, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = NewLocal(path, true); e == nil {
		t.Fatal("public key file accepted")
	}
}
func TestLocalRejectsSymlinksAndMissingExistingKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "master.key")
	if _, e := NewLocal(path, true); e == nil {
		t.Fatal("existing key replaced")
	}
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("missing key created")
	}
	other := filepath.Join(dir, "other")
	if e := os.WriteFile(other, make([]byte, 32), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(other, path); e != nil {
		t.Fatal(e)
	}
	if _, e := NewLocal(path, false); e == nil {
		t.Fatal("symlink key accepted")
	}
}
