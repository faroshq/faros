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
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func TestCheckAPIExportRequiredResources(t *testing.T) {
	widgetRef := apiExportResource("widgets.example.io", "widgets", "v1alpha1.widgets.example.io")
	widgetSchema := apiResourceSchema("v1alpha1.widgets.example.io", "widgets.example.io", "widgets")
	required := []APIExportResource{{Group: "widgets.example.io", Name: "widgets"}}

	tests := []struct {
		name      string
		resources []any
		objects   []*unstructured.Unstructured
		wantErr   bool
	}{
		{
			name:      "required resource present",
			resources: []any{widgetRef},
			objects:   []*unstructured.Unstructured{widgetSchema},
		},
		{
			name:      "different resource does not satisfy required resource",
			resources: []any{apiExportResource("widgets.example.io", "gadgets", "v1alpha1.gadgets.example.io")},
			objects: []*unstructured.Unstructured{
				apiResourceSchema("v1alpha1.gadgets.example.io", "widgets.example.io", "gadgets"),
			},
			wantErr: true,
		},
		{
			name:    "empty APIExport shell is not ready",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			export := readyAPIExport(tt.resources, nil)
			err := checkAPIExportWithObjects(t, export, required, nil, tt.objects...)
			if tt.wantErr && err == nil {
				t.Fatal("APIExport was ready, want readiness error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("APIExport was not ready: %v", err)
			}
		})
	}
}

func TestCheckAPIExportRejectsEmptyRequiredResourceContract(t *testing.T) {
	export := readyAPIExport([]any{
		apiExportResource("widgets.example.io", "widgets", "v1alpha1.widgets.example.io"),
	}, nil)
	err := checkAPIExportWithObjects(t, export, nil, nil,
		apiResourceSchema("v1alpha1.widgets.example.io", "widgets.example.io", "widgets"),
	)
	if err == nil || !strings.Contains(err.Error(), "at least one") {
		t.Fatalf("error = %v, want required resource declaration error", err)
	}
}

func TestCheckAPIExportRejectsInvalidResourceSchemaContracts(t *testing.T) {
	required := []APIExportResource{{Group: "widgets.example.io", Name: "widgets"}}

	tests := []struct {
		name      string
		resource  map[string]any
		schema    *unstructured.Unstructured
		wantError string
	}{
		{
			name: "exported resource omits schema reference",
			resource: map[string]any{
				"group": "widgets.example.io",
				"name":  "widgets",
			},
			wantError: "schema",
		},
		{
			name:      "referenced schema does not exist",
			resource:  apiExportResource("widgets.example.io", "widgets", "v1alpha1.widgets.example.io"),
			wantError: "v1alpha1.widgets.example.io",
		},
		{
			name:      "schema group does not match export resource",
			resource:  apiExportResource("widgets.example.io", "widgets", "v1alpha1.widgets.example.io"),
			schema:    apiResourceSchema("v1alpha1.widgets.example.io", "other.example.io", "widgets"),
			wantError: "other.example.io/widgets",
		},
		{
			name:      "schema plural does not match export resource",
			resource:  apiExportResource("widgets.example.io", "widgets", "v1alpha1.widgets.example.io"),
			schema:    apiResourceSchema("v1alpha1.widgets.example.io", "widgets.example.io", "gadgets"),
			wantError: "widgets.example.io/gadgets",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objects []*unstructured.Unstructured
			if tt.schema != nil {
				objects = append(objects, tt.schema)
			}
			err := checkAPIExportWithObjects(t, readyAPIExport([]any{tt.resource}, nil), required, nil, objects...)
			if err == nil {
				t.Fatal("APIExport was ready, want schema contract error")
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tt.wantError)) {
				t.Fatalf("error = %q, want substring %q", err, tt.wantError)
			}
		})
	}
}

func TestCheckAPIExportAllowsValidDynamicResource(t *testing.T) {
	export := readyAPIExport([]any{
		apiExportResource("widgets.example.io", "widgets", "v1alpha1.widgets.example.io"),
		apiExportResource("dynamic.example.io", "instances", "v1alpha1.instances.dynamic.example.io"),
	}, nil)
	required := []APIExportResource{{Group: "widgets.example.io", Name: "widgets"}}
	schemas := []*unstructured.Unstructured{
		apiResourceSchema("v1alpha1.widgets.example.io", "widgets.example.io", "widgets"),
		apiResourceSchema("v1alpha1.instances.dynamic.example.io", "dynamic.example.io", "instances"),
	}

	if err := checkAPIExportWithObjects(t, export, required, nil, schemas...); err != nil {
		t.Fatalf("valid dynamic resource made APIExport unavailable: %v", err)
	}
}

