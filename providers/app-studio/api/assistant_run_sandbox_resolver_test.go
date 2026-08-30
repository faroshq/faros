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

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/faroshq/provider-app-studio/workspace"
)

func TestProductionCodingSandboxResolverOwnershipMatrix(t *testing.T) {
	const (
		org = "org-a"
		ws  = "workspace-a"
	)
	platformAppStudio := enabledProviderBinding{
		BindingName: "app-studio", ExportPath: projectAssistantPlatformAppStudioExportPath,
	}
	platformInfrastructure := enabledProviderBinding{
		BindingName: "infrastructure", ExportPath: projectAssistantPlatformInfrastructureExportPath,
	}
	orgAppStudio := enabledProviderBinding{
		BindingName: "app-studio", ExportPath: "root:faros:tenants:" + org + ":providers:app-studio", SelfHosted: true,
	}
	orgInfrastructure := enabledProviderBinding{
		BindingName: "infrastructure", ExportPath: "root:faros:tenants:" + org + ":providers:infrastructure", SelfHosted: true,
	}

	tests := []struct {
		name           string
		appStudio      enabledProviderBinding
		infrastructure enabledProviderBinding
		wantEligible   bool
		wantExport     string
		wantReason     string
	}{
		{
			name: "SaaS pair", appStudio: platformAppStudio, infrastructure: platformInfrastructure,
			wantEligible: true, wantExport: projectAssistantPlatformInfrastructureExportPath,
		},
		{
			name: "BYO pair", appStudio: orgAppStudio, infrastructure: orgInfrastructure,
			wantEligible: true, wantExport: orgInfrastructure.ExportPath,
		},
		{
			name: "SaaS App Studio with BYO Infrastructure", appStudio: platformAppStudio, infrastructure: orgInfrastructure,
			wantReason: "mixed platform and self-hosted ownership",
		},
		{
			name: "BYO App Studio with SaaS Infrastructure", appStudio: orgAppStudio, infrastructure: platformInfrastructure,
			wantReason: "mixed platform and self-hosted ownership",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newCodingSandboxBindingServer(t, org, ws, map[string]enabledProviderBinding{
				"app-studio": tt.appStudio, "infrastructure": tt.infrastructure,
			})
			eligibility := server.ResolveCodingSandboxEligibility(
				context.Background(),
				testCodingSandboxIdentity(org, ws),
				workspace.Scope{OrgUUID: org, WorkspaceUUID: ws},
			)
			if eligibility.Eligible != tt.wantEligible {
				t.Fatalf("eligibility = %#v, want eligible=%t", eligibility, tt.wantEligible)
			}
			if eligibility.ProviderExportPath != tt.wantExport {
				t.Fatalf("provider export = %q, want %q", eligibility.ProviderExportPath, tt.wantExport)
			}
			if tt.wantEligible && eligibility.TransportGeneration != projectAssistantSandboxTransportGeneration {
				t.Fatalf("transport generation = %q", eligibility.TransportGeneration)
			}
			if tt.wantReason != "" && !strings.Contains(eligibility.Reason, tt.wantReason) {
				t.Fatalf("reason = %q, want substring %q", eligibility.Reason, tt.wantReason)
			}
		})
	}
}

func TestProductionCodingSandboxResolverHandlesBindingHealth(t *testing.T) {
	const (
		org = "org-a"
		ws  = "workspace-a"
	)
	platformAppStudio := enabledProviderBinding{BindingName: "app-studio", ExportPath: projectAssistantPlatformAppStudioExportPath}
	platformInfrastructure := enabledProviderBinding{BindingName: "infrastructure", ExportPath: projectAssistantPlatformInfrastructureExportPath}

	tests := []struct {
		name       string
		bindings   map[string]enabledProviderBinding
		wantReason string
	}{
		{
			name: "missing App Studio", bindings: map[string]enabledProviderBinding{"infrastructure": platformInfrastructure},
			wantReason: "App Studio is not enabled",
		},
		{
			name: "missing Infrastructure", bindings: map[string]enabledProviderBinding{"app-studio": platformAppStudio},
			wantReason: "Infrastructure is not enabled",
		},
		{
			name: "terminating binding", bindings: map[string]enabledProviderBinding{
				"app-studio":     platformAppStudio,
				"infrastructure": func() enabledProviderBinding { b := platformInfrastructure; b.Terminating = true; return b }(),
			},
			wantReason: "being disabled",
		},
		{
			name: "foreign Org export", bindings: map[string]enabledProviderBinding{
				"app-studio":     {BindingName: "app-studio", ExportPath: "root:faros:tenants:org-b:providers:app-studio", SelfHosted: true},
				"infrastructure": {BindingName: "infrastructure", ExportPath: "root:faros:tenants:org-b:providers:infrastructure", SelfHosted: true},
			},
			wantReason: "unexpected provider export",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newCodingSandboxBindingServer(t, org, ws, tt.bindings)
			eligibility := server.ResolveCodingSandboxEligibility(
				context.Background(), testCodingSandboxIdentity(org, ws), workspace.Scope{OrgUUID: org, WorkspaceUUID: ws},
			)
			wantEligible := tt.wantReason == ""
			if eligibility.Eligible != wantEligible || (!wantEligible && !strings.Contains(eligibility.Reason, tt.wantReason)) {
				t.Fatalf("eligibility = %#v, want eligible=%t reason containing %q", eligibility, wantEligible, tt.wantReason)
			}
		})
	}
}

func TestProductionCodingSandboxResolverRejectsIdentityScopeMismatchBeforeHubCall(t *testing.T) {
	calls := 0
	hub := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	t.Cleanup(hub.Close)
	server := &Server{hubBase: hub.URL}
	server.ConfigureCodingSandbox(CodingSandboxConfig{Mode: CodingSandboxModeOn, ReplicaCount: 1})
	server.ConfigureCodingSandboxResolver()

	eligibility := server.ResolveCodingSandboxEligibility(
		context.Background(),
		testCodingSandboxIdentity("org-a", "workspace-a"),
		workspace.Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-b"},
	)
	if eligibility.Eligible || !strings.Contains(eligibility.Reason, "caller identity does not match") {
		t.Fatalf("eligibility = %#v", eligibility)
	}
	if calls != 0 {
		t.Fatalf("hub calls = %d, want zero", calls)
	}
}

func newCodingSandboxBindingServer(t *testing.T, org, ws string, bindings map[string]enabledProviderBinding) *Server {
	t.Helper()
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/orgs/"+org+"/workspaces/"+ws+"/providers/enabled" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer caller-token" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-Faros-Tenant"); got != "root:faros:tenants:"+org+":"+ws {
			t.Errorf("X-Faros-Tenant = %q", got)
		}
		if got := r.Header.Get("X-Faros-Org"); got != org {
			t.Errorf("X-Faros-Org = %q", got)
		}
		if got := r.Header.Get("X-Faros-Workspace"); got != ws {
			t.Errorf("X-Faros-Workspace = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(enabledProviderBindingsResponse{BindingsByProvider: bindings}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	t.Cleanup(hub.Close)

	server := &Server{hubBase: hub.URL}
	server.ConfigureCodingSandbox(CodingSandboxConfig{Mode: CodingSandboxModeOn, ReplicaCount: 1})
	server.ConfigureCodingSandboxResolver()
	return server
}

func testCodingSandboxIdentity(org, ws string) identity {
	return identity{
		orgUUID: org, workspaceUUID: ws, token: "caller-token",
		tenantPath: "root:faros:tenants:" + org + ":" + ws,
		clusterID:  "logical-cluster", user: "alice",
	}
}
