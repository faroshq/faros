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
	"net/http"
	"net/http/httptest"
	"testing"

	codev1alpha1 "github.com/faroshq/provider-code/apis/v1alpha1"
	"github.com/faroshq/provider-code/backend"
)

func TestEnsureChangeRequestObservesExistingReviewWithoutDuplicate(t *testing.T) {
	createCalls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/repos/acme/widgets/pulls":
			_, _ = w.Write([]byte(`[{"number":7}]`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v3/repos/acme/widgets/pulls":
			createCalls++
			_, _ = w.Write([]byte(`{"number":8}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/repos/acme/widgets/pulls/7":
			_, _ = w.Write([]byte(`{"number":7,"html_url":"https://example.test/pull/7","state":"open","merged":false,"head":{"sha":"head-sha"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/repos/acme/widgets/pulls/7/reviews":
			_, _ = w.Write([]byte(`[{"state":"APPROVED","user":{"id":42}}]`))
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()
	result, err := New().EnsureChangeRequest(context.Background(), &codev1alpha1.Connection{Spec: codev1alpha1.ConnectionSpec{Owner: "acme", BaseURL: srv.URL}}, backend.Credential{Token: "token"}, &codev1alpha1.Repository{Spec: codev1alpha1.RepositorySpec{Name: "widgets"}}, backend.ChangeRequestInput{BaseBranch: "main", HeadBranch: "feature", Title: "Deploy"})
	if err != nil {
		t.Fatalf("EnsureChangeRequest: %v", err)
	}
	if createCalls != 0 {
		t.Fatalf("created %d duplicate pull requests", createCalls)
	}
	if result.Number != 7 || result.Approvals != 1 || !result.Open || result.HeadSHA != "head-sha" {
		t.Fatalf("result = %#v", result)
	}
}