func TestCheckAPIExportPermissionClaimContract(t *testing.T) {
	declared := []PermissionClaim{{
		Group: "authentication.k8s.io", Resource: "tokenreviews", Verbs: []string{"create"},
	}}
	matching := apiExportPermissionClaim("authentication.k8s.io", "tokenreviews", []string{"create"}, "")
	required := []APIExportResource{{Group: "widgets.example.io", Name: "widgets"}}
	resource := apiExportResource("widgets.example.io", "widgets", "v1alpha1.widgets.example.io")
	schema := apiResourceSchema("v1alpha1.widgets.example.io", "widgets.example.io", "widgets")

	tests := []struct {
		name     string
		actual   []any
		declared []PermissionClaim
		wantErr  bool
	}{
		{
			name:     "matching claims",
			actual:   []any{matching},
			declared: declared,
		},
		{
			name:     "declared claim missing from APIExport",
			declared: declared,
			wantErr:  true,
		},
		{
			name: "undeclared APIExport claim",
			actual: []any{
				matching,
				apiExportPermissionClaim("authorization.k8s.io", "subjectaccessreviews", []string{"create"}, ""),
			},
			declared: declared,
			wantErr:  true,
		},
		{
			name:     "verb drift",
			actual:   []any{apiExportPermissionClaim("authentication.k8s.io", "tokenreviews", []string{"get"}, "")},
			declared: declared,
			wantErr:  true,
		},
		{
			name:     "declared wildcard does not match concrete exported verbs",
			actual:   []any{apiExportPermissionClaim("authentication.k8s.io", "tokenreviews", []string{"create"}, "")},
			declared: []PermissionClaim{{Group: "authentication.k8s.io", Resource: "tokenreviews", Verbs: []string{"*"}}},
			wantErr:  true,
		},
		{
			name:     "exported wildcard does not match concrete declared verbs",
			actual:   []any{apiExportPermissionClaim("authentication.k8s.io", "tokenreviews", []string{"*"}, "")},
			declared: declared,
			wantErr:  true,
		},
		{
			name:     "wildcard matches wildcard",
			actual:   []any{apiExportPermissionClaim("authentication.k8s.io", "tokenreviews", []string{"*"}, "")},
			declared: []PermissionClaim{{Group: "authentication.k8s.io", Resource: "tokenreviews", Verbs: []string{"*"}}},
		},
		{
			name: "identity-bearing claim requires a resolved trusted identity",
			actual: []any{
				apiExportPermissionClaim("tenancy.faros.sh", "organizations", []string{"get"}, "organizations-identity"),
			},
			declared: []PermissionClaim{{Group: "tenancy.faros.sh", Resource: "organizations", Verbs: []string{"get"}}},
			wantErr:  true,
		},
		{
			name: "wrong nonempty identity hash is rejected",
			actual: []any{
				apiExportPermissionClaim("tenancy.faros.sh", "organizations", []string{"get"}, "stale-identity"),
			},
			declared: []PermissionClaim{{Group: "tenancy.faros.sh", Resource: "organizations", Verbs: []string{"get"}, ExpectedIdentityHash: "current-identity"}},
			wantErr:  true,
		},
		{
			name: "current identity hash is valid",
			actual: []any{
				apiExportPermissionClaim("tenancy.faros.sh", "organizations", []string{"get"}, "organizations-identity"),
			},
			declared: []PermissionClaim{{Group: "tenancy.faros.sh", Resource: "organizations", Verbs: []string{"get"}, ExpectedIdentityHash: "organizations-identity"}},
		},
		{
			name: "non Faros custom claim also requires trusted identity",
			actual: []any{
				apiExportPermissionClaim("widgets.example.io", "widgets", []string{"get"}, "widgets-identity"),
			},
			declared: []PermissionClaim{{Group: "widgets.example.io", Resource: "widgets", Verbs: []string{"get"}}},
			wantErr:  true,
		},
		{
			name: "non Faros custom claim accepts matching trusted identity",
			actual: []any{
				apiExportPermissionClaim("widgets.example.io", "widgets", []string{"get"}, "widgets-identity"),
			},
			declared: []PermissionClaim{{Group: "widgets.example.io", Resource: "widgets", Verbs: []string{"get"}, ExpectedIdentityHash: "widgets-identity"}},
		},
		{
			name: "identity-less built-in claim rejects identity source",
			actual: []any{
				apiExportPermissionClaim("authentication.k8s.io", "tokenreviews", []string{"create"}, ""),
			},
			declared: []PermissionClaim{{
				Group: "authentication.k8s.io", Resource: "tokenreviews", Verbs: []string{"create"},
				IdentitySourceKind: "Platform",
			}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkAPIExportWithObjects(t, readyAPIExport([]any{resource}, tt.actual), required, tt.declared, schema)
			if tt.wantErr && err == nil {
				t.Fatal("APIExport was ready, want permission claim contract error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("APIExport was not ready: %v", err)
			}
		})
	}
}

func TestTrustedResourceIdentityUsesExactCurrentExport(t *testing.T) {
	current := readyAPIExport([]any{
		apiExportResource("infrastructure.faros.sh", "applications", "v1alpha1.applications.infrastructure.faros.sh"),
	}, nil)
	current.SetName("infrastructure.faros.sh")
	exports := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*current}}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(),
		apiResourceSchema("v1alpha1.applications.infrastructure.faros.sh", "infrastructure.faros.sh", "applications"),
	)

	got, err := trustedResourceIdentity(context.Background(), client, exports, "root:faros:providers:infrastructure", "infrastructure.faros.sh", "applications")
	if err != nil {
		t.Fatalf("current trusted identity was rejected: %v", err)
	}
	if got != "export-identity" {
		t.Fatalf("identityHash = %q, want export-identity", got)
	}

	if _, err := trustedResourceIdentity(context.Background(), client, exports, "root:faros:providers:infrastructure", "other.faros.sh", "applications"); err == nil {
		t.Fatal("same resource name from the wrong API group satisfied the trusted source")
	}
}

