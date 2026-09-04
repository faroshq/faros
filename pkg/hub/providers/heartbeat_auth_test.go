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
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	authnv1 "k8s.io/api/authentication/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func heartbeatRequestWithBearer(name, token string) *http.Request {
	req := heartbeatRequestFor(name)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

// The endpoint has no auth middleware in front of it, so a beat with no bearer
// must be turned away before it touches the registry: otherwise anyone who can
// reach the hub keeps a dead provider Ready.
func TestHeartbeatEnforceRejectsMissingBearerAndLeavesRegistryUntouched(t *testing.T) {
	reg := NewRegistry()
	reg.Upsert(Provider{Name: "code", EndpointsValid: true})

	persisted := false
	handler := NewHeartbeatHandler(reg, func(context.Context, string, string, time.Time) error {
		persisted = true
		return nil
	}, func(_ context.Context, r *http.Request, _ string) error {
		if _, ok := bearerToken(r); !ok {
			return ErrHeartbeatNoBearer
		}
		return nil
	}, HeartbeatAuthEnforce, logr.Discard())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, heartbeatRequestFor("code"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if persisted {
		t.Fatal("unauthenticated beat was persisted")
	}
	got, _ := reg.Get("code")
	if got.HeartbeatRequired || !got.LastHeartbeat.IsZero() {
		t.Fatalf("registry moved on an unauthenticated beat: %+v", got)
	}
}

func TestHeartbeatEnforceRejectsWrongIdentityWith403(t *testing.T) {
	reg := NewRegistry()
	reg.Upsert(Provider{Name: "code", EndpointsValid: true})

	handler := NewHeartbeatHandler(reg, nil, func(context.Context, *http.Request, string) error {
		return fmt.Errorf("%w: authenticated as %q", ErrHeartbeatWrongIdentity, "system:serviceaccount:default:someone-else")
	}, HeartbeatAuthEnforce, logr.Discard())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, heartbeatRequestWithBearer("code", "tok"))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if got, _ := reg.Get("code"); got.HeartbeatRequired {
		t.Fatal("registry recorded a beat from the wrong identity")
	}
}

// A verification outage is not an identity failure: the provider should retry,
// not be told its token is bad.
func TestHeartbeatEnforceReports503WhenItCannotVerify(t *testing.T) {
	reg := NewRegistry()
	reg.Upsert(Provider{Name: "code", EndpointsValid: true})

	handler := NewHeartbeatHandler(reg, nil, func(context.Context, *http.Request, string) error {
		return fmt.Errorf("%w: kcp down", ErrHeartbeatAuthUnavailable)
	}, HeartbeatAuthEnforce, logr.Discard())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, heartbeatRequestWithBearer("code", "tok"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}

	// With no authenticator at all (no kcp) enforce mode must fail closed the
	// same way rather than silently accepting everything.
	handler = NewHeartbeatHandler(reg, nil, nil, HeartbeatAuthEnforce, logr.Discard())
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, heartbeatRequestWithBearer("code", "tok"))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("nil authenticator: status = %d, want 503", rec.Code)
	}
}

