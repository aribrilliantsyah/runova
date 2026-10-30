// Package secret encrypts small secret values with AES-256-GCM.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
)

const keySize = 32

var ErrDecrypt = errors.New("cannot decrypt secret")

type Box struct{ aead cipher.AEAD }

// GenerateKey writes a new random key to path. It fails if the file exists.
func GenerateKey(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create key file: %w", err)
	}
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		f.Close()
		os.Remove(path)
		return fmt.Errorf("generate key: %w", err)
	}
	if _, err := f.Write(key); err != nil {
		f.Close()
		os.Remove(path)
		return fmt.Errorf("write key file: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return fmt.Errorf("write key file: %w", err)
	}
	return nil
}

// LoadKey reads an existing key file. It never creates one.
func LoadKey(path string) (*Box, error) {
	key, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("baca kunci rahasia %s: %w", path, err)
	}
	if len(key) != keySize {
		return nil, fmt.Errorf("kunci rahasia %s harus 32 byte, ditemukan %d byte", path, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("kunci rahasia %s: %w", path, err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("kunci rahasia %s: %w", path, err)
	}
	return &Box{aead: aead}, nil
}

// Seal returns base64url(nonce || ciphertext) with a fresh random nonce.
func (b *Box) Seal(plain []byte) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b.aead.Seal(nonce, nonce, plain, nil)), nil
}

func (b *Box) Open(sealed string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil || len(raw) < b.aead.NonceSize()+b.aead.Overhead() {
		return nil, ErrDecrypt
	}
	n := b.aead.NonceSize()
	plain, err := b.aead.Open(nil, raw[:n], raw[n:], nil)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plain, nil
}
