package encryption

import (
	"context"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"

	"github.com/codahale/etm"
)

type aes256Sha512 struct {
	aead       cipher.AEAD
	randReader io.Reader
}

var _ AEAD = (*aes256Sha512)(nil)

func NewAES256SHA512(ctx context.Context, key []byte) (AEAD, error) {
	aead, err := etm.NewAES256SHA512(key)
	if err != nil {
		return nil, err
	}

	return &aes256Sha512{
		aead:       aead,
		randReader: rand.Reader,
	}, nil
}

func (c *aes256Sha512) Open(input string) (string, error) {
	if len(input) < 32 {
		return "", fmt.Errorf("encrypted value too short")
	}

	data, err := base64.StdEncoding.DecodeString(input)
	if err != nil {
		return "", err
	}

	opened, err := c.aead.Open(nil, nil, data, nil)
	if err != nil {
		return "", err
	}
	return string(opened), nil
}

func (c *aes256Sha512) Seal(input string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())

	_, err := io.ReadFull(c.randReader, nonce)
	if err != nil {
		return "", err
	}

	sealed, err := c.aead.Seal(nil, nonce, []byte(input), nil), nil
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sealed), nil
}
