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
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const (
	codeProviderName = "code"
	codeWorkspace    = "root:faros:providers:code"
	codeGroup        = "code.faros.sh"
	codeAPIExport    = "code.providers.faros.sh"
)

var (
	codeConnectionGVR            = schema.GroupVersionResource{Group: codeGroup, Version: "v1alpha1", Resource: "connections"}
	codeRepositoryGVR            = schema.GroupVersionResource{Group: codeGroup, Version: "v1alpha1", Resource: "repositories"}
	codeChangeRequestGVR         = schema.GroupVersionResource{Group: codeGroup, Version: "v1alpha1", Resource: "changerequests"}
	deploymentsRepositorySyncGVR = schema.GroupVersionResource{Group: deploymentsGroup, Version: "v1alpha1", Resource: "repositorysyncs"}
	coreNamespaceGVR             = schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}
	coreSecretGVR                = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
	coreConfigMapGVR             = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
)

// TestReviewedGitConfigurationReachesReadyInstance proves target-neutral Git
// reconciliation. A local GitHub-compatible host reports one approved change
// request; Code merges it, then RepositorySync applies both an Infrastructure
// Instance and a native ConfigMap. Infrastructure readiness is asserted
// separately from RepositorySync's applied-state contract.
//
// The mock is only the external Git host boundary. All provider controllers,
// APIBindings, permission claims, status transitions, and runtime reconciliation
// are the processes from the running Tilt stack.
func TestReviewedGitConfigurationReachesReadyInstance(t *testing.T) {
	requireStack(t)
	if !httpOK(envOr("FAROS_E2E_CODE_URL", "http://localhost:8083")+"/healthz", 5*time.Second) {
		t.Skip("Code provider is not running; trigger code-register, code-init, and code in Tilt")
	}
	ctx := context.Background()
	runtimeClient := infrastructureRuntimeClient(t)
	gitHost := newPromotionGitHost(t)

	workspaceName := "e2e-gitops-" + shortNonce()
	parent := kcpAdminDynamic(t, "root:faros")
	workspacePath := createDeploymentsWorkspace(t, parent, workspaceName)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := parent.Resource(deploymentsWorkspaceGVR).Delete(cleanupCtx, workspaceName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("cleanup GitOps workspace %q: %v", workspaceName, err)
		}
		waitTiltResourceGone(t, parent.Resource(deploymentsWorkspaceGVR), workspaceName, time.Minute)
	})

	tenant := kcpAdminDynamic(t, workspacePath)
	infraExport := mustAPIExport(t, providerWorkspace, infraAPIExportName)
	infraIdentity, _, _ := nestedString(infraExport.Object, "status", "identityHash")
	if infraIdentity == "" {
		t.Fatal("Infrastructure APIExport has no identityHash")
	}
	codeExport := mustAPIExport(t, codeWorkspace, codeAPIExport)
	codeIdentity, _, _ := nestedString(codeExport.Object, "status", "identityHash")
	if codeIdentity == "" {
		t.Fatal("Code APIExport has no identityHash")
	}
	// Infrastructure reads optional per-instance registry credentials from the
	// tenant workspace. Accept its advertised Secret claim so the runtime
	// readiness assertion exercises a fully authorized target provider.
	createBinding(t, tenant, bindingAcceptingExportClaims(providerName, providerWorkspace, infraAPIExportName, infraExport))
	waitBindingBound(t, tenant, "infrastructure")
	createBinding(t, tenant, deploymentsBinding(infraIdentity, codeIdentity))
	waitBindingBound(t, tenant, deploymentsProviderName)
	createBinding(t, tenant, bindingAcceptingExportClaims(codeProviderName, codeWorkspace, codeAPIExport, codeExport))
	waitBindingBound(t, tenant, codeProviderName)

	ensureDefaultNamespace(t, tenant)
	secret := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   map[string]any{"name": "git-token", "namespace": "default"},
		"type":       "Opaque",
		"data":       map[string]any{"token": "dG9rZW4="},
	}}
	if !waitTilt(t, deploymentsTestWait, func() (bool, string) {
		_, err := tenant.Resource(coreSecretGVR).Namespace("default").Create(ctx, secret.DeepCopy(), metav1.CreateOptions{})
		return err == nil || apierrors.IsAlreadyExists(err), fmt.Sprintf("create credential Secret: %v", err)
	}) {
		t.Fatal("create credential Secret after Code permission claim became active")
	}
	createTenantObject(t, tenant, codeConnectionGVR, &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": codeGroup + "/v1alpha1",
		"kind":       "Connection",
		"metadata":   map[string]any{"name": "git"},
		"spec": map[string]any{
			"provider": "github", "type": "pat", "owner": "acme", "baseURL": gitHost.URL,
			"secretRef": map[string]any{"name": "git-token", "namespace": "default", "key": "token"},
		},
	}})
	createTenantObject(t, tenant, codeRepositoryGVR, &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": codeGroup + "/v1alpha1",
		"kind":       "Repository",
		"metadata":   map[string]any{"name": "application"},
		"spec": map[string]any{
			"connectionRef": "git", "name": "application", "owner": "acme", "visibility": "private", "defaultBranch": "main",
		},
	}})
	waitCodeReady(t, tenant, codeConnectionGVR, "git", "Validated")
	waitCodeReady(t, tenant, codeRepositoryGVR, "application", "Ready")

	changeRequestName := "promote-" + shortNonce()
	createTenantObject(t, tenant, codeChangeRequestGVR, &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": codeGroup + "/v1alpha1",
		"kind":       "ChangeRequest",
		"metadata":   map[string]any{"name": changeRequestName},
		"spec": map[string]any{
			"repositoryRef": "application", "baseBranch": "main", "headBranch": "faros/promote-e2e",
			"title": "Promote reviewed release", "mergePolicy": "AfterApproval", "requiredApprovals": int64(1),
		},
	}})
	if !waitTilt(t, deploymentsTestWait, func() (bool, string) {
		cr, err := tenant.Resource(codeChangeRequestGVR).Get(ctx, changeRequestName, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		phase, _, _ := nestedString(cr.Object, "status", "phase")
		mergeSHA, _, _ := nestedString(cr.Object, "status", "mergeSHA")
		return phase == "Merged" && mergeSHA == promotionGitRevision,
			fmt.Sprintf("phase=%q mergeSHA=%q conditions=%v", phase, mergeSHA, conditionsOf(cr.Object))
	}) {
		t.Fatalf("ChangeRequest %q was not approved and merged", changeRequestName)
	}

	syncName := "production-" + shortNonce()
	createTenantObject(t, tenant, deploymentsRepositorySyncGVR, &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": deploymentsGroup + "/v1alpha1",
		"kind":       "RepositorySync",
		"metadata":   map[string]any{"name": syncName},
		"spec": map[string]any{
			"repositoryRef": "application", "ref": "main", "path": ".faros", "intervalSeconds": int64(10), "prune": true,
		},
	}})
	if !waitTilt(t, deploymentsTestWait, func() (bool, string) {
		syncObj, err := tenant.Resource(deploymentsRepositorySyncGVR).Get(ctx, syncName, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		phase, _, _ := nestedString(syncObj.Object, "status", "phase")
		applied, _, _ := nestedString(syncObj.Object, "status", "appliedRevision")
		observed, _, _ := unstructured.NestedInt64(syncObj.Object, "status", "observedGeneration")
		return phase == "Synced" && applied == promotionGitRevision && observed == syncObj.GetGeneration() && conditionTrue(syncObj.Object, "Applied"),
			fmt.Sprintf("phase=%q appliedRevision=%q observedGeneration=%d/%d conditions=%v", phase, applied, observed, syncObj.GetGeneration(), conditionsOf(syncObj.Object))
	}) {
		t.Fatalf("RepositorySync %q did not apply reviewed revision", syncName)
	}

	instance, err := tenant.Resource(deploymentsInstanceGVR).Get(ctx, promotionInstanceName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get synced Infrastructure Instance %q: %v", promotionInstanceName, err)
	}
	template, _, _ := nestedString(instance.Object, "spec", "template")
	values, _, _ := unstructured.NestedMap(instance.Object, "spec", "values")
	database, _, _ := unstructured.NestedMap(instance.Object, "spec", "values", "database")
	oidc, _, _ := unstructured.NestedMap(instance.Object, "spec", "values", "oidc")
	if template != "application" || values["name"] != promotionInstanceName || values["farosMode"] != "production" ||
		values["farosRedeployRevision"] != promotionGitRevision || values["webImage"] != promotionWebImage || values["apiImage"] != promotionAPIImage ||
		database["version"] != "16" || oidc["mode"] != "none" {
		t.Fatalf("synced Instance does not match reviewed desired state: template=%q values=%#v", template, values)
	}

	configMap, err := tenant.Resource(coreConfigMapGVR).Namespace("default").Get(ctx, promotionConfigMapName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get synced ConfigMap %q: %v", promotionConfigMapName, err)
	}
	data, _, _ := unstructured.NestedStringMap(configMap.Object, "data")
	if data["source"] != "repositorysync" || data["revision"] != promotionGitRevision {
		t.Fatalf("synced ConfigMap data = %v, want source and reviewed revision", data)
	}

	instance = waitInfrastructureInstanceReady(t, tenant, promotionInstanceName)
	runtimeRef := deploymentRuntimeTarget(t, instance)
	t.Logf("reviewed Git revision %s was synced to Instance %q, ConfigMap %q, and Ready runtime %s/%s",
		promotionGitRevision, instance.GetName(), promotionConfigMapName, runtimeRef.namespace, runtimeRef.name)

	if err := tenant.Resource(deploymentsRepositorySyncGVR).Delete(ctx, syncName, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete RepositorySync %q: %v", syncName, err)
	}
	waitTiltResourceGone(t, tenant.Resource(deploymentsRepositorySyncGVR), syncName, deploymentsTestWait)
	waitTiltResourceGone(t, tenant.Resource(deploymentsInstanceGVR), promotionInstanceName, deploymentsTestWait)
	waitTiltResourceGone(t, tenant.Resource(coreConfigMapGVR).Namespace("default"), promotionConfigMapName, deploymentsTestWait)
	waitTiltResourceGone(t, runtimeClient.Resource(runtimeRef.gvr).Namespace(runtimeRef.namespace), runtimeRef.name, deploymentsTestWait)
}

