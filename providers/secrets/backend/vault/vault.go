/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package vault implements the StoreBackend seam against HashiCorp Vault's
// KV version 2 engine over its plain HTTP API. The surface we need — token
// lookup-self, KV v2 data read — is small and stable, so we speak it directly
// with net/http rather than pulling the vault SDK dependency tree into the
// provider (external stores stay the source of truth; we only ever read).
package vault

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	secretsv1alpha1 "github.com/faroshq/provider-secrets/apis/v1alpha1"
	"github.com/faroshq/provider-secrets/backend"
)

// defaultMount is the KV v2 mount Vault enables out of the box; used when
// spec.vault.mount is empty (the CRD also defaults it server-side).
const defaultMount = "secret"

// maxErrorBody bounds how much of an error response we read before
// discarding it. Bodies are never surfaced — see safeMessage.
const maxErrorBody = 4 << 10

// Backend is the Vault KV v2 implementation. Stateless; safe for concurrent
// use. The zero value is not usable — construct with New.
type Backend struct {
	client *http.Client
}

// New returns a Vault backend with a bounded-timeout HTTP client.
func New() *Backend {
	return &Backend{client: &http.Client{Timeout: 15 * time.Second}}
}

var _ backend.StoreBackend = &Backend{}

// Name implements StoreBackend.
func (b *Backend) Name() string { return string(secretsv1alpha1.BackendVault) }

// Validate implements StoreBackend: it authenticates the token via
// auth/token/lookup-self and reports the server version from sys/health
// (best effort — version is cosmetic).
func (b *Backend) Validate(ctx context.Context, store *secretsv1alpha1.SecretStore, cred backend.Credential) (backend.StoreInfo, error) {
	cfg, err := vaultConfig(store)
	if err != nil {
		return backend.StoreInfo{}, err
	}
	// lookup-self is the cheapest authenticated call: it proves both
	// reachability and that the token is live, without touching any secret.
	var lookup struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := b.get(ctx, cfg, cred, "auth/token/lookup-self", &lookup); err != nil {
		return backend.StoreInfo{}, err
	}

	info := backend.StoreInfo{}
	var health struct {
		Version string `json:"version"`
	}
	if err := b.get(ctx, cfg, cred, "sys/health", &health); err == nil {
		info.Version = health.Version
	}
	return info, nil
}

// Fetch implements StoreBackend: it reads all properties at path from the
// KV v2 engine and returns them with the KV metadata version.
func (b *Backend) Fetch(ctx context.Context, store *secretsv1alpha1.SecretStore, cred backend.Credential, path string) (backend.SecretValues, string, error) {
	cfg, err := vaultConfig(store)
	if err != nil {
		return nil, "", err
	}
	// KV v2 reads go through <mount>/data/<path>; values live under
	// data.data and the engine's monotonically-increasing version under
	// data.metadata.version.
	apiPath := cfg.mount + "/data/" + strings.Trim(path, "/")
	var out struct {
		Data struct {
			Data     map[string]json.RawMessage `json:"data"`
			Metadata struct {
				Version int `json:"version"`
			} `json:"metadata"`
		} `json:"data"`
	}
	if err := b.get(ctx, cfg, cred, apiPath, &out); err != nil {
		return nil, "", err
	}

	values := make(backend.SecretValues, len(out.Data.Data))
	for k, raw := range out.Data.Data {
		values[k] = decodeValue(raw)
	}
	version := ""
	if out.Data.Metadata.Version > 0 {
		version = fmt.Sprintf("%d", out.Data.Metadata.Version)
	}
	return values, version, nil
}

// decodeValue turns one KV v2 JSON value into secret bytes: strings decode to
// their contents; anything else (numbers, bools, nested objects) is kept as
// its compact JSON encoding so no material is silently dropped.
func decodeValue(raw json.RawMessage) []byte {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return []byte(s)
	}
	return []byte(raw)
}

type config struct {
	address   string
	mount     string
	namespace string
}

func vaultConfig(store *secretsv1alpha1.SecretStore) (config, error) {
	if store.Spec.Vault == nil {
		return config{}, &backend.StatusError{Message: "spec.vault is required for backend=vault"}
	}
	mount := store.Spec.Vault.Mount
	if mount == "" {
		mount = defaultMount
	}
	return config{
		address:   strings.TrimRight(store.Spec.Vault.Address, "/"),
		mount:     strings.Trim(mount, "/"),
		namespace: store.Spec.Vault.Namespace,
	}, nil
}

// get performs one authenticated Vault API GET and decodes the JSON response
// into out. Failures come back as *backend.StatusError with a bounded,
// body-free message so they are safe to surface on CR status.
func (b *Backend) get(ctx context.Context, cfg config, cred backend.Credential, apiPath string, out any) error {
	url := cfg.address + "/v1/" + apiPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return &backend.StatusError{Message: fmt.Sprintf("build request for %s: %v", apiPath, err)}
	}
	req.Header.Set("X-Vault-Token", cred.Token)
	if cfg.namespace != "" {
		req.Header.Set("X-Vault-Namespace", cfg.namespace)
	}

	resp, err := b.client.Do(req)
	if err != nil {
		// Deliberately keep the transport error (it wraps net.Error /
		// context.DeadlineExceeded, which ClassifyError inspects) but not
		// the full URL — the path is enough and addresses can carry creds.
		return fmt.Errorf("vault request %s: %w", apiPath, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody))
		return &backend.StatusError{
			Code:    resp.StatusCode,
			Message: fmt.Sprintf("vault %s", apiPath),
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return &backend.StatusError{Message: fmt.Sprintf("vault %s: undecodable response", apiPath)}
	}
	return nil
}
