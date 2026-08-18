/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
*/

package kcp

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"

	"github.com/faroshq/faros/pkg/hub/providers"
)

func TestEdgeProxyGrantRulesLimitConsumerProviders(t *testing.T) {
	want := []any{
		map[string]any{
			"nonResourceURLs": []any{"/"},
			"verbs":           []any{"access"},
		},
		map[string]any{
			"apiGroups": []any{"edges.faros.sh"},
			"resources": []any{"kubernetesclusters"},
			"verbs":     []any{"proxy"},
		},
	}
	if got := edgeProxyGrantRules("kuery"); !reflect.DeepEqual(got, want) {
		t.Fatalf("kuery edge-proxy rules = %#v, want %#v", got, want)
	}
}

func TestEdgeProxyGrantRulesRetainEdgesRuntimePrivileges(t *testing.T) {
	rules := edgeProxyGrantRules("edges")
	want := map[string]bool{
		"edges.faros.sh/kubernetesclusters/status": false,
		"/secrets":                                  false,
		"authentication.k8s.io/tokenreviews":        false,
		"authorization.k8s.io/subjectaccessreviews": false,
	}
	for _, raw := range rules {
		rule := raw.(map[string]any)
		groups, _ := rule["apiGroups"].([]any)
		resources, _ := rule["resources"].([]any)
		group := ""
		if len(groups) != 0 {
			group, _ = groups[0].(string)
		}
		for _, resource := range resources {
			key := group + "/" + resource.(string)
			if _, ok := want[key]; ok {
				want[key] = true
			}
		}
	}
	for privilege, found := range want {
		if !found {
			t.Errorf("edges grant lost required privilege %s", privilege)
		}
	}
}

func TestEdgeProxyGrantSubjectsDoNotBindConsumerLocalFallback(t *testing.T) {
	qualified := "system:kcp:serviceaccount:abc123:default:provider"
	kuery := edgeProxyGrantSubjects("kuery", qualified)
	if len(kuery) != 1 || kuery[0].(map[string]any)["name"] != qualified {
		t.Fatalf("kuery subjects = %#v, want qualified subject only", kuery)
	}

	edges := edgeProxyGrantSubjects("edges", qualified)
	if len(edges) != 2 || edges[1].(map[string]any)["name"] != "system:serviceaccount:default:provider" {
		t.Fatalf("edges subjects = %#v, want qualified and legacy local subjects", edges)
	}
}

func TestReconcileProviderAPIBindingUpdatesClaimsInPlace(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		apiBindingGVR: "APIBindingList",
	})
	resource := dyn.Resource(apiBindingGVR)
	existing := providerBindingForTest("agents", "root:faros:providers:agents", "agents.faros.sh", []any{
		bindingClaimForTest("", "secrets", []any{"get"}, "Accepted"),
	})
	existing.SetAnnotations(map[string]string{"keep": "me"})
	if _, err := resource.Create(ctx, existing, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create existing binding: %v", err)
	}

	desired := providerBindingForTest("agents", "root:faros:providers:agents", "agents.faros.sh", []any{
		bindingClaimForTest("", "secrets", []any{"get"}, "Accepted"),
		bindingClaimForTest("authentication.k8s.io", "tokenreviews", []any{"create"}, "Accepted"),
		bindingClaimForTest("authorization.k8s.io", "subjectaccessreviews", []any{"create"}, "Accepted"),
	})
	if err := reconcileProviderAPIBinding(ctx, resource, desired); err != nil {
		t.Fatalf("reconcile existing binding: %v", err)
	}

	got, err := resource.Get(ctx, "agents", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get reconciled binding: %v", err)
	}
	if got.GetAnnotations()["keep"] != "me" {
		t.Fatalf("existing metadata was not preserved: %#v", got.GetAnnotations())
	}
	claims, _, _ := unstructured.NestedSlice(got.Object, "spec", "permissionClaims")
	if len(claims) != 3 {
		t.Fatalf("permission claims = %d, want 3: %#v", len(claims), claims)
	}
}

func TestReconcileProviderAPIBindingRefusesReferenceAdoption(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		apiBindingGVR: "APIBindingList",
	})
	resource := dyn.Resource(apiBindingGVR)
	existing := providerBindingForTest("agents", "root:faros:providers:other", "other.faros.sh", nil)
	if _, err := resource.Create(ctx, existing, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create existing binding: %v", err)
	}
	desired := providerBindingForTest("agents", "root:faros:providers:agents", "agents.faros.sh", nil)
	if err := reconcileProviderAPIBinding(ctx, resource, desired); err == nil || !apierrors.IsConflict(err) || !strings.Contains(err.Error(), "already references") {
		t.Fatalf("error = %v, want reference conflict", err)
	}
	got, err := resource.Get(ctx, "agents", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get existing binding: %v", err)
	}
	path, _, _ := unstructured.NestedString(got.Object, "spec", "reference", "export", "path")
	if path != "root:faros:providers:other" {
		t.Fatalf("reference path mutated to %q", path)
	}
}

