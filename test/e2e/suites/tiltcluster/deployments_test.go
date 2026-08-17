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
	deploymentsReleaseGVR    = schema.GroupVersionResource{Group: deploymentsGroup, Version: "v1alpha1", Resource: "releases"}
	deploymentsDeploymentGVR = schema.GroupVersionResource{Group: deploymentsGroup, Version: "v1alpha1", Resource: "deployments"}
	deploymentsTemplateGVR   = schema.GroupVersionResource{Group: infraGroup, Version: "v1alpha1", Resource: "templates"}
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
	for _, resource := range []string{"releases", "deployments"} {
		if !apiExportHasResource(export.Object, resource, deploymentsGroup) {
			t.Fatalf("APIExport %q missing %s/%s; spec.resources=%v",
				deploymentsAPIExport, deploymentsGroup, resource, nestedSlice(export.Object, "spec", "resources"))
		}
	}

	infraExport, err := kcpAdminDynamic(t, providerWorkspace).
		Resource(apiExportGVR).Get(ctx, infraAPIExportName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get Infrastructure APIExport identity: %v", err)
	}
	infraIdentity, _, _ := nestedString(infraExport.Object, "status", "identityHash")
	if infraIdentity == "" {
		t.Fatal("Infrastructure APIExport has no identityHash")
	}

	wantVerbs := map[string][]string{
		"instances": {"get", "list", "watch", "create", "update", "patch", "delete"},
	}
	claims := nestedSlice(export.Object, "spec", "permissionClaims")
	if len(claims) != len(wantVerbs) {
		t.Fatalf("permissionClaims count = %d, want %d: %v", len(claims), len(wantVerbs), claims)
	}
	for resource, verbs := range wantVerbs {
		claim, ok := permissionClaim(claims, infraGroup, resource)
		if !ok {
			t.Fatalf("missing Infrastructure permission claim for %s", resource)
		}
		if got, _ := claim["identityHash"].(string); got != infraIdentity {
			t.Fatalf("claim %s identityHash = %q, want %q", resource, got, infraIdentity)
		}
		for _, verb := range verbs {
			if !slices.Contains(stringSlice(claim["verbs"]), verb) {
				t.Fatalf("claim %s missing verb %q: %v", resource, verb, claim["verbs"])
			}
		}
	}

	t.Logf("deployments provider registered and ready: APIExport=%s identity=%s", deploymentsAPIExport, infraIdentity)
}

