// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadinessGateAndLivenessAreIndependent(t *testing.T) {
	readiness := NewReadiness("provider", "controller", "backend")
	srv := New(Deps{Readiness: readiness.Check})

	ready := httptest.NewRecorder()
	srv.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("unready /readyz status = %d, want %d", ready.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(ready.Body.String(), "backend") {
		t.Fatalf("unready /readyz body = %q, want blocking dependency", ready.Body.String())
	}

	health := httptest.NewRecorder()
	srv.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("unready /healthz status = %d, want %d", health.Code, http.StatusOK)
	}
	if !strings.Contains(health.Body.String(), `"status":"ok"`) {
		t.Fatalf("/healthz body = %q, want liveness response", health.Body.String())
	}

	readiness.Set("provider", nil)
	readiness.Set("controller", nil)
	ready = httptest.NewRecorder()
	srv.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("backend-unready /readyz status = %d, want %d", ready.Code, http.StatusServiceUnavailable)
	}

	readiness.Set("backend", nil)
	ready = httptest.NewRecorder()
	srv.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusOK {
		t.Fatalf("ready /readyz status = %d, want %d", ready.Code, http.StatusOK)
	}
	if !strings.Contains(ready.Body.String(), `"status":"ready"`) {
		t.Fatalf("ready /readyz body = %q, want ready response", ready.Body.String())
	}

	readiness.Set("controller", errors.New("manager exited"))
	ready = httptest.NewRecorder()
	srv.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusServiceUnavailable || !strings.Contains(ready.Body.String(), "manager exited") {
		t.Fatalf("failed /readyz = %d %q, want 503 with manager error", ready.Code, ready.Body.String())
	}
}

func TestReadinessWithoutAConfiguredCheckFailsClosed(t *testing.T) {
	srv := New(Deps{})
	ready := httptest.NewRecorder()
	srv.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing readiness check status = %d, want %d", ready.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(ready.Body.String(), ErrReadinessNotConfigured.Error()) {
		t.Fatalf("missing readiness check body = %q, want configuration error", ready.Body.String())
	}
}
