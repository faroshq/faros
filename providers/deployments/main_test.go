// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestProviderCommand(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "default serve", want: "serve"},
		{name: "explicit serve", args: []string{"serve"}, want: "serve"},
		{name: "init", args: []string{"init"}, want: "init"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := providerCommand(tc.args)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("command = %q, want %q", got, tc.want)
			}
		})
	}
	if _, err := providerCommand([]string{"unknown"}); err == nil {
		t.Fatal("unknown subcommand must fail")
	}
	if _, err := providerCommand([]string{"serve", "unexpected"}); err == nil {
		t.Fatal("trailing arguments must fail")
	}
}

func TestPortalAssetsAreEmbeddedAndServed(t *testing.T) {
	_, distFS, err := portalHandler()
	if err != nil {
		t.Fatalf("portalHandler: %v", err)
	}
	recorder := httptest.NewRecorder()
	if !servePortalAsset(recorder, httptest.NewRequest(http.MethodGet, "/main.js", nil), distFS, "main.js") {
		t.Fatal("embedded main.js was not served")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("main.js status = %d, want 200", recorder.Code)
	}
	if !strings.Contains(recorder.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("main.js content type = %q, want javascript", recorder.Header().Get("Content-Type"))
	}
	body, err := io.ReadAll(recorder.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "faros-provider-deployments") {
		t.Fatal("served bundle does not register the Deployments custom element")
	}

	miss := httptest.NewRecorder()
	if servePortalAsset(miss, httptest.NewRequest(http.MethodGet, "/missing.js", nil), distFS, "missing.js") {
		t.Fatal("missing asset was reported as served")
	}
}