// TestDeploymentsProviderReconcilesTenantDeployment proves the controller's
// effective tenant authority, not only its registration metadata. A fresh
// workspace binds Infrastructure and Deployments with accepted claims, then a
// Release/Deployment must materialize an Infrastructure Instance. Deleting
// the default-Retain Deployment must detach, not delete, that Instance.
func TestDeploymentsProviderReconcilesTenantDeployment(t *testing.T) {
	requireStack(t)
	ctx := context.Background()

	infraExport, err := kcpAdminDynamic(t, providerWorkspace).
		Resource(apiExportGVR).Get(ctx, infraAPIExportName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get Infrastructure APIExport identity: %v", err)
	}
	infraIdentity, _, _ := nestedString(infraExport.Object, "status", "identityHash")
	if infraIdentity == "" {
		t.Fatal("Infrastructure APIExport has no identityHash")
	}

	workspaceName := "e2e-deployments-" + shortNonce()
	parent := kcpAdminDynamic(t, "root:faros")
	workspacePath := createDeploymentsWorkspace(t, parent, workspaceName)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := parent.Resource(deploymentsWorkspaceGVR).Delete(cleanupCtx, workspaceName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("cleanup deployments workspace %q: %v", workspaceName, err)
		}
		waitTiltResourceGone(t, parent.Resource(deploymentsWorkspaceGVR), workspaceName, time.Minute)
	})

	tenant := kcpAdminDynamic(t, workspacePath)
	createBinding(t, tenant, infrastructureBinding())
	waitBindingBound(t, tenant, "infrastructure")
	createBinding(t, tenant, deploymentsBinding(infraIdentity))
	waitBindingBound(t, tenant, deploymentsProviderName)

	if !waitTilt(t, deploymentsTestWait, func() (bool, string) {
		_, err := tenant.Resource(deploymentsTemplateGVR).Get(ctx, "application", metav1.GetOptions{})
		return err == nil, fmt.Sprintf("application Template: %v", err)
	}) {
		t.Fatalf("application Template was not visible in tenant %s", workspacePath)
	}

	releaseName := "release-" + shortNonce()
	deploymentName := "deployment-" + shortNonce()
	release := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": deploymentsGroup + "/v1alpha1",
		"kind":       "Release",
		"metadata":   map[string]any{"name": releaseName},
		"spec": map[string]any{
			"source":       map[string]any{"repositoryRef": "e2e-repository", "revision": "0123456789abcdef"},
			"blueprintRef": map[string]any{"name": "application"},
			"artifacts": []any{
				map[string]any{"name": "web", "image": "ghcr.io/faroshq/faros-scaffold-application/web:v0.1.3"},
				map[string]any{"name": "api", "image": "ghcr.io/faroshq/faros-scaffold-application/api:v0.1.3"},
			},
		},
	}}
	createTenantObject(t, tenant, deploymentsReleaseGVR, release)
	deployment := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": deploymentsGroup + "/v1alpha1",
		"kind":       "Deployment",
		"metadata":   map[string]any{"name": deploymentName},
		"spec": map[string]any{
			"releaseRef": releaseName,
			"className":  "kro-direct",
			"rolloutID":  "e2e-rollout-1",
			"configuration": map[string]any{
				"database": map[string]any{"version": "16"},
				"oidc":     map[string]any{"mode": "none"},
			},
		},
	}}
	createTenantObject(t, tenant, deploymentsDeploymentGVR, deployment)

	if !waitTilt(t, deploymentsTestWait, func() (bool, string) {
		instance, err := tenant.Resource(deploymentsInstanceGVR).Get(ctx, deploymentName, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		template, _, _ := nestedString(instance.Object, "spec", "template")
		values, _, _ := unstructured.NestedMap(instance.Object, "spec", "values")
		if template != "application" ||
			values["webImage"] != "ghcr.io/faroshq/faros-scaffold-application/web:v0.1.3" ||
			values["apiImage"] != "ghcr.io/faroshq/faros-scaffold-application/api:v0.1.3" ||
			values["farosRedeployRevision"] != "e2e-rollout-1" {
			return false, fmt.Sprintf("instance template=%q values not projected: %#v", template, values)
		}
		current, err := tenant.Resource(deploymentsDeploymentGVR).Get(ctx, deploymentName, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		backendName, _, _ := nestedString(current.Object, "status", "backendRef", "name")
		rollout, _, _ := nestedString(current.Object, "status", "observedRolloutID")
		return backendName == deploymentName && rollout == "e2e-rollout-1",
			fmt.Sprintf("backend=%q rollout=%q", backendName, rollout)
	}) {
		t.Fatalf("Deployment %q did not materialize its Infrastructure Instance", deploymentName)
	}

	if err := tenant.Resource(deploymentsDeploymentGVR).Delete(ctx, deploymentName, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete retained Deployment %q: %v", deploymentName, err)
	}
	waitTiltResourceGone(t, tenant.Resource(deploymentsDeploymentGVR), deploymentName, deploymentsTestWait)
	instance, err := tenant.Resource(deploymentsInstanceGVR).Get(ctx, deploymentName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Retain policy removed Instance %q: %v", deploymentName, err)
	}
	if instance.GetLabels()["deployments.faros.sh/deployment"] != "" {
		t.Fatalf("retained Instance still carries Deployment ownership label: %v", instance.GetLabels())
	}
	if instance.GetAnnotations()["deployments.faros.sh/last-applied-spec"] != "" {
		t.Fatalf("retained Instance still carries managed-spec ownership: %v", instance.GetAnnotations())
	}
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

func infrastructureBinding() *unstructured.Unstructured {
	return providerBinding("infrastructure", providerWorkspace, infraAPIExportName, nil)
}

func deploymentsBinding(identityHash string) *unstructured.Unstructured {
	verbs := map[string][]string{
		"instances": {"get", "list", "watch", "create", "update", "patch", "delete"},
	}
	claims := make([]any, 0, len(verbs))
	for _, resource := range []string{"instances"} {
		claimVerbs := make([]any, len(verbs[resource]))
		for i, verb := range verbs[resource] {
			claimVerbs[i] = verb
		}
		claims = append(claims, map[string]any{
			"group":        infraGroup,
			"resource":     resource,
			"verbs":        claimVerbs,
			"identityHash": identityHash,
			"selector":     map[string]any{"matchAll": true},
			"state":        "Accepted",
		})
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
		if ok && claim["group"] == group && claim["resource"] == resource {
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
