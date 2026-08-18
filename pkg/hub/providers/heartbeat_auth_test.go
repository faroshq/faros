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
		{name: "missing", auth: func(context.Context, string) (bool, error) { return true, nil }, want: http.StatusUnauthorized},
		{name: "malformed", header: "Basic token", auth: func(context.Context, string) (bool, error) { return true, nil }, want: http.StatusUnauthorized},
		{name: "rejected", header: "Bearer invalid", auth: func(context.Context, string) (bool, error) { return false, nil }, want: http.StatusUnauthorized},
		{name: "review unavailable", header: "Bearer token", auth: func(context.Context, string) (bool, error) { return false, errors.New("kcp down") }, want: http.StatusServiceUnavailable},
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
	var gotToken string
	auth := func(_ context.Context, token string) (bool, error) {
		gotToken = token
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
	if rec.Code != http.StatusNoContent || !called || gotToken != "valid-token" {
		t.Fatalf("status=%d called=%t token=%q", rec.Code, called, gotToken)
	}
}

type tokenReviewRoundTripper struct {
	wantToken     string
	authenticated bool
}

func (rt tokenReviewRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodPost || r.URL.Path != "/apis/authentication.k8s.io/v1/tokenreviews" {
		return nil, errors.New("unexpected request")
	}
	var review authnv1.TokenReview
	if err := json.NewDecoder(r.Body).Decode(&review); err != nil {
		return nil, err
	}
	if review.Spec.Token != rt.wantToken {
		return nil, errors.New("unexpected token")
	}
	response, err := json.Marshal(&authnv1.TokenReview{
		TypeMeta: metav1.TypeMeta{APIVersion: "authentication.k8s.io/v1", Kind: "TokenReview"},
		Status: authnv1.TokenReviewStatus{
			Authenticated: rt.authenticated,
			User:          authnv1.UserInfo{Username: "provider-runtime"},
		},
	})
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

func TestTokenReviewHeartbeatAuthenticator(t *testing.T) {
	const token = "provider-token"
	auth, err := NewTokenReviewHeartbeatAuthenticator(&rest.Config{
		Host:      "https://kcp.example.test",
		Transport: tokenReviewRoundTripper{wantToken: token, authenticated: true},
	})
	if err != nil {
		t.Fatalf("NewTokenReviewHeartbeatAuthenticator: %v", err)
	}
	ok, err := auth(context.Background(), token)
	if err != nil || !ok {
		t.Fatalf("authenticated=%t err=%v, want true", ok, err)
	}
}
