/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package api

import (
	"context"
	"encoding/base64"
	"net/http"
	"testing"
)

func TestDataPlaneURL(t *testing.T) {
	s := &Server{hubBase: "https://hub.example/"}

	got := s.dataPlaneURL(identity{clusterID: "root:faros:orgs:acme"}, dataPlaneRef{Resource: "applications", Name: "shop-dev"}, dataPlaneVerbLog, "")
	want := "https://hub.example/services/providers/infrastructure/dataplane/clusters/root:faros:orgs:acme/applications/shop-dev/log"
	if got != want {
		t.Fatalf("dataPlaneURL = %q, want %q", got, want)
	}

	// The open proxy verb appends the caller tail after the verb.
	gotProxy := s.dataPlaneURL(identity{clusterID: "c1"}, dataPlaneRef{Resource: "applications", Name: "r1"}, dataPlaneVerbProxy, "/assets/app.js")
	wantProxy := "https://hub.example/services/providers/infrastructure/dataplane/clusters/c1/applications/r1/proxy/assets/app.js"
	if gotProxy != wantProxy {
		t.Fatalf("proxy URL = %q, want %q", gotProxy, wantProxy)
	}

	// Component verbs address a template instance's component
	// (docs/app-studio-template-sandboxes.md §3).
	gotComp := s.dataPlaneURL(identity{clusterID: "c1"}, dataPlaneRef{Resource: "applications", Name: "shop-dev", Component: "backend"}, dataPlaneVerbSync, "")
	wantComp := "https://hub.example/services/providers/infrastructure/dataplane/clusters/c1/applications/shop-dev/components/backend/sync"
	if gotComp != wantComp {
		t.Fatalf("component URL = %q, want %q", gotComp, wantComp)
	}

	exportPath := "root:faros:tenants:org-a:providers:infrastructure"
	gotBound := s.dataPlaneURL(identity{clusterID: "c1", providerExportPath: exportPath}, dataPlaneRef{Resource: "instances", Name: "sandbox"}, dataPlaneVerbWorkspace, "")
	wantBound := "https://hub.example/services/providers/infrastructure/__bound/v1/" + base64.RawURLEncoding.EncodeToString([]byte(exportPath)) + "/dataplane/clusters/c1/instances/sandbox/workspace"
	if gotBound != wantBound {
		t.Fatalf("bound dataPlaneURL = %q, want %q", gotBound, wantBound)
	}
}

func TestNewDataPlaneRequestRequiresHubAndCluster(t *testing.T) {
	id := identity{clusterID: "c1", token: "tok"}
	ref := dataPlaneRef{Resource: "applications", Name: "r1"}
	// No hub base configured.
	if _, err := (&Server{}).newDataPlaneRequest(context.Background(), http.MethodGet, id, ref, dataPlaneVerbLog, "", nil); err == nil {
		t.Fatal("expected error when hubBase is unset")
	}
	// No cluster on the request.
	s := &Server{hubBase: "https://hub.example"}
	if _, err := s.newDataPlaneRequest(context.Background(), http.MethodGet, identity{token: "tok"}, ref, dataPlaneVerbLog, "", nil); err == nil {
		t.Fatal("expected error when clusterID is empty")
	}
	// Happy path forwards the caller's complete, server-verified identity.
	id = identity{
		token:         "tok",
		tenantPath:    "root:faros:tenants:org-a:workspace-a",
		orgUUID:       "org-a",
		workspaceUUID: "workspace-a",
		clusterID:     "cluster-a",
		user:          "alice",
	}
	req, err := s.newDataPlaneRequest(context.Background(), http.MethodGet, id, ref, dataPlaneVerbLog, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer tok" {
		t.Fatalf("Authorization = %q, want Bearer tok", got)
	}
	for _, tc := range []struct {
		name string
		want string
	}{
		{name: "X-Faros-Tenant", want: "root:faros:tenants:org-a:workspace-a"},
		{name: "X-Faros-Org", want: "org-a"},
		{name: "X-Faros-Workspace", want: "workspace-a"},
		{name: "X-Faros-Cluster", want: "cluster-a"},
		{name: "X-Faros-User", want: "alice"},
	} {
		if got := req.Header.Get(tc.name); got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, got, tc.want)
		}
	}

	// The route selector is sandbox-only; a generic identity remains on the
	// original provider path even though it carries the same cluster.
	if got := req.URL.Path; got != "/services/providers/infrastructure/dataplane/clusters/cluster-a/applications/r1/log" {
		t.Fatalf("generic data-plane path = %q, want unchanged route", got)
	}
	boundID := id
	boundID.providerExportPath = "root:faros:tenants:org-a:providers:infrastructure"
	boundReq, err := s.newDataPlaneRequest(context.Background(), http.MethodGet, boundID, ref, dataPlaneVerbLog, "", nil)
	if err != nil {
		t.Fatalf("bound request: %v", err)
	}
	wantBoundPath := "/services/providers/infrastructure/__bound/v1/" + base64.RawURLEncoding.EncodeToString([]byte(boundID.providerExportPath)) + "/dataplane/clusters/cluster-a/applications/r1/log"
	if got := boundReq.URL.Path; got != wantBoundPath {
		t.Fatalf("bound data-plane path = %q, want %q", got, wantBoundPath)
	}
}
