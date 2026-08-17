// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestReadinessHandlerTracksControllerState(t *testing.T) {
	var ready atomic.Bool
	recorder := httptest.NewRecorder()
	readinessHandler(&ready).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unsynchronized controller reported ready: %d", recorder.Code)
	}
	ready.Store(true)
	recorder = httptest.NewRecorder()
	readinessHandler(&ready).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("synchronized controller did not report ready: %d", recorder.Code)
	}
	ready.Store(false)
	recorder = httptest.NewRecorder()
	readinessHandler(&ready).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("stopped controller remained ready: %d", recorder.Code)
	}
}
