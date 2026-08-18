// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// Package server wires the provider's HTTP routes: /healthz, the MCP
// handler, and the embedded portal. Template + instance traffic is NOT
// served here — the portal and tenants drive those as CRDs directly
// against kcp (templates.infrastructure.faros.sh and the
// per-template instance kinds), projected to tenant workspaces via the
// CachedResource + APIExport. See providers/infrastructure/portal/src/api.ts.
package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// AssetServer writes the asset at name from distFS to w. Returns
// false when the file is absent so the caller can fall through to
// index.html. Matches the signature of providers/infrastructure/
// assets.go's servePortalAsset.
type AssetServer func(w http.ResponseWriter, r *http.Request, distFS fs.FS, name string) bool

// ErrReadinessNotConfigured is returned when a server has no readiness check.
// A missing check is deliberately not treated as ready: /healthz is the
// liveness contract and /readyz must only pass after the provider wires its
// dependency lifecycle into the server.
var ErrReadinessNotConfigured = errors.New("readiness check is not configured")

// Readiness tracks named provider dependencies. Dependencies start not ready
// and become ready only when Set is called with a nil error. Keeping the gate
// here, rather than in an HTTP handler or a controller implementation, gives
// the process one small contract that can be updated by asynchronous startup
// and observed safely by concurrent probe requests.
type Readiness struct {
	mu           sync.RWMutex
	dependencies map[string]error
}

// NewReadiness creates a gate with the supplied required dependency names.
// Names are used in the returned error to identify the first dependency that
// is still blocking readiness.
func NewReadiness(names ...string) *Readiness {
	r := &Readiness{dependencies: make(map[string]error, len(names))}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		r.dependencies[name] = fmt.Errorf("dependency %q is not ready", name)
	}
	return r
}

// Set records the current state of a dependency. A nil error marks it ready;
// a non-nil error keeps /readyz at 503 and is surfaced as diagnostic context.
// Unknown names are accepted so optional callers can add a dependency without
// reconstructing the gate, while NewReadiness remains the normal path for
// required dependencies.
func (r *Readiness) Set(name string, err error) {
	if r == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dependencies == nil {
		r.dependencies = make(map[string]error)
	}
	r.dependencies[name] = err
}

// Check returns nil only when every dependency is ready.
func (r *Readiness) Check() error {
	if r == nil {
		return ErrReadinessNotConfigured
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.dependencies) == 0 {
		return ErrReadinessNotConfigured
	}
	names := make([]string, 0, len(r.dependencies))
	for name := range r.dependencies {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := r.dependencies[name]; err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

// Deps bundles everything Server needs. The portal fields are exercised
// in the smoke test only.
type Deps struct {
	MCP              http.Handler // /mcp + /mcp/sse handler; may be nil
	DataPlane        http.Handler // /dataplane/* subresource proxy; may be nil
	WorkloadIdentity http.Handler // POST /workload-identities/review attestation
	PortalFileServer http.Handler
	PortalFS         fs.FS
	ServePortalAsset AssetServer
	Readiness        func() error // required dependency gate for /readyz
}

// Server is the wired-up HTTP server. Implements http.Handler so
// main() can install it under a net/http.Server directly.
type Server struct {
	mux       *http.ServeMux
	readiness func() error
}

// New composes the mux. Route order: /healthz and /readyz first, then /mcp +
// /mcp/sse, then the "/" catch-all serving the portal. Templates and
// instances are NOT served as REST here — they live as CRDs in kcp
// (see the comment below). The stdlib ServeMux picks longest-prefix
// wins for path patterns, so this order is illustrative — not load-bearing.
func New(d Deps) *Server {
	s := &Server{
		mux:       http.NewServeMux(),
		readiness: d.Readiness,
	}

	s.mux.HandleFunc("/healthz", s.handleHealthz)
	s.mux.HandleFunc("/readyz", s.handleReadyz)

	// Templates + instances are NOT served here: the portal and tenants
	// read/write them as CRDs directly against kcp (projected via the
	// CachedResource + APIExport). MCP keeps its own kro.Client.
	if d.MCP != nil {
		// One handler covers both /mcp (JSON-RPC POST) and /mcp/sse
		// (streamable transport server-sent events) — the SDK's
		// streamable-HTTP handler dispatches on method internally.
		s.mux.Handle("/mcp", d.MCP)
		s.mux.Handle("/mcp/sse", d.MCP)
	}

	// Data-plane subresource proxy: /dataplane/clusters/<ws>/<resource>/<name>/<verb>.
	// Reached via the hub backend proxy at
	// /services/providers/infrastructure/dataplane/... — see the dataplane
	// package and docs/app-studio-runtime-decoupling.md.
	if d.DataPlane != nil {
		s.mux.Handle("/dataplane/", d.DataPlane)
	}
	if d.WorkloadIdentity != nil {
		s.mux.Handle("/workload-identities/review", d.WorkloadIdentity)
	}

	// Portal fallback — last so all explicit routes above take
	// precedence. Tries the embedded FS first; falls back to
	// index.html so a direct browser visit shows the standalone
	// debug page rather than a 404.
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		clean := strings.TrimPrefix(r.URL.Path, "/")
		if clean != "" && d.ServePortalAsset(w, r, d.PortalFS, clean) {
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		d.PortalFileServer.ServeHTTP(w, r2)
	})

	return s
}

// ServeHTTP satisfies http.Handler so main() can use *Server directly.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.readiness == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "not_ready",
			"error":  ErrReadinessNotConfigured.Error(),
		})
		return
	}
	if err := s.readiness(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status": "not_ready",
			"error":  err.Error(),
		})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
}
