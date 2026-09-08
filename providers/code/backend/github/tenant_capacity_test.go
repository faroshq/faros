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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"

	"github.com/faroshq/provider-code/backend"
)

func TestTenantAdmissionIsolatesNoisyCredentials(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.Header.Get("Authorization"), "noisy-") {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message":"secondary rate limit"}`))
			return
		}
		_, _ = w.Write([]byte(`{"login":"alice","type":"User"}`))
	}))
	defer server.Close()
	b, clock := pollingBackend()
	a := pollingConnection(server.URL)
	a.Annotations = map[string]string{"kcp.io/cluster": "tenant-a"}
	var wg sync.WaitGroup
	// Distinct Connections and credentials in one tenant still share its quota.
	for i := 0; i < 2*maxTenantRequestStates; i++ {
		wg.Go(func() {
			conn := a.DeepCopy()
			conn.UID = types.UID(fmt.Sprint(i))
			conn.Name = fmt.Sprint(i)
			_, _, err := b.ValidateConnection(context.Background(), conn, backend.Credential{Token: fmt.Sprintf("noisy-%d", i)})
			if err == nil {
				t.Error("expected throttled or capacity failure")
			}
		})
	}
	wg.Wait()
	if got := calls.Load(); got != maxTenantRequestStates {
		t.Fatalf("noisy network admissions=%d want %d", got, maxTenantRequestStates)
	}
	// Package and workflow reads must not bypass the same admission boundary.
	_, err := b.ListPackages(context.Background(), a, backend.Credential{Token: "noisy-package"}, pollingRepo("demo"))
	if err == nil || !strings.Contains(err.Error(), "tenant request budget capacity") {
		t.Fatalf("package admission: %v", err)
	}
	_, err = b.LatestWorkflowRun(context.Background(), a, backend.Credential{Token: "noisy-workflow"}, pollingRepo("demo"), backend.WorkflowRunQuery{})
	if err == nil || !strings.Contains(err.Error(), "tenant request budget capacity") {
		t.Fatalf("workflow admission: %v", err)
	}
	other := a.DeepCopy()
	other.Annotations["kcp.io/cluster"] = "tenant-b"
	login, _, err := b.ValidateConnection(context.Background(), other, backend.Credential{Token: "healthy"})
	if err != nil || login != "alice" {
		t.Fatalf("noisy tenant blocked healthy tenant: %q %v", login, err)
	}
	healthyKey := requestIdentity{credentialHash("healthy"), server.URL}
	cache := b.requestCache()
	cache.mu.Lock()
	healthyState := cache.states[healthyKey]
	cache.mu.Unlock()
	if healthyState == nil {
		t.Fatal("missing healthy request state")
	}
	before := calls.Load()
	// Sharing the exact credential still shares its GitHub pause across tenants.
	var shared string
	cache.mu.Lock()
	for i := 0; i < 2*maxTenantRequestStates; i++ {
		token := fmt.Sprintf("noisy-%d", i)
		if cache.states[requestIdentity{credentialHash(token), server.URL}] != nil {
			shared = token
			break
		}
	}
	cache.mu.Unlock()
	_, _, err = b.ValidateConnection(context.Background(), other, backend.Credential{Token: shared})
	assertRetryAt(t, err, clock.now().Add(time.Minute))
	if calls.Load() != before {
		t.Fatal("shared credential bypassed throttle")
	}
	// Rotation after expiry replaces the same tenant's idle slot, preserving B.
	clock.advance(61 * time.Second)
	_, _, err = b.ValidateConnection(context.Background(), a, backend.Credential{Token: "rotated"})
	if err != nil {
		t.Fatalf("rotation after expiry: %v", err)
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.states[healthyKey] != healthyState {
		t.Fatal("rotation evicted another tenant's state")
	}
	if len(cache.states) != maxTenantRequestStates+1 {
		t.Fatalf("states=%d", len(cache.states))
	}
}
