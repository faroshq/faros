package encryption

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

func TestNewAES256SHA512(t *testing.T) {
	for _, tt := range []struct {
		name    string
		key     []byte
		wantErr string
	}{
		{
			name: "valid",
			key:  make([]byte, 64),
		},
		{
			name:    "key too short",
			key:     make([]byte, 63),
			wantErr: "etm: key must be 64 bytes long",
		},
		{
			name:    "key too long",
			key:     make([]byte, 65),
			wantErr: "etm: key must be 64 bytes long",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewAES256SHA512(context.Background(), tt.key)
			if err != nil && err.Error() != tt.wantErr ||
				err == nil && tt.wantErr != "" {
				t.Fatal(err)
			}
		})
	}
}

func TestAES256SHA512Open(t *testing.T) {
	for _, tt := range []struct {
		name       string
		key        []byte
		input      string
		wantOpened string
		wantErr    string
	}{
		{
			name:       "valid",
			key:        []byte("\x6a\x98\x95\x6b\x2b\xb2\x7e\xfd\x1b\x68\xdf\x5c\x40\xc3\x4f\x8b\xcf\xff\xe8\x17\xc2\x2d\xf6\x40\x2e\x5a\xb0\x15\x63\x4a\x2d\x2e\xab\x79\x86\x50\xfb\xce\xdc\x9d\xdd\x1c\x01\x32\xd6\x03\x99\xe6\x59\x81\x37\xb3\xdb\x67\x6f\x12\x34\x1d\xb9\x58\x18\x31\x30\x57"),
			input:      "U14NXDQ5Nfog7dfyvk7T4YUghLX3B1eeTp0d7/1THZllH0XGdEnR+hs6sn7E2i/gXr9sqg8WUG5wn3ATXFoBnA==",
			wantOpened: "foo",
		},
		{
			name:    "invalid - encrypted value tampered with",
			key:     []byte("\x98\x98\x95\x6b\x2b\xb2\x7e\xfd\x1b\x68\xdf\x5c\x40\xc3\x4f\x8b\xcf\xff\xe8\x17\xc2\x2d\xf6\x40\x2e\x5a\xb0\x15\x63\x4a\x2d\x2e\xab\x79\x86\x50\xfb\xce\xdc\x9d\xdd\x1c\x01\x32\xd6\x03\x99\xe6\x59\x81\x37\xb3\xdb\x67\x6f\x12\x34\x1d\xb9\x58\x18\x31\x30\x57"),
			input:   "U14NXDQ5Nfog7dfyvk7T4YUghLX3B1eeTp0d7/1THZllH0XGdEnR+hs6sn7E2i/gX89sqg8WUG5wn3ATXFoBnA==",
			wantErr: "message authentication failed",
		},
		{
			name:    "invalid - encryption doesn't match input",
			key:     []byte("\x56\xd9\xb1\xa1\xc0\x4f\x7e\xf8\xbe\xd0\xd9\xb6\x16\xf1\x90\x84\x6b\x8e\x93\x98\x5e\xd2\x48\xf0\xc5\x60\xa0\x13\x3a\x0a\x7d\x7f\xb2\x20\xbd\x4b\x1c\x49\xab\xc5\xa7\x71\x6d\x17\xd9\xa8\x4a\x20\xec\xab\x05\x0d\x3c\xfc\x57\x0b\x5a\x4e\x63\x43\x27\x0c\xad\x6d"),
			input:   "z12GfUsA7hTsMTavPI6ciPelqpXA0xw7YGE7ASyQLOAPf2yby/7TuTdYazOCUfWaZwY/00bNoQJzg8TAZ8zFHA==",
			wantErr: "message authentication failed",
		},
		{
			name:    "invalid - too short",
			key:     []byte("\x6a\x98\x95\x6b\x2b\xb2\x7e\xfd\x1b\x68\xdf\x5c\x40\xc3\x4f\x8b\xcf\xff\xe8\x17\xc2\x2d\xf6\x40\x2e\x5a\xb0\x15\x63\x4a\x2d\x2e\xab\x79\x86\x50\xfb\xce\xdc\x9d\xdd\x1c\x01\x32\xd6\x03\x99\xe6\x59\x81\x37\xb3\xdb\x67\x6f\x12\x34\x1d\xb9\x58\x18\x31\x30\x57"),
			input:   "123",
			wantErr: "encrypted value too short",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cipher, err := NewAES256SHA512(context.Background(), tt.key)
			if err != nil {
				t.Fatal(err)
			}

			opened, err := cipher.Open(tt.input)
			if err != nil && err.Error() != tt.wantErr ||
				err == nil && tt.wantErr != "" {
				t.Fatal(err)
			}

			if !strings.EqualFold(tt.wantOpened, opened) {
				t.Error(string(opened))
			}
		})
	}
}

func TestAES256SHA512Seal(t *testing.T) {
	for _, tt := range []struct {
		name       string
		key        []byte
		randReader io.Reader
		input      string
		wantSealed string
		wantErr    string
	}{
		{
			name:       "valid",
			key:        []byte("\x6a\x98\x95\x6b\x2b\xb2\x7e\xfd\x1b\x68\xdf\x5c\x40\xc3\x4f\x8b\xcf\xff\xe8\x17\xc2\x2d\xf6\x40\x2e\x5a\xb0\x15\x63\x4a\x2d\x2e\xab\x79\x86\x50\xfb\xce\xdc\x9d\xdd\x1c\x01\x32\xd6\x03\x99\xe6\x59\x81\x37\xb3\xdb\x67\x6f\x12\x34\x1d\xb9\x58\x18\x31\x30\x57"),
			randReader: bytes.NewBufferString("\xd9\x1c\x3c\x05\xb2\xf3\xc5\x93\x20\x9f\x9b\x67\x43\x8c\x0c\x3d\x9c\x33\x5b\x16\xd6\x9a\x9c\xf2"),
			input:      "test",
			wantSealed: "2Rw8BbLzxZMgn5tnQ4wMPeCAJlkqILLlXjDW0SQeNDa++3mORrWVzuB5nERcqoMmktt2NDPgDg5Usgsv3mNT9g==",
		},
		{
			name:       "rand.Read EOF",
			key:        make([]byte, 64),
			randReader: &bytes.Buffer{},
			wantErr:    "EOF",
		},
		{
			name:       "rand.Read unexpected EOF",
			key:        make([]byte, 64),
			randReader: bytes.NewBufferString("X"),
			wantErr:    "unexpected EOF",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cipher, err := NewAES256SHA512(context.Background(), tt.key)
			if err != nil {
				t.Fatal(err)
			}

			cipher.(*aes256Sha512).randReader = tt.randReader

			sealed, err := cipher.Seal(tt.input)
			if err != nil && err.Error() != tt.wantErr ||
				err == nil && tt.wantErr != "" {
				t.Fatal(err)
			}

			if !strings.EqualFold(tt.wantSealed, sealed) {
				t.Error(sealed)
			}
		})
	}
}
