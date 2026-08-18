// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package repositorysync

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestHTTPBundleFetcherUsesCapabilityAndValidatesCoordinates(t *testing.T) {
	fetcher, err := NewHTTPBundleFetcher("http://code.default.svc:8083/")
	if err != nil {
		t.Fatal(err)
	}
	const (
		scope  = "tenant-a"
		name   = "bundle-a"
		digest = "sha256:a"
		token  = "opaque-capability"
	)
	var got *http.Request
	fetcher.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		got = req
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(fmt.Sprintf(`{"name":%q,"digest":%q,"scope":%q,"files":[{"path":".faros/release.yaml","content":"release"}]}`, name, digest, scope))),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})
	checkout := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "sync-checkout"},
		"status": map[string]any{
			"ref":       "main",
			"commitSHA": "abc123",
			"bundleRef": map[string]any{"name": name, "digest": digest},
			"access":    map[string]any{"token": token},
		},
	}}
	result, err := fetcher.Fetch(context.Background(), scope, checkout)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("fetcher did not issue an HTTP request")
	}
	if got.URL.String() != "http://code.default.svc:8083/internal/bundles" {
		t.Fatalf("request URL = %s", got.URL)
	}
	if got.Header.Get("Authorization") != "Bearer "+token {
		t.Fatalf("Authorization = %q", got.Header.Get("Authorization"))
	}
	if got.Header.Get(capabilityScopeHeader) != scope || got.Header.Get(capabilityNameHeader) != name || got.Header.Get(capabilityDigestHeader) != digest {
		t.Fatalf("capability headers = scope %q name %q digest %q", got.Header.Get(capabilityScopeHeader), got.Header.Get(capabilityNameHeader), got.Header.Get(capabilityDigestHeader))
	}
	if result.Ref != "main" || result.CommitSHA != "abc123" || len(result.Files) != 1 {
		t.Fatalf("checkout result = %+v", result)
	}

	fetcher.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"name":"different","digest":"sha256:a","scope":"tenant-a","files":[]}`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})
	if _, err := fetcher.Fetch(context.Background(), scope, checkout); err == nil || !strings.Contains(err.Error(), "coordinates") {
		t.Fatalf("coordinate mismatch error = %v", err)
	}
}

func TestHTTPBundleFetcherAllowsValidEmptyPathCheckoutWithoutTransfer(t *testing.T) {
	fetcher, err := NewHTTPBundleFetcher("https://code.example")
	if err != nil {
		t.Fatal(err)
	}
	called := false
	fetcher.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		called = true
		return nil, fmt.Errorf("unexpected transfer")
	})
	checkout := &unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{
			"ref":       "main",
			"commitSHA": "abc123",
		},
	}}
	result, err := fetcher.Fetch(context.Background(), "tenant-a", checkout)
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("empty path checkout attempted a bundle transfer")
	}
	if result.CommitSHA != "abc123" || len(result.Files) != 0 {
		t.Fatalf("empty checkout result = %+v", result)
	}
}