func TestProviderAPIBindingFromUnstructuredReadsClaimValidityAndDecisions(t *testing.T) {
	item := providerBindingForTest("agents", "root:faros:providers:agents", "agents.faros.sh", []any{
		bindingClaimForTest("authentication.k8s.io", "tokenreviews", []any{"create"}, "Accepted"),
	})
	item.Object["status"] = map[string]any{"conditions": []any{
		map[string]any{"type": "PermissionClaimsValid", "status": "False", "reason": "InvalidPermissionClaims"},
	}}

	got := providerAPIBindingFromUnstructured(item)
	if got.Name != "agents" || !got.PermissionClaimsValidKnown || got.PermissionClaimsValid {
		t.Fatalf("parsed binding validity = %#v", got)
	}
	if len(got.PermissionClaims) != 1 {
		t.Fatalf("permission claims = %#v", got.PermissionClaims)
	}
	claim := got.PermissionClaims[0]
	if claim.Group != "authentication.k8s.io" || claim.Resource != "tokenreviews" || claim.State != "Accepted" {
		t.Fatalf("parsed claim = %#v", claim)
	}
}

func TestProviderAPIBindingUsesVerifiedSnapshotIdentity(t *testing.T) {
	const claimKey = "tenancy.faros.sh/organizations"
	verified := providers.VerifiedAPIExport{ClaimIdentityHashes: map[string]string{
		claimKey: "trusted-identity",
	}}
	claims := []ProviderClaim{{
		Group: "tenancy.faros.sh", Resource: "organizations", Verbs: []string{"get"}, Accepted: true,
	}}

	// The provider-owned export can change after verification. Binding
	// construction must not perform another export read or substitute that newer,
	// unverified value; it consumes only the trusted snapshot passed here.
	driftedProviderValue := "attacker-identity"
	binding := providerAPIBindingFromVerifiedSnapshot(
		"app-studio",
		"root:faros:providers:app-studio",
		"app-studio.faros.sh",
		claims,
		verified,
	)
	if len(binding.Spec.PermissionClaims) != 1 {
		t.Fatalf("permission claims = %d, want 1", len(binding.Spec.PermissionClaims))
	}
	got := binding.Spec.PermissionClaims[0].IdentityHash
	if got != "trusted-identity" {
		t.Fatalf("binding identityHash = %q, want verified snapshot", got)
	}
	if got == driftedProviderValue {
		t.Fatal("binding used the post-verification provider value")
	}
}

func providerBindingForTest(name, path, exportName string, claims []any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apis.kcp.io/v1alpha2",
		"kind":       "APIBinding",
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{
			"reference":        map[string]any{"export": map[string]any{"path": path, "name": exportName}},
			"permissionClaims": claims,
		},
	}}
}

func bindingClaimForTest(group, resource string, verbs []any, state string) map[string]any {
	return map[string]any{
		"group": group, "resource": resource, "verbs": verbs, "state": state,
		"selector": map[string]any{"matchAll": true},
	}
}

func TestEnsureExportBindingWaitsForInPlaceSchemaUpgradeWithoutPruning(t *testing.T) {
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		apiBindingGVR:        "APIBindingList",
		apiExportV1Alpha2GVR: "APIExportList",
		catalogEntryGVR:      "CatalogEntryList",
	}
	bindClient := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds,
		&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "apis.kcp.io/v1alpha2", "kind": "APIBinding",
			"metadata": map[string]any{"name": "providers.faros.sh"},
			"spec": map[string]any{"reference": map[string]any{"export": map[string]any{
				"path": "root:faros:system:controllers", "name": "providers.faros.sh",
			}}},
			"status": map[string]any{"phase": "Bound", "boundResources": []any{map[string]any{
				"group": "providers.faros.sh", "resource": "catalogentries",
				"schema": map[string]any{"name": "v1-old.catalogentries.providers.faros.sh"},
			}}},
		}},
		&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "providers.faros.sh/v1alpha1", "kind": "CatalogEntry",
			"metadata": map[string]any{"name": "code"},
		}},
	)
	exportClient := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds,
		&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "apis.kcp.io/v1alpha2", "kind": "APIExport",
			"metadata": map[string]any{"name": "providers.faros.sh"},
			"spec": map[string]any{"resources": []any{map[string]any{
				"group": "providers.faros.sh", "name": "catalogentries",
				"schema": "v2-current.catalogentries.providers.faros.sh",
			}}},
		}},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := ensureExportBinding(ctx, bindClient, exportClient, "root:faros:system:controllers", "providers.faros.sh")
	if err == nil {
		t.Fatal("stale binding unexpectedly reported current")
	}
	if _, err := bindClient.Resource(apiBindingGVR).Get(context.Background(), "providers.faros.sh", metav1.GetOptions{}); err != nil {
		t.Fatalf("schema upgrade deleted the APIBinding: %v", err)
	}
	if _, err := bindClient.Resource(catalogEntryGVR).Get(context.Background(), "code", metav1.GetOptions{}); err != nil {
		t.Fatalf("schema upgrade pruned the CatalogEntry: %v", err)
	}
}