const (
	promotionGitRevision   = "merge-e2e-0123456789abcdef"
	promotionInstanceName  = "gitops-e2e-production"
	promotionConfigMapName = "gitops-e2e-config"
	promotionWebImage      = "ghcr.io/faroshq/faros-scaffold-application/web:v0.1.3"
	promotionAPIImage      = "ghcr.io/faroshq/faros-scaffold-application/api:v0.1.3"
)

type promotionGitHost struct {
	*httptest.Server
	mu     sync.RWMutex
	merged bool
}

func newPromotionGitHost(t *testing.T) *promotionGitHost {
	t.Helper()
	host := &promotionGitHost{}
	host.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/user":
			w.Header().Set("X-OAuth-Scopes", "repo")
			_, _ = w.Write([]byte(`{"login":"e2e-user"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/repos/acme/application":
			_, _ = w.Write([]byte(`{"id":1,"name":"application","full_name":"acme/application","default_branch":"main","html_url":"https://git.example/acme/application","clone_url":"https://git.example/acme/application.git","ssh_url":"git@git.example:acme/application.git"}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v3/repos/acme/application":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/repos/acme/application/pulls":
			_, _ = w.Write([]byte(`[{"number":7}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/repos/acme/application/pulls/7":
			host.mu.RLock()
			merged := host.merged
			host.mu.RUnlock()
			if merged {
				_, _ = w.Write([]byte(`{"number":7,"html_url":"https://git.example/acme/application/pull/7","state":"closed","merged":true,"merge_commit_sha":"` + promotionGitRevision + `","head":{"sha":"head-e2e"}}`))
			} else {
				_, _ = w.Write([]byte(`{"number":7,"html_url":"https://git.example/acme/application/pull/7","state":"open","merged":false,"head":{"sha":"head-e2e"}}`))
			}
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/repos/acme/application/pulls/7/reviews":
			_, _ = w.Write([]byte(`[{"state":"APPROVED","user":{"id":42}}]`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v3/repos/acme/application/pulls/7/merge":
			host.mu.Lock()
			host.merged = true
			host.mu.Unlock()
			_, _ = w.Write([]byte(`{"merged":true,"sha":"` + promotionGitRevision + `","message":"merged"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/repos/acme/application/commits/main":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(promotionGitRevision))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/repos/acme/application/git/trees/"+promotionGitRevision:
			_, _ = w.Write([]byte(`{"sha":"` + promotionGitRevision + `","truncated":false,"tree":[` +
				`{"path":".faros/instance.yaml","type":"blob","sha":"instance-blob","size":500},` +
				`{"path":".faros/configmap.yaml","type":"blob","sha":"configmap-blob","size":300}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/repos/acme/application/git/blobs/instance-blob":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte(promotionInstanceYAML))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v3/repos/acme/application/git/blobs/configmap-blob":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte(promotionConfigMapYAML))
		default:
			http.Error(w, fmt.Sprintf("unexpected request %s %s", r.Method, r.URL.RequestURI()), http.StatusNotFound)
		}
	}))
	t.Cleanup(host.Close)
	return host
}

const promotionInstanceYAML = `apiVersion: infrastructure.faros.sh/v1alpha1
kind: Instance
metadata:
  name: gitops-e2e-production
spec:
  template: application
  values:
    name: gitops-e2e-production
    farosMode: production
    farosRedeployRevision: merge-e2e-0123456789abcdef
    webImage: ghcr.io/faroshq/faros-scaffold-application/web:v0.1.3
    apiImage: ghcr.io/faroshq/faros-scaffold-application/api:v0.1.3
    database:
      version: "16"
    oidc:
      mode: none
`

const promotionConfigMapYAML = `apiVersion: v1
kind: ConfigMap
metadata:
  name: gitops-e2e-config
  namespace: default
data:
  source: repositorysync
  revision: merge-e2e-0123456789abcdef
`

func mustAPIExport(t *testing.T, workspace, name string) *unstructured.Unstructured {
	t.Helper()
	export, err := kcpAdminDynamic(t, workspace).Resource(apiExportGVR).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get APIExport %q in %s: %v", name, workspace, err)
	}
	return export
}

func bindingAcceptingExportClaims(name, workspace, exportName string, export *unstructured.Unstructured) *unstructured.Unstructured {
	claims := make([]any, 0)
	for _, raw := range nestedSlice(export.Object, "spec", "permissionClaims") {
		claim, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		accepted := map[string]any{
			"group": claim["group"], "resource": claim["resource"], "verbs": claim["verbs"],
			"selector": map[string]any{"matchAll": true}, "state": "Accepted",
		}
		if identityHash, ok := claim["identityHash"].(string); ok && identityHash != "" {
			accepted["identityHash"] = identityHash
		}
		claims = append(claims, accepted)
	}
	return providerBinding(name, workspace, exportName, claims)
}

func ensureDefaultNamespace(t *testing.T, tenant dynamic.Interface) {
	t.Helper()
	ns := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": "default"},
	}}
	if _, err := tenant.Resource(coreNamespaceGVR).Create(context.Background(), ns, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create default namespace: %v", err)
	}
}

func waitCodeReady(t *testing.T, tenant dynamic.Interface, gvr schema.GroupVersionResource, name, condition string) {
	t.Helper()
	if !waitTilt(t, deploymentsTestWait, func() (bool, string) {
		obj, err := tenant.Resource(gvr).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		observed, _, _ := unstructured.NestedInt64(obj.Object, "status", "observedGeneration")
		return observed == obj.GetGeneration() && conditionTrue(obj.Object, condition),
			fmt.Sprintf("observedGeneration=%d/%d conditions=%v", observed, obj.GetGeneration(), conditionsOf(obj.Object))
	}) {
		t.Fatalf("%s %q did not become current and %s=True", gvr.Resource, name, condition)
	}
}
