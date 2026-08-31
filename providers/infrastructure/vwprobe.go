// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

// Virtual-workspace reachability, reported as readiness.
//
// The provider watches every workspace that consumes its APIExport through the
// URL kcp publishes in APIExportEndpointSlice.status.endpoints[]. That URL comes
// from Shard.spec.virtualWorkspaceURL, which is a property of the PLATFORM, not
// of this provider — so a provider running somewhere the platform did not
// anticipate (a tenant's own cluster, a kind pod) can find it unreachable
// through no fault of its own.
//
// The failure is silent, and that is the point of this file. Templates live in
// the provider's own workspace and are reached over the provider kubeconfig — a
// different URL that keeps working — so the provider starts, serves its API,
// wins leader election, reconciles Templates, and simply never acts on a tenant
// Instance. Health checks pass throughout. On a real deployment that went
// unnoticed for days.
//
// Probing the endpoint directly, rather than inspecting the multicluster
// manager, keeps this honest about the thing that actually breaks: whether this
// process can reach that URL. It also survives library changes — the manager
// logs its watch failures and does not surface them.

var apiExportEndpointSliceGVR = schema.GroupVersionResource{
	Group: "apis.kcp.io", Version: "v1alpha1", Resource: "apiexportendpointslices",
}

// vwReadiness holds the last probe result. Safe for concurrent use: the prober
// writes, the HTTP handler reads.
type vwReadiness struct {
	mu      sync.RWMutex
	checked bool
	url     string
	err     error
}

func (r *vwReadiness) set(url string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checked, r.url, r.err = true, url, err
}

// Check reports why the provider is not ready, or nil.
//
// Before the first probe completes it reports ready. Readiness gates traffic,
// and a provider that has not finished starting must not be marked broken —
// the interesting state is a probe that ran and failed.
func (r *vwReadiness) Check() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.checked || r.err == nil {
		return nil
	}
	return fmt.Errorf("cannot reach the APIExport virtual workspace at %s: %w — "+
		"tenant Instances will not reconcile. The address comes from the platform's "+
		"Shard.spec.virtualWorkspaceURL; it must be reachable from where this provider runs", r.url, r.err)
}

// watchVirtualWorkspaceReachability probes the endpoint every interval until ctx
// ends. Never fatal: an unreachable virtual workspace is a condition to report,
// not a reason to stop serving the API and the Template controller, both of
// which keep working without it.
func watchVirtualWorkspaceReachability(ctx context.Context, cfg *rest.Config, exportName string, state *vwReadiness, interval time.Duration) {
	if cfg == nil || state == nil {
		return
	}
	for {
		url, err := probeVirtualWorkspace(ctx, cfg, exportName)
		state.set(url, err)
		if err != nil {
			// Default verbosity: this is the signal that a self-hosted provider
			// is half-working, and it should be greppable in a support bundle.
			logVWUnreachable(url, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// probeVirtualWorkspace resolves this provider's advertised virtual-workspace
// URL and checks that this process can actually reach it.
func probeVirtualWorkspace(ctx context.Context, cfg *rest.Config, exportName string) (string, error) {
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return "", fmt.Errorf("building client: %w", err)
	}
	slice, err := dyn.Resource(apiExportEndpointSliceGVR).Get(ctx, exportName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("reading APIExportEndpointSlice %s: %w", exportName, err)
	}
	urls, _, _ := unstructured.NestedSlice(slice.Object, "status", "endpoints")
	if len(urls) == 0 {
		return "", fmt.Errorf("APIExportEndpointSlice %s publishes no endpoints yet", exportName)
	}
	first, _ := urls[0].(map[string]any)
	url, _ := first["url"].(string)
	if url == "" {
		return "", fmt.Errorf("APIExportEndpointSlice %s publishes an empty endpoint URL", exportName)
	}

	// Discovery on the wildcard path — the same shape the multicluster manager
	// opens, so reachability here means reachability for it.
	probeURL := strings.TrimSuffix(url, "/") + "/clusters/*/api"
	// rest.TransportFor carries both the TLS config and the credentials, so the
	// probe fails for the same reasons the manager would and no others.
	rt, err := rest.TransportFor(cfg)
	if err != nil {
		return url, fmt.Errorf("building probe transport: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return url, fmt.Errorf("building probe request: %w", err)
	}
	client := &http.Client{Transport: rt, Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		// DNS failure, connection refused, TLS mismatch — the whole class this
		// exists to catch.
		return url, err
	}
	defer func() { _ = resp.Body.Close() }()
	// Any answer at all means the address resolves and something is serving it.
	// 401/403 would mean credentials, which is a different problem and not this
	// probe's business to judge.
	if resp.StatusCode >= 500 {
		return url, fmt.Errorf("endpoint returned %s", resp.Status)
	}
	return url, nil
}

// logVWUnreachable reports the condition once per probe. Kept as a seam so the
// test can exercise the prober without writing to the process log.
var logVWUnreachable = func(url string, err error) {
	log.Printf("virtual workspace unreachable at %s: %v — tenant Instances will not reconcile", url, err)
}