func TestEnsureExportBindingRejectsCanonicalNameCollision(t *testing.T) {
	scheme := runtime.NewScheme()
	listKinds := map[schema.GroupVersionResource]string{
		apiBindingGVR:        "APIBindingList",
		apiExportV1Alpha2GVR: "APIExportList",
	}
	bindClient := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds,
		providerBindingForTest("providers.faros.sh", "root:faros:providers:attacker", "attacker.example.io", nil),
	)
	exportClient := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds,
		&unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "apis.kcp.io/v1alpha2", "kind": "APIExport",
			"metadata": map[string]any{"name": "providers.faros.sh"},
			"spec": map[string]any{"resources": []any{map[string]any{
				"group": "providers.faros.sh", "name": "catalogentries",
				"schema": "v2-current.catalogentries.providers.faros.sh",
			}}},
		}},
	)

	started := time.Now()
	err := ensureExportBinding(context.Background(), bindClient, exportClient, "root:faros:system:controllers", "providers.faros.sh")
	if err == nil || !strings.Contains(err.Error(), "already references") {
		t.Fatalf("error = %v, want reference collision", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("deterministic binding collision waited %v instead of failing fast", elapsed)
	}
}

func TestAPIBindingUsesSchemasRequiresCurrentReferencesAndAllowsRetainedResources(t *testing.T) {
	binding := &unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{"boundResources": []any{map[string]any{
			"group": "providers.faros.sh", "resource": "catalogentries",
			"schema": map[string]any{"name": "v2-current.catalogentries.providers.faros.sh"},
		}}},
	}}
	want := []apiBindingSchema{{
		Group: "providers.faros.sh", Resource: "catalogentries",
		Schema: "v2-current.catalogentries.providers.faros.sh",
	}}
	if !apiBindingUsesSchemas(binding, want) {
		t.Fatal("current binding schema was rejected")
	}
	want[0].Schema = "v3-next.catalogentries.providers.faros.sh"
	if apiBindingUsesSchemas(binding, want) {
		t.Fatal("stale binding schema was accepted")
	}
	want[0].Schema = "v2-current.catalogentries.providers.faros.sh"
	bound, _, _ := unstructured.NestedSlice(binding.Object, "status", "boundResources")
	bound = append(bound, map[string]any{
		"group": "providers.faros.sh", "resource": "retired",
		"schema": map[string]any{"name": "v1.retired.providers.faros.sh"},
	})
	_ = unstructured.SetNestedSlice(binding.Object, bound, "status", "boundResources")
	if !apiBindingUsesSchemas(binding, want) {
		t.Fatal("binding with all current schemas and a retained removed resource was rejected")
	}
}

func TestEnsureBuiltinCatalogEntries_DoesNotTouchChartOwnedEntry(t *testing.T) {
	const providerName = "chart-owned-test"
	if _, ok := providers.BuiltinByName(providerName); !ok {
		providers.RegisterBuiltin(providers.BuiltinSpec{
			Name:        providerName,
			DisplayName: "Chart Owned Test",
		})
	}

	scheme := runtime.NewScheme()
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		apiBindingGVR:   "APIBindingList",
		catalogEntryGVR: "CatalogEntryList",
	})

	if _, err := dyn.Resource(apiBindingGVR).Create(context.Background(), &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "apis.kcp.io/v1alpha2",
		"kind":       "APIBinding",
		"metadata": map[string]interface{}{
			"name": "providers.faros.sh",
		},
		"status": map[string]interface{}{
			"phase": "Bound",
		},
	}}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seeding APIBinding: %v", err)
	}

	original := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "providers.faros.sh/v1alpha1",
		"kind":       "CatalogEntry",
		"metadata": map[string]interface{}{
			"name": providerName,
		},
		"spec": map[string]interface{}{
			"displayName": "Provider from Chart",
			"ui": map[string]interface{}{
				"url": "/services/chart-owned-test",
			},
		},
	}}
	if _, err := dyn.Resource(catalogEntryGVR).Create(context.Background(), original, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seeding CatalogEntry: %v", err)
	}

	if err := ensureBuiltinCatalogEntries(context.Background(), dyn, []string{providerName}); err != nil {
		t.Fatalf("ensureBuiltinCatalogEntries: %v", err)
	}

	got, err := dyn.Resource(catalogEntryGVR).Get(context.Background(), providerName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get CatalogEntry: %v", err)
	}
	if got.GetAnnotations()[builtinAnnotation] == "true" {
		t.Fatal("expected chart-owned entry to remain unannotated")
	}
	displayName, found, err := unstructured.NestedString(got.Object, "spec", "displayName")
	if err != nil {
		t.Fatalf("reading displayName: %v", err)
	}
	if !found || displayName != "Provider from Chart" {
		t.Fatalf("displayName = %q, want chart-owned value", displayName)
	}
}
