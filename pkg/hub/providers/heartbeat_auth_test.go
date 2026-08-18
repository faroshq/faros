/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package providers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	authnv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

func TestHeartbeatAuthenticationRejectsBeforeMutation(t *testing.T) {
	reg := NewRegistry()
	reg.Upsert(Provider{Name: "cost", EndpointsValid: true})
	downstream := NewHeartbeatHandler(reg, nil, logr.Discard())

	tests := []struct {
		name   string
		header string
		auth   HeartbeatAuthenticator
		want   int
	}{
		{name: "missing", auth: func(context.Context, string, string) (bool, error) { return true, nil }, want: http.StatusUnauthorized},
		{name: "malformed", header: "Basic token", auth: func(context.Context, string, string) (bool, error) { return true, nil }, want: http.StatusUnauthorized},
		{name: "rejected", header: "Bearer invalid", auth: func(context.Context, string, string) (bool, error) { return false, nil }, want: http.StatusUnauthorized},
		{name: "review unavailable", header: "Bearer token", auth: func(context.Context, string, string) (bool, error) { return false, errors.New("kcp down") }, want: http.StatusServiceUnavailable},
		{name: "not configured", header: "Bearer token", want: http.StatusServiceUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := heartbeatRequestFor("cost")
			req.Header.Set("Authorization", test.header)
			RequireHeartbeatAuthentication(test.auth, downstream).ServeHTTP(rec, req)
			if rec.Code != test.want {
				t.Fatalf("status = %d, want %d", rec.Code, test.want)
			}
			if got, _ := reg.Get("cost"); got.HeartbeatRequired || !got.LastHeartbeat.IsZero() {
				t.Fatalf("rejected heartbeat mutated registry: %+v", got)
			}
		})
	}
}

func TestHeartbeatAuthenticationAllowsAuthenticatedBearer(t *testing.T) {
	var gotName, gotToken string
	auth := func(_ context.Context, name, token string) (bool, error) {
		gotName, gotToken = name, token
		return true, nil
	}
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})
	rec := httptest.NewRecorder()
	req := heartbeatRequestFor("cost")
	req.Header.Set("Authorization", "bearer valid-token")
	RequireHeartbeatAuthentication(auth, next).ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || !called || gotName != "cost" || gotToken != "valid-token" {
		t.Fatalf("status=%d called=%t name=%q token=%q", rec.Code, called, gotName, gotToken)
	}
}

type tokenReviewRoundTripper struct {
	wantToken     string
	authenticated bool
	username      string
	userUID       string
	serviceUID    string
}

func (rt tokenReviewRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	var value any
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/apis/authentication.k8s.io/v1/tokenreviews"):
		var review authnv1.TokenReview
		if err := json.NewDecoder(r.Body).Decode(&review); err != nil {
			return nil, err
		}
		if review.Spec.Token != rt.wantToken {
			return nil, errors.New("unexpected token")
		}
		value = &authnv1.TokenReview{
			TypeMeta: metav1.TypeMeta{APIVersion: "authentication.k8s.io/v1", Kind: "TokenReview"},
			Status: authnv1.TokenReviewStatus{
				Authenticated: rt.authenticated,
				User:          authnv1.UserInfo{Username: rt.username, UID: rt.userUID},
			},
		}
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/api/v1/namespaces/default/serviceaccounts/provider"):
		value = map[string]any{
			"apiVersion": "v1", "kind": "ServiceAccount",
			"metadata": map[string]any{"name": ProviderSAName, "namespace": ProviderSANamespace, "uid": rt.serviceUID},
		}
	default:
		return nil, errors.New("unexpected request: " + r.Method + " " + r.URL.Path)
	}
	response, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(string(response))),
		Request:    r,
	}, nil
}

func TestProviderHeartbeatAuthenticatorBindsServiceAccountUID(t *testing.T) {
	const token = "provider-token"
	reg := NewRegistry()
	reg.Upsert(Provider{Name: "cost", CatalogEntryCluster: "provider-cluster"})
	auth, err := NewProviderHeartbeatAuthenticator(&rest.Config{
		Host: "https://kcp.example.test",
		Transport: tokenReviewRoundTripper{
			wantToken: token, authenticated: true,
			username: "system:serviceaccount:default:provider", userUID: "provider-uid", serviceUID: "provider-uid",
		},
	}, reg)
	if err != nil {
		t.Fatalf("NewProviderHeartbeatAuthenticator: %v", err)
	}
	ok, err := auth(context.Background(), "cost", token)
	if err != nil || !ok {
		t.Fatalf("authenticated=%t err=%v, want true", ok, err)
	}
}

func TestProviderHeartbeatAuthenticatorRejectsOtherIdentity(t *testing.T) {
	const token = "other-token"
	reg := NewRegistry()
	reg.Upsert(Provider{Name: "cost", CatalogEntryCluster: "provider-cluster"})
	for _, test := range []struct {
		name, username, userUID, serviceUID string
	}{
		{name: "human", username: "alice", userUID: "alice-uid", serviceUID: "provider-uid"},
		{name: "same name different workspace", username: "system:serviceaccount:default:provider", userUID: "other-uid", serviceUID: "provider-uid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			auth, err := NewProviderHeartbeatAuthenticator(&rest.Config{
				Host: "https://kcp.example.test",
				Transport: tokenReviewRoundTripper{
					wantToken: token, authenticated: true,
					username: test.username, userUID: test.userUID, serviceUID: test.serviceUID,
				},
			}, reg)
			if err != nil {
				t.Fatal(err)
			}
			ok, err := auth(context.Background(), "cost", token)
			if err != nil || ok {
				t.Fatalf("authenticated=%t err=%v, want false", ok, err)
			}
		})
	}
}

func TestHeartbeatStaticTokenFallback(t *testing.T) {
	called := false
	strict := func(context.Context, string, string) (bool, error) {
		called = true
		return false, nil
	}
	auth := WithHeartbeatStaticTokenFallback(strict, []string{"dev-token"})
	if ok, err := auth(context.Background(), "cost", "dev-token"); err != nil || !ok || called {
		t.Fatalf("dev fallback: authenticated=%t calledStrict=%t err=%v", ok, called, err)
	}
	if ok, err := auth(context.Background(), "cost", "other-token"); err != nil || ok || !called {
		t.Fatalf("strict fallback: authenticated=%t calledStrict=%t err=%v", ok, called, err)
	}
}
