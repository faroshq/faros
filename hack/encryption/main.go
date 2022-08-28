package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

func main() {
	ctx := context.Background()

	err := generateKey(ctx)
	if err != nil {
		panic(err)
	}
}

func generateKey(ctx context.Context) error {
	key := make([]byte, 64)

	_, err := rand.Read(key)
	if err != nil {
		return err
	}

	keyBase64 := base64.StdEncoding.EncodeToString(key)

	fmt.Printf("encryption key in base64: %s \n", keyBase64)
	return nil
}
