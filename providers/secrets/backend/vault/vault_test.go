/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package vault

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	secretsv1alpha1 "github.com/faroshq/provider-secrets/apis/v1alpha1"
	"github.com/faroshq/provider-secrets/backend"
)

func fakeVault(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("X-Vault-Token") != "good-token" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"errors":["permission denied"]}`))
			return false
		}
		return true
	}
	mux.HandleFunc("/v1/auth/token/lookup-self", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		_, _ = w.Write([]byte(`{"data":{"id":"token-accessor"}}`))
	})
	mux.HandleFunc("/v1/sys/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"version":"1.17.2"}`))
	})
	mux.HandleFunc("/v1/secret/data/app/config", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		_, _ = w.Write([]byte(`{"data":{"data":{"username":"alice","port":5432},"metadata":{"version":3}}}`))
	})
	mux.HandleFunc("/v1/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errors":[]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func store(address string) *secretsv1alpha1.SecretStore {
	return &secretsv1alpha1.SecretStore{
		Spec: secretsv1alpha1.SecretStoreSpec{
			Backend: secretsv1alpha1.BackendVault,
			Vault:   &secretsv1alpha1.VaultStoreSpec{Address: address},
		},
	}
}

func TestValidate(t *testing.T) {
	srv := fakeVault(t)
	b := New()

	info, err := b.Validate(context.Background(), store(srv.URL), backend.Credential{Token: "good-token"})
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if info.Version != "1.17.2" {
		t.Fatalf("version = %q, want 1.17.2", info.Version)
	}
}

func TestValidateBadToken(t *testing.T) {
	srv := fakeVault(t)
	b := New()

	_, err := b.Validate(context.Background(), store(srv.URL), backend.Credential{Token: "bad"})
	if err == nil {
		t.Fatal("Validate with bad token succeeded")
	}
	if got := backend.ClassifyError(err); got != secretsv1alpha1.ReasonAccessDenied {
		t.Fatalf("ClassifyError = %q, want AccessDenied", got)
	}
}

func TestValidateMissingVaultSpec(t *testing.T) {
	b := New()
	s := store("http://ignored")
	s.Spec.Vault = nil
	if _, err := b.Validate(context.Background(), s, backend.Credential{Token: "good-token"}); err == nil {
		t.Fatal("Validate without spec.vault succeeded")
	}
}

func TestFetch(t *testing.T) {
	srv := fakeVault(t)
	b := New()

	values, version, err := b.Fetch(context.Background(), store(srv.URL), backend.Credential{Token: "good-token"}, "app/config")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if version != "3" {
		t.Fatalf("version = %q, want 3", version)
	}
	if string(values["username"]) != "alice" {
		t.Fatalf("username = %q, want alice", values["username"])
	}
	// Non-string values keep their JSON encoding rather than being dropped.
	if string(values["port"]) != "5432" {
		t.Fatalf("port = %q, want 5432", values["port"])
	}
}

func TestFetchNotFound(t *testing.T) {
	srv := fakeVault(t)
	b := New()

	_, _, err := b.Fetch(context.Background(), store(srv.URL), backend.Credential{Token: "good-token"}, "missing/path")
	if err == nil {
		t.Fatal("Fetch of missing path succeeded")
	}
	if got := backend.ClassifyError(err); got != secretsv1alpha1.ReasonPathNotFound {
		t.Fatalf("ClassifyError = %q, want PathNotFound", got)
	}
}

func TestFetchServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	b := New()

	_, _, err := b.Fetch(context.Background(), store(srv.URL), backend.Credential{Token: "good-token"}, "app/config")
	if err == nil {
		t.Fatal("Fetch against 500 server succeeded")
	}
	if got := backend.ClassifyError(err); got != secretsv1alpha1.ReasonStoreUnavailable {
		t.Fatalf("ClassifyError = %q, want StoreUnavailable", got)
	}
}

func TestFetchUnreachable(t *testing.T) {
	b := New()
	// Closed port: transport error, classified as unavailable.
	_, _, err := b.Fetch(context.Background(), store("http://127.0.0.1:1"), backend.Credential{Token: "t"}, "app/config")
	if err == nil {
		t.Fatal("Fetch against unreachable server succeeded")
	}
	if got := backend.ClassifyError(err); got != secretsv1alpha1.ReasonStoreUnavailable {
		t.Fatalf("ClassifyError = %q, want StoreUnavailable", got)
	}
}
