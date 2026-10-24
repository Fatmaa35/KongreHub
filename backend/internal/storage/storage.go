// Package storage keeps uploaded files on a local volume, content-addressed
// by SHA-256 (aynı dosya iki kez yazılmaz, adlar kimlik sızdırmaz).
package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

type Store struct{ dir string }

func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return &Store{dir: dir}, nil
}

// Put writes data and returns (key, sha256hex).
func (s *Store) Put(data []byte) (string, string, error) {
	sum := sha256.Sum256(data)
	h := hex.EncodeToString(sum[:])
	key := h[:2] + "/" + h + ".pdf"
	p := filepath.Join(s.dir, key)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return "", "", err
	}
	if _, err := os.Stat(p); err == nil {
		return key, h, nil
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return "", "", err
	}
	return key, h, os.Rename(tmp, p)
}

// Get reads a stored file.
func (s *Store) Get(key string) ([]byte, error) {
	if filepath.IsAbs(key) || filepath.Clean(key) != key {
		return nil, fmt.Errorf("invalid key")
	}
	return os.ReadFile(filepath.Join(s.dir, key))
}

// Ping checks the directory is writable (health).
func (s *Store) Ping() error {
	f, err := os.CreateTemp(s.dir, ".health")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}
