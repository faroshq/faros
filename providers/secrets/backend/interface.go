/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package backend defines the seam between the secrets provider's controllers
// and a concrete external secret store (Vault KV v2 today; kubernetes-on-edge
// and cloud secret managers later).
//
// Design rules (mirroring the code provider's GitBackend seam):
//
//   - The interface is the seam; controllers never import a concrete store
//     package.
//   - A backend is a pure, synchronous remote-API dispatcher given an
//     already-resolved credential. It holds no credential state: credentials
//     are passed per call, resolved by the reconciler from the SecretStore's
//     secretRef.
//   - Every method is read-only against the external store. faros never
//     writes back: external stores stay the source of truth.
package backend

import (
	"context"
	"fmt"
	"sort"
	"sync"

	secretsv1alpha1 "github.com/faroshq/provider-secrets/apis/v1alpha1"
)

// Credential is the resolved secret material that authenticates to the
// external store (for Vault, the client token).
type Credential struct {
	Token string
}

// StoreInfo is what Validate discovers about a healthy store.
type StoreInfo struct {
	// Version is the backend server version, when discoverable.
	Version string
}

// SecretValues is the decoded material at one path: property name → value.
type SecretValues map[string][]byte

// StoreBackend is the seam between the controllers and a concrete external
// secret store. Implementations must be safe for concurrent use.
type StoreBackend interface {
	// Name MUST match secretsv1alpha1.StoreBackendType used in
	// SecretStore.spec.backend (lower-case: "vault"). Registered at process
	// startup via Registry.Register.
	Name() string

	// Validate authenticates cred against the store and returns discoverable
	// server info. An error means the credential is bad or the store is
	// unreachable; the SecretStoreController surfaces it on the Validated
	// condition via ClassifyError.
	Validate(ctx context.Context, store *secretsv1alpha1.SecretStore, cred Credential) (StoreInfo, error)

	// Fetch reads all properties at path and returns them with the store's
	// own version identifier for the value (Vault KV v2 metadata.version),
	// empty when the store has none.
	Fetch(ctx context.Context, store *secretsv1alpha1.SecretStore, cred Credential, path string) (SecretValues, string, error)
}

// Registry holds the registered backends, keyed by Name(). Same contract as
// the code provider's backend registry: Register errors on nil/unnamed/
// duplicate so main() fails fast; Get returns ok=false so reconcilers surface
// a condition instead of crashing.
type Registry struct {
	mu     sync.RWMutex
	byName map[string]StoreBackend
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{byName: map[string]StoreBackend{}}
}

// Register adds b under its Name().
func (r *Registry) Register(b StoreBackend) error {
	if b == nil {
		return fmt.Errorf("backend is nil")
	}
	name := b.Name()
	if name == "" {
		return fmt.Errorf("backend has empty name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.byName[name]; dup {
		return fmt.Errorf("backend %q already registered", name)
	}
	r.byName[name] = b
	return nil
}

// Get returns the backend registered under name.
func (r *Registry) Get(name string) (StoreBackend, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.byName[name]
	return b, ok
}

// Names returns the registered backend names, sorted for deterministic logs.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.byName))
	for n := range r.byName {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
