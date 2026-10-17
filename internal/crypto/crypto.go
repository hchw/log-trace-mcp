// Package crypto provides authenticated encryption for stored credentials.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

// Cipher encrypts and decrypts small credential blobs with AES-256-GCM.
type Cipher struct {
	aead cipher.AEAD
}

// New builds a Cipher from a 32-byte key.
func New(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("master key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt seals plaintext with additional authenticated data aad.
// Output layout: nonce || ciphertext.
func (c *Cipher) Encrypt(plaintext, aad []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, plaintext, aad), nil
}

// Decrypt opens a blob produced by Encrypt.
func (c *Cipher) Decrypt(blob, aad []byte) ([]byte, error) {
	ns := c.aead.NonceSize()
	if len(blob) < ns {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ct := blob[:ns], blob[ns:]
	pt, err := c.aead.Open(nil, nonce, ct, aad)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return pt, nil
}
