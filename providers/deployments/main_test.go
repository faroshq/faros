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