func TestTrustedResourceIdentityValidatesReferencedSchemaContract(t *testing.T) {
	const (
		sourcePath = "root:faros:providers:widgets"
		group      = "widgets.example.io"
		resource   = "widgets"
		schemaName = "v1alpha1.widgets.widgets.example.io"
	)
	export := readyAPIExport([]any{apiExportResource(group, resource, schemaName)}, nil)
	export.SetName("widgets.example.io")
	exports := &unstructured.UnstructuredList{Items: []unstructured.Unstructured{*export}}

	tests := []struct {
		name      string
		objects   []runtime.Object
		wantError string
	}{
		{
			name:      "missing schema",
			wantError: "missing APIResourceSchema",
		},
		{
			name:      "wrong schema group",
			objects:   []runtime.Object{apiResourceSchema(schemaName, "attacker.example.io", resource)},
			wantError: "want widgets.example.io/widgets",
		},
		{
			name:      "wrong schema plural",
			objects:   []runtime.Object{apiResourceSchema(schemaName, group, "gadgets")},
			wantError: "want widgets.example.io/widgets",
		},
		{
			name:    "matching schema",
			objects: []runtime.Object{apiResourceSchema(schemaName, group, resource)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), tt.objects...)
			got, err := trustedResourceIdentity(context.Background(), client, exports, sourcePath, group, resource)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("error = %v, want substring %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("trusted identity rejected: %v", err)
			}
			if got != "export-identity" {
				t.Fatalf("identityHash = %q, want export-identity", got)
			}
		})
	}
}

func TestVerifyAPIExportRejectsDriftAfterReadySnapshot(t *testing.T) {
	const (
		group      = "tenancy.faros.sh"
		resource   = "organizations"
		currentID  = "organizations-identity"
		driftedID  = "attacker-identity"
		exportName = "database.providers.faros.sh"
	)
	required := []APIExportResource{{Group: "widgets.example.io", Name: "widgets"}}
	claims := []PermissionClaim{{
		Group: group, Resource: resource, Verbs: []string{"get"}, ExpectedIdentityHash: currentID,
	}}
	export := readyAPIExport(
		[]any{apiExportResource("widgets.example.io", "widgets", "v1alpha1.widgets.example.io")},
		[]any{apiExportPermissionClaim(group, resource, []string{"get"}, currentID)},
	)
	schemaObject := apiResourceSchema("v1alpha1.widgets.example.io", "widgets.example.io", "widgets")
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), export, schemaObject)

	verified, err := verifyAPIExport(context.Background(), client, exportName, required, claims)
	if err != nil {
		t.Fatalf("verify ready snapshot: %v", err)
	}
	if got := verified.ClaimIdentityHashes[group+"/"+resource]; got != currentID {
		t.Fatalf("verified identity = %q, want %q", got, currentID)
	}

	current, err := client.Resource(apiExportV1Alpha2GVR).Get(context.Background(), exportName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get export for drift: %v", err)
	}
	if err := unstructured.SetNestedSlice(current.Object, []any{
		apiExportPermissionClaim(group, resource, []string{"get"}, driftedID),
	}, "spec", "permissionClaims"); err != nil {
		t.Fatalf("set drifted claims: %v", err)
	}
	if _, err := client.Resource(apiExportV1Alpha2GVR).Update(context.Background(), current, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update drifted export: %v", err)
	}

	if _, err := verifyAPIExport(context.Background(), client, exportName, required, claims); err == nil || !strings.Contains(err.Error(), "identityHash does not match") {
		t.Fatalf("drift verification error = %v, want identity mismatch", err)
	}
	if got := verified.ClaimIdentityHashes[group+"/"+resource]; got != currentID {
		t.Fatalf("trusted snapshot changed after export mutation: got %q, want %q", got, currentID)
	}
}

