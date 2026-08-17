/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package backend defines the contract every infrastructure-provider
// backend implements. The platform Template controller never depends on a
// specific provisioning engine — it dispatches through this interface.
//
// PR A ships the interface + a stub implementation used by the
// platform's own tests. PR C ships the real kro backend. Future PRs
// might add terraform, cloud, etc.; nothing in this file or in the
// Template controller changes when they land.
package backend

import (
	"context"
	"fmt"
	"sync"

	"k8s.io/client-go/rest"

	infrastructurev1alpha1 "github.com/faroshq/provider-infrastructure/apis/v1alpha1"
)

// Backend is the seam between the platform layer and any concrete
// provisioning engine.
//
// Lifecycle calls (SetupTemplate / TeardownTemplate) are invoked
// by the Template controller whenever a Template CR is added,
// updated, or deleted. Tenant-facing reconciliation of the stable Instance
// kind is owned by controller/instance; a backend prepares and runs only its
// engine-specific runtime machinery.
type Backend interface {
	// Name MUST match the string operators put in Template.spec.backend.
	// Lower-case, kebab-case, registered at process startup via the
	// package-level Register function below.
	Name() string

	// SetupTemplate is called after the platform validates the Template's
	// flattened Instance values contract. The backend does engine-specific
	// preparation (the kro backend writes an RGD; a hypothetical terraform
	// backend stages a module). The returned status is mirrored onto
	// Template.status.backend; an error moves Ready=False/BackendError.
	//
	// SetupTemplate MUST be idempotent — the platform calls it on
	// every reconcile pass for a given Template generation.
	SetupTemplate(ctx context.Context, tmpl *infrastructurev1alpha1.Template) (TemplateStatus, error)

	// TeardownTemplate is called during Template deletion. The backend should
	// clean up any per-template runtime state it owns. Instance CRs may still
	// exist; a backend may refuse teardown until they are gone (the platform
	// requeues the finalizer).
	//
	// TeardownTemplate MUST be idempotent — the platform may call it
	// repeatedly until success.
	TeardownTemplate(ctx context.Context, tmpl *infrastructurev1alpha1.Template) error

	// Run starts any backend-owned long-running loop and blocks until ctx is
	// cancelled. The current kro backend has no loop here: upstream kro runs on
	// the runtime cluster and the provider's Instance controller owns the
	// kcp-to-runtime bridge.
	//
	// Run returns when ctx is done. Errors during steady state are
	// logged + retried by the backend itself; a returned error means
	// "the backend cannot continue" and brings down the process.
	Run(ctx context.Context, vwConfig *rest.Config) error
}

// TemplateStatus is what the backend reports for one Template. The
// platform mirrors this verbatim onto Template.status.backend.
type TemplateStatus struct {
	// Ready is true when the backend has fully accepted the Template
	// and is prepared to reconcile instances. False until then;
	// Message should explain why.
	Ready bool

	// Message is a short human-readable string the platform surfaces
	// on the Template's Ready=false condition. Empty when Ready=true.
	Message string
}

// Registry holds the backends a process has registered, indexed by
// their Name(). The Template controller looks here when dispatching
// SetupTemplate / TeardownTemplate. Callers register backends in
// main() before starting the controller manager.
//
// Concurrency: Registry is goroutine-safe for the dispatcher's
// concurrent reconciles; registration itself is expected to happen
// during single-threaded startup so the mutex is for paranoia.
type Registry struct {
	mu     sync.RWMutex
	byName map[string]Backend
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{byName: map[string]Backend{}}
}

// Register adds a backend by Name(). Returns an error if a backend
// with the same name was previously registered — main() should fail
// fast rather than silently overwrite.
func (r *Registry) Register(b Backend) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if b == nil || b.Name() == "" {
		return fmt.Errorf("backend: cannot register nil or unnamed backend")
	}
	if _, ok := r.byName[b.Name()]; ok {
		return fmt.Errorf("backend: %q already registered", b.Name())
	}
	r.byName[b.Name()] = b
	return nil
}

// Get returns the backend registered under name, or false when the
// name is unknown. The Template controller uses ok=false to set a
// BackendNotFound condition without crashing the process.
func (r *Registry) Get(name string) (Backend, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.byName[name]
	return b, ok
}

// Names returns every registered backend's Name() in deterministic
// order. Used by /healthz + diagnostics; not on the hot path.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.byName))
	for n := range r.byName {
		names = append(names, n)
	}
	// Sort to make logs/diagnostic output stable.
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] < names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
	return names
}
