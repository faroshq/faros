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
	clienttesting "k8s.io/client-go/testing"

	"github.com/faroshq/faros/pkg/hub/providers"
)

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

func TestReconcileProviderAPIBinding(t *testing.T) {
	desiredClaims := []any{
		map[string]any{"group": "infrastructure.faros.sh", "resource": "instances", "state": "Accepted"},
		map[string]any{"group": "code.faros.sh", "resource": "repositorysyncs", "state": "Accepted"},
	}
	desired := providerAPIBindingForTest("app-studio", "root:faros:providers:app-studio", "ai.faros.sh", desiredClaims)

	newClient := func(objects ...runtime.Object) *fake.FakeDynamicClient {
		return fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
			apiBindingGVR: "APIBindingList",
		}, objects...)
	}

	t.Run("creates missing binding", func(t *testing.T) {
		dyn := newClient()
		if err := reconcileProviderAPIBinding(context.Background(), dyn.Resource(apiBindingGVR), desired); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got, err := dyn.Resource(apiBindingGVR).Get(context.Background(), desired.GetName(), metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get created binding: %v", err)
		}
		assertProviderBindingClaims(t, got, desiredClaims)
	})

	t.Run("updates stale claims and preserves server state", func(t *testing.T) {
		existing := providerAPIBindingForTest("app-studio", "root:faros:providers:app-studio", "ai.faros.sh", []any{
			map[string]any{"group": "infrastructure.faros.sh", "resource": "applications", "state": "Accepted"},
		})
		existing.SetLabels(map[string]string{"preserve": "true"})
		existing.Object["status"] = map[string]any{"phase": "Bound"}
		dyn := newClient(existing)

		if err := reconcileProviderAPIBinding(context.Background(), dyn.Resource(apiBindingGVR), desired); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got, err := dyn.Resource(apiBindingGVR).Get(context.Background(), desired.GetName(), metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get updated binding: %v", err)
		}
		assertProviderBindingClaims(t, got, desiredClaims)
		if got.GetLabels()["preserve"] != "true" {
			t.Fatalf("labels were not preserved: %#v", got.GetLabels())
		}
		phase, _, _ := unstructured.NestedString(got.Object, "status", "phase")
		if phase != "Bound" {
			t.Fatalf("status.phase = %q, want Bound", phase)
		}
	})

	t.Run("skips update when current", func(t *testing.T) {
		dyn := newClient(desired.DeepCopy())
		updates := 0
		dyn.PrependReactor("update", "apibindings", func(clienttesting.Action) (bool, runtime.Object, error) {
			updates++
			return false, nil, nil
		})
		if err := reconcileProviderAPIBinding(context.Background(), dyn.Resource(apiBindingGVR), desired); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		if updates != 0 {
			t.Fatalf("updates = %d, want 0", updates)
		}
	})

	t.Run("retries update conflict from fresh read", func(t *testing.T) {
		existing := providerAPIBindingForTest("app-studio", "root:faros:providers:app-studio", "ai.faros.sh", []any{})
		dyn := newClient(existing)
		updates := 0
		dyn.PrependReactor("update", "apibindings", func(clienttesting.Action) (bool, runtime.Object, error) {
			updates++
			if updates == 1 {
				return true, nil, apierrors.NewConflict(apiBindingGVR.GroupResource(), desired.GetName(), nil)
			}
			return false, nil, nil
		})
		if err := reconcileProviderAPIBinding(context.Background(), dyn.Resource(apiBindingGVR), desired); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		if updates != 2 {
			t.Fatalf("updates = %d, want 2", updates)
		}
		got, err := dyn.Resource(apiBindingGVR).Get(context.Background(), desired.GetName(), metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get updated binding: %v", err)
		}
		assertProviderBindingClaims(t, got, desiredClaims)
	})

	t.Run("rejects mismatched export", func(t *testing.T) {
		existing := providerAPIBindingForTest("app-studio", "root:faros:providers:other", "other.faros.sh", []any{})
		dyn := newClient(existing)
		err := reconcileProviderAPIBinding(context.Background(), dyn.Resource(apiBindingGVR), desired)
		if err == nil || !strings.Contains(err.Error(), "expected") {
			t.Fatalf("error = %v, want export mismatch", err)
		}
	})

	t.Run("rejects terminating binding", func(t *testing.T) {
		existing := desired.DeepCopy()
		now := metav1.NewTime(time.Now())
		existing.SetDeletionTimestamp(&now)
		dyn := newClient(existing)
		err := reconcileProviderAPIBinding(context.Background(), dyn.Resource(apiBindingGVR), desired)
		if err == nil || !strings.Contains(err.Error(), "terminating") {
			t.Fatalf("error = %v, want terminating binding", err)
		}
	})
}

