package encryption

import (
	"context"
	"encoding/base64"

	"github.com/davecgh/go-spew/spew"
	"github.com/faroshq/faros/pkg/config"
)

type multi struct {
	sealer  AEAD
	openers []AEAD
}

var _ AEAD = (*multi)(nil)

func NewMulti(ctx context.Context, config *config.Config) (AEAD, error) {
	spew.Dump(config.Controller.EncryptionKeys)
	latestKeyB64 := config.Controller.EncryptionKeys[len(config.Controller.EncryptionKeys)-1]
	latestKey, err := base64.StdEncoding.DecodeString(latestKeyB64)
	if err != nil {
		return nil, err
	}
	aead, err := NewAES256SHA512(ctx, latestKey)
	if err != nil {
		return nil, err
	}

	m := &multi{
		sealer: aead,
	}

	for _, k := range config.Controller.EncryptionKeys {
		key, err := base64.StdEncoding.DecodeString(k)
		if err != nil {
			return nil, err
		}

		aead, err := NewAES256SHA512(ctx, key)
		if err != nil {
			return nil, err
		}

		m.openers = append(m.openers, aead)
	}

	return m, nil
}

func (c *multi) Open(input string) (b string, err error) {
	for _, opener := range c.openers {
		b, err = opener.Open(input)
		if err == nil {
			return
		}
	}

	return "", err
}

func (c *multi) Seal(input string) (string, error) {
	return c.sealer.Seal(input)
}
