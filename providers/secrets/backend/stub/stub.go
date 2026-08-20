/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package stub is a canned-success, no-network StoreBackend for dev and
// tests. New() registers under "vault" so it transparently stands in for the
// real backend (enabled via SECRETS_DEV_STUB_BACKEND=true in main); NewNamed
// supports multi-registration tests.
package stub

import (
	"context"
	"fmt"
	"strings"
	"sync"

	secretsv1alpha1 "github.com/faroshq/provider-secrets/apis/v1alpha1"
	"github.com/faroshq/provider-secrets/backend"
)

// Backend serves fixed in-memory data. Any non-empty credential validates;
// unknown paths return a 404-shaped StatusError so reason classification
// behaves like the real thing.
type Backend struct {
	name string

	mu      sync.RWMutex
	values  map[string]backend.SecretValues
	version int
}

// New returns a stub registered under the real vault backend's name, seeded
// with one demo path.
func New() *Backend { return NewNamed(string(secretsv1alpha1.BackendVault)) }

// NewNamed returns a stub registered under name.
func NewNamed(name string) *Backend {
	return &Backend{
		name: name,
		values: map[string]backend.SecretValues{
			"demo/config": {
				"username": []byte("demo-user"),
				"password": []byte("demo-password"),
			},
		},
		version: 1,
	}
}

var _ backend.StoreBackend = &Backend{}

// Name implements StoreBackend.
func (b *Backend) Name() string { return b.name }

// Validate implements StoreBackend.
func (b *Backend) Validate(_ context.Context, _ *secretsv1alpha1.SecretStore, cred backend.Credential) (backend.StoreInfo, error) {
	if cred.Token == "" {
		return backend.StoreInfo{}, &backend.StatusError{Code: 403, Message: "stub: empty token"}
	}
	return backend.StoreInfo{Version: "stub"}, nil
}

// Fetch implements StoreBackend.
func (b *Backend) Fetch(_ context.Context, _ *secretsv1alpha1.SecretStore, cred backend.Credential, path string) (backend.SecretValues, string, error) {
	if cred.Token == "" {
		return nil, "", &backend.StatusError{Code: 403, Message: "stub: empty token"}
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	values, ok := b.values[strings.Trim(path, "/")]
	if !ok {
		return nil, "", &backend.StatusError{Code: 404, Message: fmt.Sprintf("stub: path %q not found", path)}
	}
	out := make(backend.SecretValues, len(values))
	for k, v := range values {
		out[k] = append([]byte(nil), v...)
	}
	return out, fmt.Sprintf("%d", b.version), nil
}

// Set replaces the values at path (test hook), bumping the version so
// SyncedSecret refresh picks the change up like a real rotation.
func (b *Backend) Set(path string, values backend.SecretValues) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.values[strings.Trim(path, "/")] = values
	b.version++
}