func TestHeartbeatEnforceRecordsTheProvidersOwnIdentity(t *testing.T) {
	reg := NewRegistry()
	reg.Upsert(Provider{Name: "code", EndpointsValid: true})

	var seen []string
	handler := NewHeartbeatHandler(reg, nil, func(_ context.Context, r *http.Request, name string) error {
		token, _ := bearerToken(r)
		seen = append(seen, name+":"+token)
		return nil
	}, HeartbeatAuthEnforce, logr.Discard())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, heartbeatRequestWithBearer("code", "sa-token"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if len(seen) != 1 || seen[0] != "code:sa-token" {
		t.Fatalf("authenticator saw %v, want the provider name and bearer from the request", seen)
	}
	if got, _ := reg.Get("code"); !got.HeartbeatRequired || got.LastHeartbeat.IsZero() {
		t.Fatalf("authenticated beat not recorded: %+v", got)
	}
}

// Warn mode is the bridge for providers on charts that predate the
// authenticated heartbeat: the beat still counts, but the failure is logged at
// the default verbosity with the provider name so operators can find them.
func TestHeartbeatWarnRecordsAndLogsFailures(t *testing.T) {
	reg := NewRegistry()
	reg.Upsert(Provider{Name: "code", EndpointsValid: true})

	var logged []string
	log := funcr.New(func(prefix, args string) { logged = append(logged, prefix+" "+args) }, funcr.Options{})

	handler := NewHeartbeatHandler(reg, nil, func(context.Context, *http.Request, string) error {
		return ErrHeartbeatNoBearer
	}, HeartbeatAuthWarn, log)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, heartbeatRequestFor("code"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 in warn mode", rec.Code)
	}
	if got, _ := reg.Get("code"); !got.HeartbeatRequired {
		t.Fatal("warn mode dropped the beat")
	}
	var found bool
	for _, line := range logged {
		if strings.Contains(line, `"provider"="code"`) && strings.Contains(line, ErrHeartbeatNoBearer.Error()) && strings.Contains(line, "warn") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no warn-mode log line naming the provider and reason; got %q", logged)
	}
}

func TestParseHeartbeatAuthMode(t *testing.T) {
	for in, want := range map[string]HeartbeatAuthMode{"warn": HeartbeatAuthWarn, " Enforce ": HeartbeatAuthEnforce} {
		got, err := ParseHeartbeatAuthMode(in)
		if err != nil || got != want {
			t.Fatalf("ParseHeartbeatAuthMode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "off", "audit"} {
		if _, err := ParseHeartbeatAuthMode(in); err == nil {
			t.Fatalf("ParseHeartbeatAuthMode(%q) accepted", in)
		}
	}
}

// tokenReviewFake returns a fake clientset whose TokenReview answers with the
// given status, counting the reviews it served and recording which cluster it
// was asked to serve.
type tokenReviewFake struct {
	reviews  int
	clusters []string
	status   authnv1.TokenReviewStatus
}

func (f *tokenReviewFake) newClient(cluster string) (kubernetes.Interface, error) {
	f.clusters = append(f.clusters, cluster)
	cs := fake.NewClientset()
	cs.PrependReactor("create", "tokenreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		f.reviews++
		in := action.(k8stesting.CreateAction).GetObject().(*authnv1.TokenReview)
		if len(in.Spec.Audiences) != 0 {
			// The provider kubeconfig token is a legacy Secret token with no
			// audience claim; asking kcp for a specific audience would reject it.
			return true, nil, fmt.Errorf("unexpected audiences %v requested", in.Spec.Audiences)
		}
		out := in.DeepCopy()
		out.Status = f.status
		return true, out, nil
	})
	return cs, nil
}

func newTestTokenReviewAuthenticator(reg *Registry, f *tokenReviewFake, now *time.Time) *tokenReviewAuthenticator {
	return &tokenReviewAuthenticator{
		clusters:  reg,
		newClient: f.newClient,
		now:       func() time.Time { return *now },
		cacheTTL:  heartbeatAuthCacheTTL,
		cache:     map[heartbeatAuthCacheKey]time.Time{},
	}
}

func TestTokenReviewAuthenticatorRejectsAuthenticatedWrongUser(t *testing.T) {
	reg := NewRegistry()
	reg.Upsert(Provider{Name: "code", EndpointsValid: true, CatalogEntryCluster: "code-cluster"})
	f := &tokenReviewFake{status: authnv1.TokenReviewStatus{
		Authenticated: true, User: authnv1.UserInfo{Username: "system:serviceaccount:default:not-the-provider"},
	}}
	now := time.Now()
	a := newTestTokenReviewAuthenticator(reg, f, &now)

	err := a.Authenticate(context.Background(), heartbeatRequestWithBearer("code", "tok"), "code")
	if !errors.Is(err, ErrHeartbeatWrongIdentity) {
		t.Fatalf("err = %v, want ErrHeartbeatWrongIdentity", err)
	}
	if heartbeatAuthStatus(err) != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", heartbeatAuthStatus(err))
	}
	// A failure is never cached: the next beat is reviewed again.
	if err := a.Authenticate(context.Background(), heartbeatRequestWithBearer("code", "tok"), "code"); !errors.Is(err, ErrHeartbeatWrongIdentity) {
		t.Fatalf("second err = %v, want ErrHeartbeatWrongIdentity", err)
	}
	if f.reviews != 2 {
		t.Fatalf("reviews = %d, want 2 (failures are not cached)", f.reviews)
	}
}

func TestTokenReviewAuthenticatorRejectsUnauthenticatedToken(t *testing.T) {
	reg := NewRegistry()
	reg.Upsert(Provider{Name: "code", EndpointsValid: true, CatalogEntryCluster: "code-cluster"})
	f := &tokenReviewFake{status: authnv1.TokenReviewStatus{Authenticated: false}}
	now := time.Now()
	a := newTestTokenReviewAuthenticator(reg, f, &now)

	err := a.Authenticate(context.Background(), heartbeatRequestWithBearer("code", "garbage"), "code")
	if !errors.Is(err, ErrHeartbeatTokenRejected) || heartbeatAuthStatus(err) != http.StatusUnauthorized {
		t.Fatalf("err = %v (status %d), want ErrHeartbeatTokenRejected/401", err, heartbeatAuthStatus(err))
	}
	if err := a.Authenticate(context.Background(), heartbeatRequestFor("code"), "code"); !errors.Is(err, ErrHeartbeatNoBearer) {
		t.Fatalf("no bearer: err = %v, want ErrHeartbeatNoBearer", err)
	}
	if f.reviews != 1 {
		t.Fatalf("reviews = %d, want 1 (a missing bearer never reaches kcp)", f.reviews)
	}
}

func TestTokenReviewAuthenticatorAcceptsProviderSAAndCachesIt(t *testing.T) {
	reg := NewRegistry()
	reg.Upsert(Provider{Name: "code", EndpointsValid: true, CatalogEntryCluster: "code-cluster"})
	f := &tokenReviewFake{status: authnv1.TokenReviewStatus{
		Authenticated: true, User: authnv1.UserInfo{Username: ProviderSAUsername},
	}}
	now := time.Now()
	a := newTestTokenReviewAuthenticator(reg, f, &now)

	for i := range 3 {
		if err := a.Authenticate(context.Background(), heartbeatRequestWithBearer("code", "tok"), "code"); err != nil {
			t.Fatalf("beat %d: %v", i, err)
		}
	}
	if f.reviews != 1 {
		t.Fatalf("reviews = %d, want 1 (cache hit for the repeated token)", f.reviews)
	}
	if len(f.clusters) != 1 || f.clusters[0] != "code-cluster" {
		t.Fatalf("reviewed in clusters %v, want only the provider's own", f.clusters)
	}

	// A different token for the same provider is its own review.
	if err := a.Authenticate(context.Background(), heartbeatRequestWithBearer("code", "other"), "code"); err != nil {
		t.Fatal(err)
	}
	if f.reviews != 2 {
		t.Fatalf("reviews = %d, want 2 (different token is not a cache hit)", f.reviews)
	}

	// The cache must lapse well before the liveness window does, so a
	// revoked token cannot keep a provider Ready indefinitely.
	if heartbeatAuthCacheTTL >= HeartbeatTTL {
		t.Fatalf("cache TTL %v must be shorter than heartbeat TTL %v", heartbeatAuthCacheTTL, HeartbeatTTL)
	}
	now = now.Add(heartbeatAuthCacheTTL + time.Second)
	if err := a.Authenticate(context.Background(), heartbeatRequestWithBearer("code", "tok"), "code"); err != nil {
		t.Fatal(err)
	}
	if f.reviews != 3 {
		t.Fatalf("reviews = %d, want 3 (expired entry is re-reviewed)", f.reviews)
	}
}

func TestTokenReviewAuthenticatorNeedsTheProviderCluster(t *testing.T) {
	reg := NewRegistry()
	reg.Upsert(Provider{Name: "code", EndpointsValid: true})
	f := &tokenReviewFake{}
	now := time.Now()
	a := newTestTokenReviewAuthenticator(reg, f, &now)

	err := a.Authenticate(context.Background(), heartbeatRequestWithBearer("code", "tok"), "code")
	if !errors.Is(err, ErrHeartbeatAuthUnavailable) || heartbeatAuthStatus(err) != http.StatusServiceUnavailable {
		t.Fatalf("err = %v, want ErrHeartbeatAuthUnavailable/503", err)
	}
	if f.reviews != 0 {
		t.Fatal("reviewed a token without knowing which cluster to ask")
	}
}

func TestNewTokenReviewHeartbeatAuthenticatorRequiresInputs(t *testing.T) {
	if _, err := NewTokenReviewHeartbeatAuthenticator(nil, NewRegistry()); err == nil {
		t.Fatal("nil kcp config accepted")
	}
}

func TestBearerToken(t *testing.T) {
	for header, want := range map[string]string{
		"Bearer abc":  "abc",
		"bearer abc ": "abc",
		"Basic abc":   "",
		"Bearer":      "",
		"Bearer  ":    "",
		"":            "",
	} {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		got, ok := bearerToken(req)
		if got != want || ok != (want != "") {
			t.Errorf("bearerToken(%q) = %q, %t; want %q", header, got, ok, want)
		}
	}
}
