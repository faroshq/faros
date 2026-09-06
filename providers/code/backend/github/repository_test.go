/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	codev1alpha1 "github.com/faroshq/provider-code/apis/v1alpha1"
	"github.com/faroshq/provider-code/backend"
)

func TestEnsureRepositoryReconcilesExistingDescription(t *testing.T) {
	var patchBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/repos/acme/widgets" {
			t.Fatalf("unexpected GitHub path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_, _ = io.WriteString(w, `{"id":42,"name":"widgets","description":"old","html_url":"https://github.test/acme/widgets","clone_url":"https://github.test/acme/widgets.git","ssh_url":"git@github.test:acme/widgets.git"}`)
		case http.MethodPatch:
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("read patch body: %v", err)
			}
			patchBody = string(body)
			_, _ = io.WriteString(w, `{"id":42,"name":"widgets","description":"new","html_url":"https://github.test/acme/widgets","clone_url":"https://github.test/acme/widgets.git","ssh_url":"git@github.test:acme/widgets.git"}`)
		default:
			t.Fatalf("unexpected GitHub method %s", r.Method)
		}
	}))
	defer server.Close()

	repo := &codev1alpha1.Repository{Spec: codev1alpha1.RepositorySpec{
		ConnectionRef: "conn",
		Name:          "widgets",
		Description:   "new",
		AutoInit:      true,
		DefaultBranch: "develop",
	}}
	got, err := New().EnsureRepository(context.Background(),
		&codev1alpha1.Connection{Spec: codev1alpha1.ConnectionSpec{Provider: codev1alpha1.ProviderGitHub, Owner: "acme", BaseURL: server.URL}},
		backend.Credential{Token: "token"}, repo)
	if err != nil {
		t.Fatalf("EnsureRepository returned error: %v", err)
	}
	if got.RepoID != "42" {
		t.Fatalf("RepoID = %q, want 42", got.RepoID)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(patchBody), &fields); err != nil {
		t.Fatalf("decode patch body %q: %v", patchBody, err)
	}
	if fields["description"] != "new" {
		t.Fatalf("description patch = %#v, want new", fields["description"])
	}
	if _, ok := fields["auto_init"]; ok {
		t.Fatalf("existing-repository patch unexpectedly included auto_init: %s", patchBody)
	}
	if _, ok := fields["default_branch"]; ok {
		t.Fatalf("existing-repository patch unexpectedly included default_branch: %s", patchBody)
	}
}

func TestEnsureRepositoryRejectsHostRepositoryReplacementBeforePatch(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.Method != http.MethodGet || r.URL.Path != "/api/v3/repos/acme/widgets" {
			t.Fatalf("unexpected GitHub request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":43,"name":"widgets","description":"replacement"}`)
	}))
	defer server.Close()

	repo := &codev1alpha1.Repository{
		Spec: codev1alpha1.RepositorySpec{ConnectionRef: "conn", Name: "widgets"},
		Status: codev1alpha1.RepositoryStatus{
			RepoID: "42",
			Identity: &codev1alpha1.RepositoryIdentity{
				ConnectionRef: "conn",
				Provider:      codev1alpha1.ProviderGitHub,
				BaseURL:       server.URL,
				Owner:         "acme",
				Name:          "widgets",
			},
		},
	}
	_, err := New().EnsureRepository(context.Background(),
		&codev1alpha1.Connection{Spec: codev1alpha1.ConnectionSpec{Provider: codev1alpha1.ProviderGitHub, Owner: "acme", BaseURL: server.URL}},
		backend.Credential{Token: "token"}, repo)
	if err == nil || !strings.Contains(err.Error(), "identity conflict") {
		t.Fatalf("EnsureRepository error = %v, want identity conflict", err)
	}
	if strings.Join(methods, ",") != http.MethodGet {
		t.Fatalf("replacement reconciliation made requests %v, want only GET", methods)
	}
}

func TestEnsureRepositoryRejectsRecordedIdentityMutationBeforeRequest(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		t.Fatalf("unexpected GitHub request %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	repo := &codev1alpha1.Repository{
		Spec: codev1alpha1.RepositorySpec{ConnectionRef: "conn", Name: "other"},
		Status: codev1alpha1.RepositoryStatus{Identity: &codev1alpha1.RepositoryIdentity{
			ConnectionRef: "conn",
			Provider:      codev1alpha1.ProviderGitHub,
			BaseURL:       server.URL,
			Owner:         "acme",
			Name:          "widgets",
		}},
	}
	_, err := New().EnsureRepository(context.Background(),
		&codev1alpha1.Connection{Spec: codev1alpha1.ConnectionSpec{Provider: codev1alpha1.ProviderGitHub, Owner: "acme", BaseURL: server.URL}},
		backend.Credential{Token: "token"}, repo)
	if err == nil || !strings.Contains(err.Error(), "identity conflict") {
		t.Fatalf("EnsureRepository error = %v, want identity conflict", err)
	}
	if requests != 0 {
		t.Fatalf("identity mutation made %d HTTP requests", requests)
	}
}

func TestDeleteRepositoryTreatsMissingRemoteAsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v3/repos/acme/widgets" {
			t.Fatalf("unexpected GitHub request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"Not Found"}`)
	}))
	defer server.Close()

	repo := &codev1alpha1.Repository{
		Spec: codev1alpha1.RepositorySpec{ConnectionRef: "conn", Name: "widgets"},
		Status: codev1alpha1.RepositoryStatus{Identity: &codev1alpha1.RepositoryIdentity{
			ConnectionRef: "conn",
			Provider:      codev1alpha1.ProviderGitHub,
			BaseURL:       server.URL,
			Owner:         "acme",
			Name:          "widgets",
		}},
	}
	if err := New().DeleteRepository(context.Background(),
		&codev1alpha1.Connection{Spec: codev1alpha1.ConnectionSpec{Provider: codev1alpha1.ProviderGitHub, Owner: "acme", BaseURL: server.URL}},
		backend.Credential{Token: "token"}, repo); err != nil {
		t.Fatalf("DeleteRepository returned error for missing remote: %v", err)
	}
}

func TestDeleteRepositoryRejectsHostRepositoryReplacement(t *testing.T) {
	var deletes int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/repos/acme/widgets" {
			t.Fatalf("unexpected GitHub path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, `{"id":43,"name":"widgets"}`)
			return
		}
		if r.Method == http.MethodDelete {
			deletes++
			t.Fatalf("replacement repository was deleted")
		}
		t.Fatalf("unexpected GitHub method %s", r.Method)
	}))
	defer server.Close()

	repo := &codev1alpha1.Repository{
		Spec: codev1alpha1.RepositorySpec{ConnectionRef: "conn", Name: "widgets"},
		Status: codev1alpha1.RepositoryStatus{
			RepoID: "42",
			Identity: &codev1alpha1.RepositoryIdentity{
				ConnectionRef: "conn",
				Provider:      codev1alpha1.ProviderGitHub,
				BaseURL:       server.URL,
				Owner:         "acme",
				Name:          "widgets",
			},
		},
	}
	if err := New().DeleteRepository(context.Background(),
		&codev1alpha1.Connection{Spec: codev1alpha1.ConnectionSpec{Provider: codev1alpha1.ProviderGitHub, Owner: "acme", BaseURL: server.URL}},
		backend.Credential{Token: "token"}, repo); err == nil || !strings.Contains(err.Error(), "identity conflict") {
		t.Fatalf("DeleteRepository error = %v, want identity conflict", err)
	}
	if deletes != 0 {
		t.Fatalf("replacement delete count = %d, want 0", deletes)
	}
}
