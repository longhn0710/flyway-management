// Package secrets provides authenticated local encryption behind a small adapter boundary.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Store can later be implemented by an external secret provider without exposing secrets through HTTP.
type Store interface {
	Seal(record string, plaintext []byte) ([]byte, error)
	Open(record string, payload []byte) ([]byte, error)
}
type Local struct{ aead cipher.AEAD }

// NewLocal never generates a replacement key for an installation that already uses encryption.
func NewLocal(keyPath string, encryptedInstallation bool) (*Local, error) {
	if err := privateDirectory(filepath.Dir(keyPath)); err != nil {
		return nil, err
	}
	info, err := os.Lstat(keyPath)
	if errors.Is(err, os.ErrNotExist) {
		if encryptedInstallation {
			return nil, errors.New("master key missing: restore the original master.key; encrypted installation cannot be opened")
		}
		key := make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		f, e := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return nil, fmt.Errorf("create master key: %w", e)
		}
		_, e = f.Write(key)
		if e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e == nil {
			e = closeErr
		}
		if e != nil {
			return nil, e
		}
	} else if err != nil {
		return nil, err
	} else if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("master key must be a regular private file (mode 0600)")
	}
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, errors.New("master key must contain exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	clear(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Local{aead}, nil
}
func privateDirectory(dir string) error {
	info, err := os.Lstat(dir)
	if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return errors.New("secret directory must be a real directory")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.Chmod(dir, 0700)
}
func (s *Local) Seal(record string, plaintext []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, plaintext, []byte(record)), nil
}
func (s *Local) Open(record string, payload []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(payload) < n {
		return nil, errors.New("encrypted record invalid")
	}
	plain, err := s.aead.Open(nil, payload[:n], payload[n:], []byte(record))
	if err != nil {
		return nil, errors.New("encrypted record cannot be decrypted with the supplied master key")
	}
	return plain, nil
}