func TestAuthorizeProviderClaimsIsAdditiveAndRetriesConflicts(t *testing.T) {
	existingClaims := []any{
		map[string]any{
			"group": "example.io", "resource": "preserved", "verbs": []any{"get"},
			"identityHash": "preserved-identity", "selector": map[string]any{"matchAll": true}, "state": "Accepted",
		},
		map[string]any{
			"group": "infrastructure.faros.sh", "resource": "instances", "verbs": []any{"get"},
			"identityHash": "stale-identity", "selector": map[string]any{"matchLabels": map[string]any{"team": "blue"}}, "state": "Rejected",
		},
	}
	existing := providerAPIBindingForTest("deployments", "root:faros:providers:deployments", "deployments.faros.sh", existingClaims)
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		apiBindingGVR: "APIBindingList",
	}, existing)
	updates := 0
	dyn.PrependReactor("update", "apibindings", func(clienttesting.Action) (bool, runtime.Object, error) {
		updates++
		if updates == 1 {
			return true, nil, apierrors.NewConflict(apiBindingGVR.GroupResource(), "deployments", nil)
		}
		return false, nil, nil
	})

	err := authorizeProviderClaims(
		context.Background(), dyn.Resource(apiBindingGVR), "deployments",
		"root:faros:providers:deployments", "deployments.faros.sh",
		[]ProviderClaim{{Group: "infrastructure.faros.sh", Resource: "instances", Verbs: []string{"get", "list", "create", "update"}}},
		map[string]string{"infrastructure.faros.sh/instances": "authoritative-identity"},
	)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if updates != 2 {
		t.Fatalf("updates = %d, want 2", updates)
	}
	got, err := dyn.Resource(apiBindingGVR).Get(context.Background(), "deployments", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get updated binding: %v", err)
	}
	claims, _, err := unstructured.NestedSlice(got.Object, "spec", "permissionClaims")
	if err != nil {
		t.Fatalf("read claims: %v", err)
	}
	if len(claims) != 2 {
		t.Fatalf("claims = %#v, want two", claims)
	}
	if !reflect.DeepEqual(claims[0], existingClaims[0]) {
		t.Fatalf("unrelated grant changed: got %#v, want %#v", claims[0], existingClaims[0])
	}
	updated := claims[1].(map[string]any)
	if state, _, _ := unstructured.NestedString(updated, "state"); state != "Accepted" {
		t.Fatalf("state = %q, want Accepted", state)
	}
	if identityHash, _, _ := unstructured.NestedString(updated, "identityHash"); identityHash != "authoritative-identity" {
		t.Fatalf("identityHash = %q, want authoritative-identity", identityHash)
	}
	team, _, _ := unstructured.NestedString(updated, "selector", "matchLabels", "team")
	if team != "blue" {
		t.Fatalf("selector was widened: %#v", updated["selector"])
	}
	verbs, _, _ := unstructured.NestedStringSlice(updated, "verbs")
	if want := []string{"get", "list", "create", "update"}; !reflect.DeepEqual(verbs, want) {
		t.Fatalf("verbs = %#v, want %#v", verbs, want)
	}
}

func TestProviderAPIBindingAccessSeparatesConsentOfferAndApplication(t *testing.T) {
	binding := providerAPIBindingForTest("deployments", "root:faros:providers:deployments", "deployments.faros.sh", []any{
		map[string]any{"group": "example.io", "resource": "widgets", "verbs": []any{"get"}, "identityHash": "id", "state": "Accepted"},
	})
	binding.Object["status"] = map[string]any{
		"phase": "Bound",
		"exportPermissionClaims": []any{
			map[string]any{"group": "example.io", "resource": "widgets", "verbs": []any{"get", "list"}, "identityHash": "id"},
		},
		"appliedPermissionClaims": []any{
			map[string]any{"group": "example.io", "resource": "widgets", "verbs": []any{"get"}, "identityHash": "id", "selector": map[string]any{"matchAll": true}},
		},
	}
	access, err := providerAPIBindingAccess(binding)
	if err != nil {
		t.Fatalf("parse access: %v", err)
	}
	if access.Phase != "Bound" || len(access.SpecClaims) != 1 || !access.SpecClaims[0].Accepted {
		t.Fatalf("spec access = %#v", access)
	}
	if len(access.ExportClaims) != 1 || len(access.AppliedClaims) != 1 {
		t.Fatalf("status access = %#v", access)
	}
}

func providerAPIBindingForTest(name, path, export string, claims []any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apis.kcp.io/v1alpha2",
		"kind":       "APIBinding",
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{
			"reference":        map[string]any{"export": map[string]any{"path": path, "name": export}},
			"permissionClaims": claims,
		},
	}}
}

func assertProviderBindingClaims(t *testing.T, binding *unstructured.Unstructured, want []any) {
	t.Helper()
	got, found, err := unstructured.NestedSlice(binding.Object, "spec", "permissionClaims")
	if err != nil {
		t.Fatalf("read permission claims: %v", err)
	}
	if !found {
		t.Fatal("permission claims not found")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("permission claims = %#v, want %#v", got, want)
	}
}
