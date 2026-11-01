package capture

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
)

type Cipher struct{ aead cipher.AEAD }

func NewCipher(key string) (*Cipher, error) {
	b, e := base64.StdEncoding.DecodeString(key)
	if e != nil {
		return nil, e
	}
	block, e := aes.NewCipher(b)
	if e != nil {
		return nil, e
	}
	a, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	return &Cipher{a}, nil
}
func (c *Cipher) Seal(plaintext []byte, scope string) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, e := rand.Read(nonce); e != nil {
		return nil, e
	}
	return c.aead.Seal(nonce, nonce, plaintext, []byte(scope)), nil
}
func (c *Cipher) Open(b []byte, scope string) ([]byte, error) {
	n := c.aead.NonceSize()
	if len(b) < n {
		return nil, errors.New("truncated ciphertext")
	}
	return c.aead.Open(nil, b[:n], b[n:], []byte(scope))
}
