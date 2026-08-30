// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package instance

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
)

func TestNewAndStartCanRebuildAcrossLeadershipTerms(t *testing.T) {
	cfg := Config{
		ProviderConfig: &rest.Config{
			Host: "https://kcp.example",
			Transport: instanceControllerRoundTripFunc(func(r *http.Request) (*http.Response, error) {
				body := `{"apiVersion":"v1","kind":"APIResourceList","groupVersion":"apis.kcp.io/v1alpha1","resources":[{"name":"apiexportendpointslices","kind":"APIExportEndpointSlice","verbs":["get","list","watch"]}]}`
				switch r.URL.Path {
				case "/api":
					body = `{"apiVersion":"v1","kind":"APIVersions","versions":["v1"]}`
				case "/apis":
					body = `{"apiVersion":"v1","kind":"APIGroupList","groups":[{"name":"apis.kcp.io","preferredVersion":{"groupVersion":"apis.kcp.io/v1alpha1","version":"v1alpha1"},"versions":[{"groupVersion":"apis.kcp.io/v1alpha1","version":"v1alpha1"}]}]}`
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(body)),
				}, nil
			}),
		},
		APIExportName: "infrastructure.providers.faros.sh",
		Runtime:       dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()),
	}

	first, err := New(cfg)
	if err != nil {
		t.Fatalf("first New() error = %v", err)
	}
	assertNameValidationIsScopedToInstanceController(t, first)
	firstCtx, firstCancel := context.WithCancel(context.Background())
	firstCancel()
	if err := first.Start(firstCtx); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}

	second, err := New(cfg)
	if err != nil {
		t.Fatalf("second New() error = %v", err)
	}
	assertNameValidationIsScopedToInstanceController(t, second)
	secondCtx, secondCancel := context.WithCancel(context.Background())
	secondCancel()
	if err := second.Start(secondCtx); err != nil {
		t.Fatalf("second Start() error = %v", err)
	}
}

func assertNameValidationIsScopedToInstanceController(t *testing.T, c *Controller) {
	t.Helper()
	if skip := c.mgr.GetControllerOptions().SkipNameValidation; skip != nil && *skip {
		t.Fatal("manager-wide SkipNameValidation is enabled; the exception must be scoped to the instance controller")
	}
}

type instanceControllerRoundTripFunc func(*http.Request) (*http.Response, error)

func (f instanceControllerRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