func TestPermissionClaimIdentitySourcePathIsServerDerived(t *testing.T) {
	tests := []struct {
		name    string
		claim   PermissionClaim
		want    string
		wantErr bool
	}{
		{name: "platform", claim: PermissionClaim{IdentitySourceKind: "Platform"}, want: "root:faros:system:controllers"},
		{name: "provider", claim: PermissionClaim{IdentitySourceKind: "Provider", IdentitySourceProvider: "infrastructure"}, want: "root:faros:providers:infrastructure"},
		{name: "provider cannot inject path", claim: PermissionClaim{IdentitySourceKind: "Provider", IdentitySourceProvider: "infrastructure:attacker"}, wantErr: true},
		{name: "platform cannot redirect", claim: PermissionClaim{IdentitySourceKind: "Platform", IdentitySourceProvider: "attacker"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := permissionClaimIdentitySourcePath(tt.claim)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("path = %q, want error", got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("path = %q, err = %v, want %q", got, err, tt.want)
			}
		})
	}
}

func TestPermissionClaimIdentitySourceRequirementFollowsActualTargetHash(t *testing.T) {
	p := &Provisioner{}
	tests := []struct {
		name         string
		claim        PermissionClaim
		actualHash   string
		wantError    string
		wantIdentity string
	}{
		{
			name:       "arbitrary custom group cannot omit source when identity bearing",
			claim:      PermissionClaim{Group: "widgets.example.io", Resource: "widgets"},
			actualHash: "widgets-identity",
			wantError:  "requires a Platform or Provider identitySource",
		},
		{
			name:      "identity-less built-in cannot redirect to source",
			claim:     PermissionClaim{Group: "authentication.k8s.io", Resource: "tokenreviews", IdentitySourceKind: "Platform"},
			wantError: "only valid for an identity-bearing",
		},
		{
			name:  "identity-less built-in needs no source",
			claim: PermissionClaim{Group: "authentication.k8s.io", Resource: "tokenreviews"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := p.resolvePermissionClaimIdentity(context.Background(), tt.claim, tt.actualHash)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("error = %v, want substring %q", err, tt.wantError)
				}
				return
			}
			if err != nil || got != tt.wantIdentity {
				t.Fatalf("identity = %q, error = %v, want %q", got, err, tt.wantIdentity)
			}
		})
	}
}

func checkAPIExportWithObjects(
	t *testing.T,
	export *unstructured.Unstructured,
	required []APIExportResource,
	claims []PermissionClaim,
	objects ...*unstructured.Unstructured,
) error {
	t.Helper()
	allObjects := make([]runtime.Object, 0, len(objects)+1)
	allObjects = append(allObjects, export)
	for _, object := range objects {
		allObjects = append(allObjects, object)
	}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), allObjects...)
	return checkAPIExport(context.Background(), client, export.GetName(), required, claims)
}

func readyAPIExport(resources, claims []any) *unstructured.Unstructured {
	if resources == nil {
		resources = []any{}
	}
	if claims == nil {
		claims = []any{}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apis.kcp.io/v1alpha2",
		"kind":       "APIExport",
		"metadata":   map[string]any{"name": "database.providers.faros.sh"},
		"spec": map[string]any{
			"resources":        resources,
			"permissionClaims": claims,
		},
		"status": map[string]any{
			"identityHash": "export-identity",
			"conditions": []any{
				map[string]any{"type": "IdentityValid", "status": "True"},
			},
		},
	}}
}

func apiExportResource(group, name, schemaName string) map[string]any {
	return map[string]any{
		"group":  group,
		"name":   name,
		"schema": schemaName,
	}
}

func apiResourceSchema(name, group, plural string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apis.kcp.io/v1alpha1",
		"kind":       "APIResourceSchema",
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{
			"group": group,
			"names": map[string]any{"plural": plural},
		},
	}}
}

func apiExportPermissionClaim(group, resource string, verbs []string, identityHash string) map[string]any {
	unstructuredVerbs := make([]any, 0, len(verbs))
	for _, verb := range verbs {
		unstructuredVerbs = append(unstructuredVerbs, verb)
	}
	claim := map[string]any{
		"group":    group,
		"resource": resource,
		"verbs":    unstructuredVerbs,
	}
	if identityHash != "" {
		claim["identityHash"] = identityHash
	}
	return claim
}
