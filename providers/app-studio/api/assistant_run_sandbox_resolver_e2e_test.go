//go:build e2e

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
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/faroshq/provider-app-studio/workspace"
)

var codingSandboxE2EAPIBindingGVR = schema.GroupVersionResource{
	Group: "apis.kcp.io", Version: "v1alpha2", Resource: "apibindings",
}

// TestCodingSandboxBindingMatrixE2E proves the production resolver against a
// real kcp tenant workspace and the hub's real /providers/enabled endpoint.
// The harness supplies API-only platform and same-Org provider exports; this
// test owns only the two APIBindings it replaces between matrix rows.
func TestCodingSandboxBindingMatrixE2E(t *testing.T) {
	hubURL := requireSandboxE2EEnv(t, "FAROS_E2E_HUB_URL")
	token := requireSandboxE2EEnv(t, "FAROS_E2E_STATIC_TOKEN")
	org := requireSandboxE2EEnv(t, "FAROS_E2E_ORG_UUID")
	ws := requireSandboxE2EEnv(t, "FAROS_E2E_WORKSPACE_UUID")
	cluster := requireSandboxE2EEnv(t, "FAROS_E2E_WORKSPACE_CLUSTER")
	kubeconfig := requireSandboxE2EEnv(t, "FAROS_E2E_KCP_KUBECONFIG")

	tenant := sandboxE2ETenantClient(t, kubeconfig, "root:faros:tenants:"+org+":"+ws)
	requireSandboxE2EBindingsAbsent(t, tenant)
	t.Cleanup(func() { replaceSandboxE2EBindings(t, tenant, nil) })
	scope := workspace.Scope{OrgUUID: org, WorkspaceUUID: ws, ProjectName: "matrix", ProjectUID: "matrix"}
	id := identity{
		orgUUID: org, workspaceUUID: ws, clusterID: cluster, token: token, user: "e2e",
		tenantPath: "root:faros:tenants:" + org + ":" + ws,
	}

	platform := map[string]sandboxE2EExport{
		"app-studio":     {Path: projectAssistantPlatformAppStudioExportPath, Name: "ai.faros.sh"},
		"infrastructure": {Path: projectAssistantPlatformInfrastructureExportPath, Name: "infrastructure.providers.faros.sh"},
	}
	orgOwned := map[string]sandboxE2EExport{
		"app-studio":     {Path: "root:faros:tenants:" + org + ":providers:app-studio", Name: "ai.faros.sh"},
		"infrastructure": {Path: "root:faros:tenants:" + org + ":providers:infrastructure", Name: "infrastructure.providers.faros.sh"},
	}

	tests := []struct {
		name           string
		mode           CodingSandboxMode
		appStudio      sandboxE2EExport
		infrastructure sandboxE2EExport
		wantEligible   bool
		wantExport     string
		wantReason     string
	}{
		{
			name: "SaaS mode stays on dev image", mode: CodingSandboxModeOff,
			appStudio: platform["app-studio"], infrastructure: platform["infrastructure"],
			wantReason: "mode is off",
		},
		{
			name: "future SaaS opt-in", mode: CodingSandboxModeOn,
			appStudio: platform["app-studio"], infrastructure: platform["infrastructure"],
			wantEligible: true, wantExport: projectAssistantPlatformInfrastructureExportPath,
		},
		{
			name: "BYO mode", mode: CodingSandboxModeOn,
			appStudio: orgOwned["app-studio"], infrastructure: orgOwned["infrastructure"],
			wantEligible: true, wantExport: orgOwned["infrastructure"].Path,
		},
		{
			name: "hybrid SaaS App Studio BYO Infrastructure", mode: CodingSandboxModeOn,
			appStudio: platform["app-studio"], infrastructure: orgOwned["infrastructure"],
			wantReason: "mixed platform and self-hosted ownership",
		},
		{
			name: "hybrid BYO App Studio SaaS Infrastructure", mode: CodingSandboxModeOn,
			appStudio: orgOwned["app-studio"], infrastructure: platform["infrastructure"],
			wantReason: "mixed platform and self-hosted ownership",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			replaceSandboxE2EBindings(t, tenant, map[string]sandboxE2EExport{
				"app-studio": tt.appStudio, "infrastructure": tt.infrastructure,
			})
			server := &Server{hubBase: hubURL, mcpInsecureSkipTLSVerify: true}
			server.ConfigureCodingSandbox(CodingSandboxConfig{Mode: tt.mode, ReplicaCount: 1})
			server.ConfigureCodingSandboxResolver()
			eligibility := server.ResolveCodingSandboxEligibility(context.Background(), id, scope)
			if eligibility.Eligible != tt.wantEligible {
				t.Fatalf("eligibility = %#v, want eligible=%t", eligibility, tt.wantEligible)
			}
			if eligibility.ProviderExportPath != tt.wantExport {
				t.Fatalf("provider export = %q, want %q", eligibility.ProviderExportPath, tt.wantExport)
			}
			if tt.wantReason != "" && !strings.Contains(eligibility.Reason, tt.wantReason) {
				t.Fatalf("reason = %q, want substring %q", eligibility.Reason, tt.wantReason)
			}
		})
	}
}

