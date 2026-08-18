// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package commitbundle

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCapabilityHandlerBindsCoordinatesAndConsumesOnce(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.Put(t.Context(), "tenant-a", []File{{Path: ".faros/release.yaml", Content: "apiVersion: deployments.faros.sh/v1alpha1\n"}})
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewCapabilitySigner()
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := signer.Issue(ref.Scope, ref.Name, ref.Digest, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	handler := NewCapabilityHandler(store, signer)

	request := func(method, bearer, scope, name, digest string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, CapabilityPath, nil)
		req.Header.Set(CapabilityTokenHeader, bearer)
		req.Header.Set(CapabilityScopeHeader, scope)
		req.Header.Set(CapabilityNameHeader, name)
		req.Header.Set(CapabilityDigestHeader, digest)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	wrongScope := request(http.MethodGet, "Bearer "+token, "tenant-b", ref.Name, ref.Digest)
	if wrongScope.Code != http.StatusUnauthorized {
		t.Fatalf("wrong scope status = %d, want %d", wrongScope.Code, http.StatusUnauthorized)
	}

	first := request(http.MethodGet, "Bearer "+token, ref.Scope, ref.Name, ref.Digest)
	if first.Code != http.StatusOK {
		t.Fatalf("first redemption status = %d, body %q", first.Code, first.Body.String())
	}
	if got := first.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	var got Bundle
	if err := json.Unmarshal(first.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode first redemption: %v", err)
	}
	if got.Scope != ref.Scope || got.Name != ref.Name || got.Digest != ref.Digest || len(got.Files) != 1 {
		t.Fatalf("redeemed bundle = %+v, want scope/name/digest %q/%q/%q and one file", got, ref.Scope, ref.Name, ref.Digest)
	}

	second := request(http.MethodGet, "Bearer "+token, ref.Scope, ref.Name, ref.Digest)
	if second.Code != http.StatusGone {
		t.Fatalf("second redemption status = %d, body %q; capability must be one-time", second.Code, second.Body.String())
	}

	post := request(http.MethodPost, "Bearer "+token, ref.Scope, ref.Name, ref.Digest)
	if post.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want %d", post.Code, http.StatusMethodNotAllowed)
	}
	if strings.Contains(first.Body.String(), token) {
		t.Fatalf("response body echoed bearer capability")
	}
}

func TestCapabilityHandlerConsumesOnceAcrossReplicaHandlers(t *testing.T) {
	root := t.TempDir()
	firstStore, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	secondStore, err := NewFileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := firstStore.Put(t.Context(), "tenant-a", []File{{Path: "app.yaml", Content: "kind: App\n"}})
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewCapabilitySignerWithKey([]byte(strings.Repeat("k", minCapabilityKeyBytes)))
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := signer.Issue(ref.Scope, ref.Name, ref.Digest, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	handlers := []http.Handler{
		NewCapabilityHandler(firstStore, signer),
		NewCapabilityHandler(secondStore, signer),
	}
	start := make(chan struct{})
	responses := make(chan int, len(handlers))
	var wg sync.WaitGroup
	for _, handler := range handlers {
		wg.Add(1)
		go func(handler http.Handler) {
			defer wg.Done()
			<-start
			req := httptest.NewRequest(http.MethodGet, CapabilityPath, nil)
			req.Header.Set(CapabilityTokenHeader, "Bearer "+token)
			req.Header.Set(CapabilityScopeHeader, ref.Scope)
			req.Header.Set(CapabilityNameHeader, ref.Name)
			req.Header.Set(CapabilityDigestHeader, ref.Digest)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			responses <- response.Code
		}(handler)
	}
	close(start)
	wg.Wait()
	close(responses)

	counts := map[int]int{}
	for code := range responses {
		counts[code]++
	}
	if counts[http.StatusOK] != 1 || counts[http.StatusGone] != 1 {
		t.Fatalf("concurrent replica responses = %#v, want one 200 and one 410", counts)
	}
}

func TestCapabilitySignerRejectsTamperingAndExpiry(t *testing.T) {
	signer, err := NewCapabilitySigner()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	token, expiry, err := signer.Issue("tenant-a", "bundle-a", "sha256:a", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := signer.Validate(token, "tenant-a", "bundle-a", "sha256:b", now); err == nil {
		t.Fatal("Validate accepted a digest not covered by the capability")
	}
	if err := signer.Validate(token+"x", "tenant-a", "bundle-a", "sha256:a", now); err == nil {
		t.Fatal("Validate accepted a tampered capability")
	}
	if !expiry.Equal(now.Add(capabilityTTL)) {
		t.Fatalf("expiry = %s, want %s", expiry, now.Add(capabilityTTL))
	}
	if err := signer.Validate(token, "tenant-a", "bundle-a", "sha256:a", expiry); err == nil {
		t.Fatal("Validate accepted an expired capability")
	}
}

func TestCapabilitySignerUsesSharedEnvKeyAcrossReplicas(t *testing.T) {
	t.Setenv(EnvCapabilitySigningKey, strings.Repeat("k", minCapabilityKeyBytes))
	issuer, err := NewCapabilitySignerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := NewCapabilitySignerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	token, _, err := issuer.Issue("tenant-a", "bundle-a", "sha256:a", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := consumer.Validate(token, "tenant-a", "bundle-a", "sha256:a", now); err != nil {
		t.Fatalf("shared signer rejected a token issued by another replica: %v", err)
	}

	t.Setenv(EnvCapabilitySigningKey, "")
	first, err := NewCapabilitySignerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewCapabilitySignerFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	token, _, err = first.Issue("tenant-a", "bundle-a", "sha256:a", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Validate(token, "tenant-a", "bundle-a", "sha256:a", now); err == nil {
		t.Fatal("random fallback unexpectedly shared a key across processes")
	}
}

func TestCapabilitySignerRejectsShortSharedKey(t *testing.T) {
	if _, err := NewCapabilitySignerWithKey([]byte("too-short")); err == nil {
		t.Fatal("NewCapabilitySignerWithKey accepted an undersized key")
	}
	t.Setenv(EnvCapabilitySigningKey, strings.Repeat("x", minCapabilityKeyBytes-1))
	if _, err := NewCapabilitySignerFromEnv(); err == nil {
		t.Fatal("NewCapabilitySignerFromEnv accepted an undersized configured key")
	}
}
