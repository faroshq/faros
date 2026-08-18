/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package tiltcluster

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const (
	deploymentsProviderName = "deployments"
	deploymentsWorkspace    = "root:faros:providers:deployments"
	deploymentsGroup        = "deployments.faros.sh"
	deploymentsAPIExport    = "deployments.faros.sh"
	deploymentsTestWait     = 3 * time.Minute
)

var (
	deploymentsAPIBindingGVR = schema.GroupVersionResource{Group: "apis.kcp.io", Version: "v1alpha2", Resource: "apibindings"}
	deploymentsWorkspaceGVR  = schema.GroupVersionResource{Group: "tenancy.kcp.io", Version: "v1alpha1", Resource: "workspaces"}
	deploymentsInstanceGVR   = schema.GroupVersionResource{
		Group: infraGroup, Version: "v1alpha1", Resource: "instances",
	}
)

// TestDeploymentsProviderRegistered is the first-class provider gate for the
// headless deployment reconciler. It verifies the catalog/runtime readiness
// contract and the exact cross-provider permissions its controller needs.
func TestDeploymentsProviderRegistered(t *testing.T) {
	requireStack(t)
	ctx := context.Background()

	if !httpOK(deploymentsURL+"/healthz", 5*time.Second) || !httpOK(deploymentsURL+"/readyz", 5*time.Second) {
		t.Fatalf("deployments provider is not live and ready at %s", deploymentsURL)
	}

	entry, err := kcpAdminDynamic(t, providersWorkspace).
		Resource(catalogEntryGVR).Get(ctx, deploymentsProviderName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get CatalogEntry %q in %s: %v", deploymentsProviderName, providersWorkspace, err)
	}
	if !conditionTrue(entry.Object, "Ready") {
		t.Fatalf("CatalogEntry %q not Ready; conditions=%v", deploymentsProviderName, conditionsOf(entry.Object))
	}

	export, err := kcpAdminDynamic(t, deploymentsWorkspace).
		Resource(apiExportGVR).Get(ctx, deploymentsAPIExport, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get APIExport %q in %s: %v", deploymentsAPIExport, deploymentsWorkspace, err)
	}
	resources := nestedSlice(export.Object, "spec", "resources")
	if len(resources) != 1 || !apiExportHasResource(export.Object, "repositorysyncs", deploymentsGroup) {
		t.Fatalf("APIExport %q resources = %v, want only %s/repositorysyncs", deploymentsAPIExport, resources, deploymentsGroup)
	}

	infraExport, err := kcpAdminDynamic(t, providerWorkspace).
		Resource(apiExportGVR).Get(ctx, infraAPIExportName, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		t.Fatalf("get Infrastructure APIExport identity: %v", err)
	}
	infraIdentity := ""
	if err == nil {
		infraIdentity, _, _ = nestedString(infraExport.Object, "status", "identityHash")
		if infraIdentity == "" {
			t.Fatal("installed Infrastructure APIExport has no identityHash")
		}
	}
	codeExport := mustAPIExport(t, codeWorkspace, codeAPIExport)
	codeIdentity, _, _ := nestedString(codeExport.Object, "status", "identityHash")
	if codeIdentity == "" {
		t.Fatal("Code APIExport has no identityHash")
	}

	wantExportClaims := []struct {
		group, resource, identity string
		verbs                     []string
	}{
		{codeGroup, "repositorycheckouts", codeIdentity, []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
		{"", "configmaps", "", []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
	}
	if infraIdentity != "" {
		wantExportClaims = append(wantExportClaims, struct {
			group, resource, identity string
			verbs                     []string
		}{infraGroup, "instances", infraIdentity, []string{"get", "create", "update", "patch", "delete"}})
	}
	claims := nestedSlice(export.Object, "spec", "permissionClaims")
	if len(claims) != len(wantExportClaims) {
		t.Fatalf("permissionClaims count = %d, want %d: %v", len(claims), len(wantExportClaims), claims)
	}
	for _, want := range wantExportClaims {
		claim, ok := permissionClaim(claims, want.group, want.resource)
		if !ok {
			t.Fatalf("missing permission claim for %s/%s", want.group, want.resource)
		}
		if got, _ := claim["identityHash"].(string); got != want.identity {
			t.Fatalf("claim %s identityHash = %q, want %q", want.resource, got, want.identity)
		}
		if got := stringSlice(claim["verbs"]); !slices.Equal(got, want.verbs) {
			t.Fatalf("claim %s verbs = %v, want %v", want.resource, got, want.verbs)
		}
	}

	wantCatalogClaims := []struct {
		group, resource string
		verbs           []string
		optional        bool
	}{
		{codeGroup, "repositorycheckouts", []string{"get", "list", "watch", "create", "update", "patch", "delete"}, false},
		{infraGroup, "instances", []string{"get", "create", "update", "patch", "delete"}, true},
		{"", "configmaps", []string{"get", "list", "watch", "create", "update", "patch", "delete"}, true},
	}
	catalogClaims := nestedSlice(entry.Object, "spec", "apiExport", "permissionClaims")
	if len(catalogClaims) != len(wantCatalogClaims) {
		t.Fatalf("CatalogEntry permissionClaims count = %d, want %d: %v", len(catalogClaims), len(wantCatalogClaims), catalogClaims)
	}
	for _, want := range wantCatalogClaims {
		catalogClaim, ok := permissionClaim(catalogClaims, want.group, want.resource)
		if !ok {
			t.Fatalf("CatalogEntry missing permission claim for %s/%s", want.group, want.resource)
		}
		if got := stringSlice(catalogClaim["verbs"]); !slices.Equal(got, want.verbs) {
			t.Fatalf("CatalogEntry claim %s verbs = %v, want %v", want.resource, got, want.verbs)
		}
		if got, _ := catalogClaim["optional"].(bool); got != want.optional {
			t.Fatalf("CatalogEntry claim %s optional = %t, want %t", want.resource, got, want.optional)
		}
		if tenantScoped, _ := catalogClaim["tenantScoped"].(bool); !tenantScoped {
			t.Fatalf("CatalogEntry claim %s is not tenant-scoped", want.resource)
		}
		if purpose, _ := catalogClaim["purpose"].(string); purpose == "" {
			t.Fatalf("CatalogEntry claim %s has no human-readable purpose", want.resource)
		}
	}

	dependencies := nestedSlice(entry.Object, "spec", "dependencies")
	if len(dependencies) != 1 {
		t.Fatalf("CatalogEntry dependencies = %v, want only Code", dependencies)
	}
	dependency, ok := dependencies[0].(map[string]any)
	if !ok || dependency["name"] != codeProviderName {
		t.Fatalf("CatalogEntry dependency = %v, want Code", dependencies[0])
	}

	t.Logf("deployments provider registered and ready: APIExport=%s codeIdentity=%s", deploymentsAPIExport, codeIdentity)
}

type deploymentRuntimeRef struct {
	gvr       schema.GroupVersionResource
	namespace string
	name      string
}

func waitInfrastructureInstanceReady(t *testing.T, tenant dynamic.Interface, instanceName string) *unstructured.Unstructured {
	t.Helper()
	var ready *unstructured.Unstructured
	if !waitTilt(t, deploymentsTestWait, func() (bool, string) {
		instance, err := tenant.Resource(deploymentsInstanceGVR).Get(context.Background(), instanceName, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		observedGeneration, _, _ := unstructured.NestedInt64(instance.Object, "status", "observedGeneration")
		phase, _, _ := nestedString(instance.Object, "status", "phase")
		if observedGeneration != instance.GetGeneration() || phase != "Ready" || !conditionTrue(instance.Object, "Ready") {
			status, reason, message := conditionState(instance.Object, "Ready")
			return false, fmt.Sprintf("phase=%q observedGeneration=%d/%d Ready=%s reason=%s message=%s",
				phase, observedGeneration, instance.GetGeneration(), status, reason, message)
		}
		ready = instance
		return true, ""
	}) {
		t.Fatalf("Infrastructure Instance %q did not become current and Ready", instanceName)
	}
	return ready
}

func deploymentRuntimeTarget(t *testing.T, instance *unstructured.Unstructured) deploymentRuntimeRef {
	t.Helper()
	ref, found, err := unstructured.NestedMap(instance.Object, "status", "runtimeRef")
	if err != nil || !found {
		t.Fatalf("ready Instance %q has no runtimeRef: found=%t err=%v", instance.GetName(), found, err)
	}
	apiVersion, _ := ref["apiVersion"].(string)
	resource, _ := ref["resource"].(string)
	namespace, _ := ref["namespace"].(string)
	name, _ := ref["name"].(string)
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil || resource == "" || namespace == "" || name == "" {
		t.Fatalf("ready Instance %q has invalid runtimeRef %#v: %v", instance.GetName(), ref, err)
	}
	return deploymentRuntimeRef{gvr: gv.WithResource(resource), namespace: namespace, name: name}
}

func createDeploymentsWorkspace(t *testing.T, parent dynamic.Interface, name string) string {
	t.Helper()
	workspace := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "tenancy.kcp.io/v1alpha1",
		"kind":       "Workspace",
		"metadata": map[string]any{
			"name":   name,
			"labels": map[string]any{"faros.sh/e2e-deployments": "true"},
		},
	}}
	if _, err := parent.Resource(deploymentsWorkspaceGVR).Create(context.Background(), workspace, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create deployments workspace %q: %v", name, err)
	}
	var path string
	if !waitTilt(t, deploymentsTestWait, func() (bool, string) {
		got, err := parent.Resource(deploymentsWorkspaceGVR).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		phase, _, _ := nestedString(got.Object, "status", "phase")
		path, _, _ = nestedString(got.Object, "spec", "cluster")
		return phase == "Ready" && path != "", fmt.Sprintf("phase=%s cluster=%s", phase, path)
	}) {
		t.Fatalf("deployments workspace %q never became Ready", name)
	}
	return path
}

func deploymentsBinding(infrastructureIdentityHash, codeIdentityHash string) *unstructured.Unstructured {
	resources := []struct {
		group, resource, identity string
		verbs                     []string
	}{
		{codeGroup, "repositorycheckouts", codeIdentityHash, []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
		{infraGroup, "instances", infrastructureIdentityHash, []string{"get", "create", "update", "patch", "delete"}},
		{"", "configmaps", "", []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
	}
	claims := make([]any, 0, len(resources))
	for _, item := range resources {
		claimVerbs := make([]any, len(item.verbs))
		for i, verb := range item.verbs {
			claimVerbs[i] = verb
		}
		claim := map[string]any{
			"group":    item.group,
			"resource": item.resource,
			"verbs":    claimVerbs,
			"selector": map[string]any{"matchAll": true},
			"state":    "Accepted",
		}
		if item.identity != "" {
			claim["identityHash"] = item.identity
		}
		claims = append(claims, claim)
	}
	return providerBinding(deploymentsProviderName, deploymentsWorkspace, deploymentsAPIExport, claims)
}

func providerBinding(name, path, export string, claims []any) *unstructured.Unstructured {
	spec := map[string]any{
		"reference": map[string]any{"export": map[string]any{"path": path, "name": export}},
	}
	if len(claims) > 0 {
		spec["permissionClaims"] = claims
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apis.kcp.io/v1alpha2",
		"kind":       "APIBinding",
		"metadata":   map[string]any{"name": name},
		"spec":       spec,
	}}
}

func createBinding(t *testing.T, tenant dynamic.Interface, binding *unstructured.Unstructured) {
	t.Helper()
	if _, err := tenant.Resource(deploymentsAPIBindingGVR).Create(context.Background(), binding, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create APIBinding %q: %v", binding.GetName(), err)
	}
}

func waitBindingBound(t *testing.T, tenant dynamic.Interface, name string) {
	t.Helper()
	if !waitTilt(t, deploymentsTestWait, func() (bool, string) {
		got, err := tenant.Resource(deploymentsAPIBindingGVR).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		phase, _, _ := nestedString(got.Object, "status", "phase")
		return phase == "Bound", fmt.Sprintf("phase=%s conditions=%v", phase, conditionsOf(got.Object))
	}) {
		t.Fatalf("APIBinding %q never reached Bound", name)
	}
}

func createTenantObject(t *testing.T, tenant dynamic.Interface, gvr schema.GroupVersionResource, object *unstructured.Unstructured) {
	t.Helper()
	if !waitTilt(t, deploymentsTestWait, func() (bool, string) {
		_, err := tenant.Resource(gvr).Create(context.Background(), object.DeepCopy(), metav1.CreateOptions{})
		if err == nil || apierrors.IsAlreadyExists(err) {
			return true, ""
		}
		return false, err.Error()
	}) {
		t.Fatalf("create %s %q after APIBinding", gvr.Resource, object.GetName())
	}
}

func permissionClaim(claims []any, group, resource string) (map[string]any, bool) {
	for _, raw := range claims {
		claim, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		claimGroup, _ := claim["group"].(string)
		claimResource, _ := claim["resource"].(string)
		if claimGroup == group && claimResource == resource {
			return claim, true
		}
	}
	return nil, false
}

func stringSlice(value any) []string {
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	values := make([]string, 0, len(raw))
	for _, item := range raw {
		if value, ok := item.(string); ok {
			values = append(values, value)
		}
	}
	return values
}

func nestedString(obj map[string]any, path ...string) (string, bool, error) {
	current := any(obj)
	for _, part := range path {
		mapping, ok := current.(map[string]any)
		if !ok {
			return "", false, fmt.Errorf("%v is not an object", path)
		}
		current, ok = mapping[part]
		if !ok {
			return "", false, nil
		}
	}
	value, ok := current.(string)
	return value, ok, nil
}
