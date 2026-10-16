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

func TestExternalAcceptsProjectedSymlinkWithoutChangingReadOnlyParent(t *testing.T) {
	dir := t.TempDir()
	version := filepath.Join(dir, "..2026_10_16_00_00_00", "master.key")
	if err := os.MkdirAll(filepath.Dir(version), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(version, bytes.Repeat([]byte{7}, 32), 0400); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "master.key")
	if err := os.Symlink(filepath.Join("..2026_10_16_00_00_00", "master.key"), link); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatal(err)
	}
	s, err := NewExternal(link)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := s.Seal("credential", []byte("persisted"))
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := NewExternal(link)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := reopened.Open("credential", payload)
	if err != nil || string(plain) != "persisted" {
		t.Fatalf("external key did not persist encryption: %q, %v", plain, err)
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0555 {
		t.Fatalf("external loader changed mounted directory mode: %v %v", info, err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
}

func TestExternalRejectsMissingInvalidAndNonPrivateKeys(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewExternal(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing external key accepted")
	}
	path := filepath.Join(dir, "key")
	for _, tc := range []struct {
		name string
		data []byte
		mode os.FileMode
	}{
		{"short", []byte("short"), 0400},
		{"public", bytes.Repeat([]byte{1}, 32), 0444},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_ = os.Chmod(path, 0600)
			if err := os.WriteFile(path, tc.data, tc.mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, tc.mode); err != nil {
				t.Fatal(err)
			}
			if _, err := NewExternal(path); err == nil {
				t.Fatal("invalid external key accepted")
			}
		})
	}
}