type sandboxE2EExport struct {
	Path string
	Name string
}

func requireSandboxE2EEnv(t *testing.T, key string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		t.Skip(key + " is required for the live sandbox binding matrix")
	}
	return value
}

func sandboxE2ETenantClient(t *testing.T, kubeconfig, workspacePath string) dynamic.Interface {
	t.Helper()
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatalf("load kcp kubeconfig: %v", err)
	}
	server, err := url.Parse(config.Host)
	if err != nil {
		t.Fatalf("parse kcp host: %v", err)
	}
	server.Path = "/clusters/" + workspacePath
	config.Host = server.String()
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		t.Fatalf("new tenant client: %v", err)
	}
	return client
}

func requireSandboxE2EBindingsAbsent(t *testing.T, tenant dynamic.Interface) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resource := tenant.Resource(codingSandboxE2EAPIBindingGVR)
	for _, name := range []string{"app-studio", "infrastructure"} {
		_, err := resource.Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			t.Fatalf("refusing to replace pre-existing APIBinding %s; use an empty, test-owned workspace", name)
		}
		if !apierrors.IsNotFound(err) {
			t.Fatalf("check APIBinding %s precondition: %v", name, err)
		}
	}
}

func replaceSandboxE2EBindings(t *testing.T, tenant dynamic.Interface, exports map[string]sandboxE2EExport) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	resource := tenant.Resource(codingSandboxE2EAPIBindingGVR)
	for _, name := range []string{"app-studio", "infrastructure"} {
		err := resource.Delete(ctx, name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			t.Fatalf("delete APIBinding %s: %v", name, err)
		}
	}
	for _, name := range []string{"app-studio", "infrastructure"} {
		for {
			_, err := resource.Get(ctx, name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				break
			}
			if err != nil {
				t.Fatalf("wait for APIBinding %s deletion: %v", name, err)
			}
			select {
			case <-ctx.Done():
				t.Fatalf("wait for APIBinding %s deletion: %v", name, ctx.Err())
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
	if exports == nil {
		return
	}
	for _, name := range []string{"app-studio", "infrastructure"} {
		export, ok := exports[name]
		if !ok {
			t.Fatalf("matrix omitted %s export", name)
		}
		binding := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "apis.kcp.io/v1alpha2",
			"kind":       "APIBinding",
			"metadata":   map[string]any{"name": name},
			"spec": map[string]any{"reference": map[string]any{"export": map[string]any{
				"path": export.Path, "name": export.Name,
			}}},
		}}
		if _, err := resource.Create(ctx, binding, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create APIBinding %s -> %s: %v", name, export.Path, err)
		}
	}
	for _, name := range []string{"app-studio", "infrastructure"} {
		for {
			binding, err := resource.Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("get APIBinding %s: %v", name, err)
			}
			phase, _, _ := unstructured.NestedString(binding.Object, "status", "phase")
			if phase == "Bound" {
				break
			}
			select {
			case <-ctx.Done():
				conditions, _, _ := unstructured.NestedSlice(binding.Object, "status", "conditions")
				t.Fatalf("APIBinding %s never became Bound: phase=%q conditions=%v err=%v", name, phase, conditions, ctx.Err())
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
}
