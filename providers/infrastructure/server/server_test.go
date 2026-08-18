// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadinessRequiresControllerButLivenessStaysProcessLevel(t *testing.T) {
	state := Readiness{Controller: "starting"}
	srv := New(Deps{Readiness: func() Readiness { return state }})

	liveness := httptest.NewRecorder()
	srv.ServeHTTP(liveness, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if liveness.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", liveness.Code)
	}

	readiness := httptest.NewRecorder()
	srv.ServeHTTP(readiness, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if readiness.Code != http.StatusServiceUnavailable || !strings.Contains(readiness.Body.String(), `"controller":"starting"`) {
		t.Fatalf("starting readyz = %d %q", readiness.Code, readiness.Body.String())
	}

	state = Readiness{Ready: true, Controller: "ready"}
	readiness = httptest.NewRecorder()
	srv.ServeHTTP(readiness, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if readiness.Code != http.StatusOK || !strings.Contains(readiness.Body.String(), `"status":"ready"`) {
		t.Fatalf("running readyz = %d %q", readiness.Code, readiness.Body.String())
	}

	state = Readiness{Controller: "failed", Error: "manager exited"}
	readiness = httptest.NewRecorder()
	srv.ServeHTTP(readiness, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if readiness.Code != http.StatusServiceUnavailable || !strings.Contains(readiness.Body.String(), "manager exited") {
		t.Fatalf("failed readyz = %d %q", readiness.Code, readiness.Body.String())
	}
}

func TestReadinessDefaultsToRESTOnly(t *testing.T) {
	srv := New(Deps{})
	response := httptest.NewRecorder()
	srv.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"controller":"rest-only"`) {
		t.Fatalf("default readyz = %d %q", response.Code, response.Body.String())
	}
}
