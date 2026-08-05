// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package provideractions

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const (
	testProject = "provider-actions-e2e"
	testAlias   = "taxi"
	tableRef    = "taxi-trips"
	pat         = "e2e-pat-not-an-app-input"
)

func TestProviderActionQueryThroughGeneratedNodeSDK(t *testing.T) {
	requireLocalSuite(t)
	orgUUID, workspaceUUID, tenantCluster := setupTenantWorkspace(t)
	tenant := kcpDynamic(t, tenantCluster, staticToken)

	bindProvider(t, tenant, "databricks", databricksWorkspace, databricksExport, []string{"get"})
	bindProvider(t, tenant, "app-studio", appStudioWorkspace, appStudioExport, []string{"get", "list", "watch", "create", "update", "delete"})
	waitTenantProxyContext(t, orgUUID, workspaceUUID)
	tenantHeaders := map[string]string{"X-Kedge-Org": orgUUID, "X-Kedge-Workspace": workspaceUUID}

	secret := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Secret",
		"metadata": map[string]any{"name": "e2e-databricks-token", "namespace": "default"},
		"type":     "Opaque",
		"data":     map[string]any{"token": base64.StdEncoding.EncodeToString([]byte(pat))},
	}}
	createOrUpdate(t, tenant.Resource(secretGVR).Namespace("default"), secret)
	t.Cleanup(func() {
		_ = tenant.Resource(secretGVR).Namespace("default").Delete(context.Background(), secret.GetName(), metav1.DeleteOptions{})
	})

	connection := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "databricks.kedge.faros.sh/v1alpha1", "kind": "Connection",
		"metadata": map[string]any{"name": "e2e-databricks-connection"},
		"spec": map[string]any{
			"host": fakeDB.URL(), "authType": "pat",
			"secretRef": map[string]any{"name": secret.GetName(), "namespace": "default", "key": "token"},
		},
	}}
	createOrUpdate(t, tenant.Resource(connectionGVR), connection)
	t.Cleanup(func() {
		_ = tenant.Resource(connectionGVR).Delete(context.Background(), connection.GetName(), metav1.DeleteOptions{})
	})
	waitCondition(t, 90*time.Second, func() (bool, string) {
		return readyCondition(tenant, connectionGVR, connection.GetName(), "Ready")
	}, "Connection Ready")

	warehouse := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "databricks.kedge.faros.sh/v1alpha1", "kind": "Warehouse",
		"metadata": map[string]any{"name": "e2e-databricks-warehouse"},
		"spec":     map[string]any{"connectionRef": connection.GetName(), "warehouseID": "e2e-warehouse"},
	}}
	createOrUpdate(t, tenant.Resource(warehouseGVR), warehouse)
	t.Cleanup(func() {
		_ = tenant.Resource(warehouseGVR).Delete(context.Background(), warehouse.GetName(), metav1.DeleteOptions{})
	})
	waitCondition(t, 90*time.Second, func() (bool, string) {
		return readyCondition(tenant, warehouseGVR, warehouse.GetName(), "Ready")
	}, "Warehouse Ready")

	table := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "databricks.kedge.faros.sh/v1alpha1", "kind": "Table",
		"metadata": map[string]any{"name": tableRef},
		"spec": map[string]any{
			"connectionRef": connection.GetName(), "warehouseRef": warehouse.GetName(),
			"catalog": "analytics", "schema": "gold", "table": "taxi_trips",
		},
	}}
	createOrUpdate(t, tenant.Resource(tableGVR), table)
	t.Cleanup(func() {
		_ = tenant.Resource(tableGVR).Delete(context.Background(), table.GetName(), metav1.DeleteOptions{})
	})
	waitCondition(t, 90*time.Second, func() (bool, string) {
		return readyCondition(tenant, tableGVR, table.GetName(), "Ready")
	}, "Table Ready")

	project := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "ai.kedge.faros.sh/v1alpha1", "kind": "Project",
		"metadata": map[string]any{"name": testProject},
		"spec":     map[string]any{"displayName": "Provider actions E2E"},
	}}
	createOrUpdate(t, tenant.Resource(projectGVR), project)
	t.Cleanup(func() {
		_ = tenant.Resource(projectGVR).Delete(context.Background(), project.GetName(), metav1.DeleteOptions{})
	})

	addBody := map[string]any{
		"environment": "development",
		"alias":       testAlias,
		"provider":    "databricks",
		"resourceRef": map[string]any{
			"name": tableRef, "apiVersion": "databricks.kedge.faros.sh/v1alpha1", "kind": "Table", "resource": "tables",
		},
		"allowedActions": []any{map[string]any{"name": "query_table", "version": "v1"}},
	}
	status, body := postJSON(t, hubURL+"/services/providers/app-studio/api/projects/"+testProject+"/integrations", staticToken, addBody, tenantHeaders)
	if status != http.StatusCreated {
		t.Fatalf("add project integration: status=%d body=%s", status, body)
	}
	assertProjectReferenceBinding(t, tenant, table)

	input := map[string]any{"columns": []any{"trip_id", "fare_amount"}, "limit": 2}
	stdout, stderr, err := runGeneratedApp(t, hubURL, testProject, testAlias, staticToken, "query_table/v1", input, tenantHeaders)
	if err != nil {
		t.Fatalf("generated app query failed: %v (stderr=%s)", err, stderr)
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("decode generated app result: %v (stdout=%s)", err, stdout)
	}
	if got := result["actionVersion"]; got != "v1" {
		t.Fatalf("actionVersion=%v, want v1", got)
	}
	if got := result["tableRef"]; got != tableRef {
		t.Fatalf("tableRef=%v, want %s", got, tableRef)
	}
	rows, ok := result["rows"].([]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("rows=%#v, want exactly two bounded rows", result["rows"])
	}
	wantRows := []any{
		map[string]any{"trip_id": float64(101), "fare_amount": 18.25},
		map[string]any{"trip_id": float64(202), "fare_amount": 27.50},
	}
	if !reflect.DeepEqual(rows, wantRows) {
		t.Fatalf("rows=%#v, want %#v", rows, wantRows)
	}

	selectRequest, ok := fakeDB.LastSelect()
	if !ok {
		t.Fatal("fake upstream received no SELECT statement")
	}
	wantSQL := "SELECT `trip_id`, `fare_amount` FROM `analytics`.`gold`.`taxi_trips` LIMIT 2"
	if selectRequest.Statement != wantSQL {
		t.Fatalf("fake SELECT=%q, want %q", selectRequest.Statement, wantSQL)
	}
	if selectRequest.WarehouseID != "e2e-warehouse" {
		t.Fatalf("fake warehouse_id=%q, want e2e-warehouse", selectRequest.WarehouseID)
	}
	if selectRequest.Authorization != "Bearer "+pat {
		t.Fatalf("fake Authorization=%q, want provider-resolved PAT", selectRequest.Authorization)
	}
	var sawDescribe bool
	for _, request := range fakeDB.Requests() {
		if request.Statement == "DESCRIBE TABLE `analytics`.`gold`.`taxi_trips`" {
			sawDescribe = true
		}
	}
	if !sawDescribe {
		t.Fatal("fake upstream did not receive the Table schema DESCRIBE target")
	}
	for _, unsafe := range []string{fakeDB.URL(), pat} {
		if strings.Contains(stdout, unsafe) || strings.Contains(stderr, unsafe) {
			t.Fatalf("generated app output leaked provider URL or PAT: %q", unsafe)
		}
	}
	writeEvidence("invocation.json", map[string]any{
		"surface": "generated-app -> @kedge/actions-node -> App Studio integration gateway -> hub MCP",
		"project": testProject, "integration": testAlias, "action": "query_table/v1",
		"input": input, "directProviderURL": false, "patInInput": false,
	})
	writeEvidence("result.json", map[string]any{
		"actionVersion": result["actionVersion"], "tableRef": result["tableRef"], "rows": rows,
		"fakeStatement": selectRequest.Statement, "warehouseID": selectRequest.WarehouseID,
		"interactionVerified": true,
	})

	beforeFailures := len(fakeDB.Requests())
	if _, stderr, err := runGeneratedApp(t, hubURL, testProject, "unbound", staticToken, "query_table/v1", input, tenantHeaders); err == nil {
		t.Fatalf("unbound integration unexpectedly succeeded (stderr=%s)", stderr)
	} else if !strings.Contains(stderr, "404") {
		t.Fatalf("unbound integration did not fail closed with 404: %s", stderr)
	}
	if _, stderr, err := runGeneratedApp(t, hubURL, testProject, testAlias, staticToken, "query_table/v2", input, tenantHeaders); err == nil {
		t.Fatalf("unsupported action version unexpectedly succeeded (stderr=%s)", stderr)
	} else if !strings.Contains(stderr, "403") {
		t.Fatalf("unsupported action version did not fail closed with 403: %s", stderr)
	}
	patchBody := map[string]any{"allowedActions": []any{map[string]any{"name": "query_table", "version": "v1", "revoked": true}}}
	status, body = patchJSON(t, hubURL+"/services/providers/app-studio/api/projects/"+testProject+"/integrations/"+testAlias, staticToken, patchBody, tenantHeaders)
	if status != http.StatusOK {
		t.Fatalf("revoke integration: status=%d body=%s", status, body)
	}
	if _, stderr, err := runGeneratedApp(t, hubURL, testProject, testAlias, staticToken, "query_table/v1", input, tenantHeaders); err == nil {
		t.Fatalf("revoked action unexpectedly succeeded (stderr=%s)", stderr)
	} else if !strings.Contains(stderr, "403") {
		t.Fatalf("revoked action did not fail closed with 403: %s", stderr)
	}
	if got := len(fakeDB.Requests()); got != beforeFailures {
		t.Fatalf("fail-closed calls reached fake upstream: request count %d before %d", got, beforeFailures)
	}
}

// TestOptionalLiveProviderActionSDK is intentionally opt-in. It exercises the
// same generated-app process against an existing local hub/project without
// creating resources or talking to a provider URL directly.
func TestOptionalLiveProviderActionSDK(t *testing.T) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("KEDGE_E2E_PROVIDER_ACTIONS_LIVE")), "true") {
		t.Skip("set KEDGE_E2E_PROVIDER_ACTIONS_LIVE=true for the bounded live smoke")
	}
	hub := strings.TrimRight(strings.TrimSpace(os.Getenv("KEDGE_LIVE_HUB_URL")), "/")
	project := strings.TrimSpace(os.Getenv("KEDGE_LIVE_PROJECT"))
	token := os.Getenv("KEDGE_LIVE_CALLER_TOKEN")
	if hub == "" || project == "" || token == "" {
		t.Skip("KEDGE_LIVE_HUB_URL, KEDGE_LIVE_PROJECT, and KEDGE_LIVE_CALLER_TOKEN are required")
	}
	alias := strings.TrimSpace(os.Getenv("KEDGE_LIVE_ACTION_ALIAS"))
	if alias == "" {
		alias = testAlias
	}
	var tenantHeaders map[string]string
	if org, workspace := strings.TrimSpace(os.Getenv("KEDGE_LIVE_ORG")), strings.TrimSpace(os.Getenv("KEDGE_LIVE_WORKSPACE")); org != "" && workspace != "" {
		tenantHeaders = map[string]string{"X-Kedge-Org": org, "X-Kedge-Workspace": workspace}
	}
	stdout, stderr, err := runGeneratedApp(t, hub, project, alias, token, "query_table/v1", map[string]any{"limit": 2}, tenantHeaders)
	if err != nil {
		t.Fatalf("live generated app query failed: %v (stderr=%s)", err, stderr)
	}
	var result struct {
		Rows []map[string]any `json:"rows"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("decode live generated app output: %v", err)
	}
	if len(result.Rows) > 2 {
		t.Fatalf("live result returned %d rows, want bounded <=2", len(result.Rows))
	}
	if strings.Contains(stdout, token) || strings.Contains(stderr, token) {
		t.Fatal("live generated app output leaked caller token")
	}
	t.Logf("live interaction verified through App Studio SDK: rows=%d; provider URL/PAT were not supplied to the app", len(result.Rows))
}

func bindProvider(t *testing.T, tenant dynamic.Interface, name, workspace, export string, secretVerbs []string) {
	t.Helper()
	claims := make([]any, 0, 1)
	if len(secretVerbs) > 0 {
		verbs := make([]any, 0, len(secretVerbs))
		for _, verb := range secretVerbs {
			verbs = append(verbs, verb)
		}
		claims = append(claims, map[string]any{
			"resource": "secrets", "verbs": verbs,
			"selector": map[string]any{"matchAll": true}, "state": "Accepted",
		})
	}
	binding := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apis.kcp.io/v1alpha2", "kind": "APIBinding",
		"metadata": map[string]any{"name": name},
		"spec": map[string]any{
			"reference":        map[string]any{"export": map[string]any{"path": workspace, "name": export}},
			"permissionClaims": claims,
		},
	}}
	createOrUpdate(t, tenant.Resource(apiBindingGVR), binding)
	waitCondition(t, 90*time.Second, func() (bool, string) {
		obj, err := tenant.Resource(apiBindingGVR).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return false, err.Error()
		}
		phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
		ready := false
		conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
		for _, raw := range conditions {
			condition, _ := raw.(map[string]any)
			if condition["type"] == "Ready" && condition["status"] == "True" {
				ready = true
				break
			}
		}
		return phase == "Bound" && ready, fmt.Sprintf("phase=%s ready=%t", phase, ready)
	}, "APIBinding "+name+" Bound")
}

func assertProjectReferenceBinding(t *testing.T, tenant dynamic.Interface, table *unstructured.Unstructured) {
	t.Helper()
	project, err := tenant.Resource(projectGVR).Get(context.Background(), testProject, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	envs, ok, err := unstructured.NestedSlice(project.Object, "spec", "environments")
	if err != nil || !ok || len(envs) != 1 {
		t.Fatalf("project environments=%#v (err=%v), want one live environment", envs, err)
	}
	env, _ := envs[0].(map[string]any)
	bindings, _, _ := unstructured.NestedSlice(env, "bindings")
	if len(bindings) != 1 {
		t.Fatalf("project bindings=%#v, want one providerReference", bindings)
	}
	binding, _ := bindings[0].(map[string]any)
	if binding["kind"] != "providerReference" {
		t.Fatalf("binding kind=%v, want providerReference", binding["kind"])
	}
	ref, _, _ := unstructured.NestedMap(binding, "resourceRef")
	if ref["name"] != tableRef || ref["kind"] != "Table" {
		t.Fatalf("binding resourceRef=%#v, want existing Table %s", ref, tableRef)
	}
	latestTable, err := tenant.Resource(tableGVR).Get(context.Background(), table.GetName(), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get bound Table: %v", err)
	}
	if owners := latestTable.GetOwnerReferences(); len(owners) != 0 {
		t.Fatalf("provider-owned Table unexpectedly gained App Studio owner references: %#v", owners)
	}
}

func createOrUpdate(t *testing.T, resource dynamic.ResourceInterface, object *unstructured.Unstructured) {
	t.Helper()
	created, err := resource.Create(context.Background(), object, metav1.CreateOptions{})
	if err == nil {
		*object = *created
		return
	}
	if !apierrors.IsAlreadyExists(err) {
		t.Fatalf("create %s/%s: %v", object.GetKind(), object.GetName(), err)
	}
	existing, err := resource.Get(context.Background(), object.GetName(), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get existing %s/%s: %v", object.GetKind(), object.GetName(), err)
	}
	object.SetResourceVersion(existing.GetResourceVersion())
	updated, err := resource.Update(context.Background(), object, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("update %s/%s: %v", object.GetKind(), object.GetName(), err)
	}
	*object = *updated
}

func readyCondition(client dynamic.Interface, resource schema.GroupVersionResource, name, conditionType string) (bool, string) {
	object, err := client.Resource(resource).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return false, err.Error()
	}
	conditions, _, _ := unstructured.NestedSlice(object.Object, "status", "conditions")
	for _, raw := range conditions {
		condition, _ := raw.(map[string]any)
		if condition["type"] == conditionType {
			return condition["status"] == "True", fmt.Sprintf("%s=%v reason=%v", conditionType, condition["status"], condition["reason"])
		}
	}
	return false, "condition absent"
}

func waitCondition(t *testing.T, timeout time.Duration, check func() (bool, string), label string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last := ""
	for time.Now().Before(deadline) {
		ok, msg := check()
		if ok {
			return
		}
		last = msg
		time.Sleep(time.Second)
	}
	t.Fatalf("%s never became ready: %s", label, last)
}

func runGeneratedApp(t *testing.T, hub, project, alias, token, action string, input map[string]any, tenantHeaders ...map[string]string) (string, string, error) {
	t.Helper()
	b, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("encode generated app input: %v", err)
	}
	script := filepath.Join(repoRoot, "test", "e2e", "provideractions", "generated-app", "run.mjs")
	cmd := exec.CommandContext(ctxWithTimeout(t, 2*time.Minute), "node", script)
	cmd.Env = append(os.Environ(),
		"KEDGE_HUB_URL="+hub,
		"KEDGE_PROJECT="+project,
		"KEDGE_ACTION_ALIAS="+alias,
		"KEDGE_ACTION="+action,
		"KEDGE_CALLER_TOKEN="+token,
		"KEDGE_ACTION_INPUT_JSON="+string(b),
	)
	if len(tenantHeaders) > 0 && len(tenantHeaders[0]) > 0 {
		headers, marshalErr := json.Marshal(tenantHeaders[0])
		if marshalErr != nil {
			t.Fatalf("encode generated app tenant headers: %v", marshalErr)
		}
		cmd.Env = append(cmd.Env, "KEDGE_ACTION_HEADERS_JSON="+string(headers))
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
}

// setupTenantWorkspace creates a real child workspace for the static-token
// caller and waits for its kcp logical-cluster ID. App Studio deliberately
// rejects org-only context for project routes; the generated app therefore
// receives only the ordinary tenant-selection headers alongside its caller
// token, never a provider URL or PAT.
func setupTenantWorkspace(t *testing.T) (string, string, string) {
	t.Helper()
	var orgUUID string
	waitCondition(t, 90*time.Second, func() (bool, string) {
		req, err := http.NewRequest(http.MethodGet, hubURL+"/api/orgs", nil)
		if err != nil {
			return false, err.Error()
		}
		req.Header.Set("Authorization", "Bearer "+staticToken)
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			return false, err.Error()
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			return false, fmt.Sprintf("status=%d body=%s", resp.StatusCode, body)
		}
		var out struct {
			Items []struct {
				UUID     string `json:"uuid"`
				Personal bool   `json:"personal"`
			} `json:"items"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			return false, err.Error()
		}
		for _, item := range out.Items {
			if item.Personal && item.UUID != "" {
				orgUUID = item.UUID
				return true, "personal org found"
			}
		}
		return false, "personal org absent"
	}, "personal organization")

	status, body := doJSON(t, http.MethodPost, hubURL+"/api/orgs/"+orgUUID+"/workspaces", staticToken,
		map[string]any{"displayName": "provider-actions-e2e"}, map[string]string{"X-Kedge-Org": orgUUID})
	if status != http.StatusCreated {
		t.Fatalf("create provider-actions workspace: status=%d body=%s", status, body)
	}
	var workspace struct {
		UUID string `json:"uuid"`
	}
	if err := json.Unmarshal([]byte(body), &workspace); err != nil || workspace.UUID == "" {
		t.Fatalf("decode provider-actions workspace: err=%v body=%s", err, body)
	}

	var clusterName string
	headers := map[string]string{"X-Kedge-Org": orgUUID, "X-Kedge-Workspace": workspace.UUID}
	waitCondition(t, 90*time.Second, func() (bool, string) {
		status, body := doJSON(t, http.MethodGet, hubURL+"/api/orgs/"+orgUUID+"/workspaces/"+workspace.UUID, staticToken, nil, headers)
		if status != http.StatusOK {
			return false, fmt.Sprintf("status=%d body=%s", status, body)
		}
		var out struct {
			ClusterName string `json:"clusterName"`
		}
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			return false, err.Error()
		}
		clusterName = strings.TrimSpace(out.ClusterName)
		return clusterName != "", "clusterName=" + clusterName
	}, "provider-actions workspace cluster")
	return orgUUID, workspace.UUID, clusterName
}

// waitTenantProxyContext confirms that the selected workspace is both
// authorized by the hub resolver and initialized in App Studio. The 503
// response is expected while the tenant APIBinding settles; 404 for a
// deliberately missing project is the first interaction-safe readiness
// signal for this provider route.
func waitTenantProxyContext(t *testing.T, orgUUID, workspaceUUID string) {
	t.Helper()
	headers := map[string]string{"X-Kedge-Org": orgUUID, "X-Kedge-Workspace": workspaceUUID}
	waitCondition(t, 3*time.Minute, func() (bool, string) {
		status, body := doJSON(t, http.MethodGet, hubURL+"/services/providers/app-studio/api/projects/__tenant_probe__", staticToken, nil, headers)
		if status == http.StatusNotFound {
			return true, "App Studio workspace context ready"
		}
		return false, fmt.Sprintf("status=%d body=%s", status, body)
	}, "provider-actions workspace proxy context")
}

func loginStaticTokenAndGetCluster(t *testing.T) string {
	t.Helper()
	var body []byte
	waitCondition(t, 90*time.Second, func() (bool, string) {
		req, err := http.NewRequest(http.MethodPost, hubURL+"/auth/token-login", nil)
		if err != nil {
			return false, err.Error()
		}
		req.Header.Set("Authorization", "Bearer "+staticToken)
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			return false, err.Error()
		}
		defer resp.Body.Close()
		body, _ = io.ReadAll(resp.Body)
		return resp.StatusCode == http.StatusOK, fmt.Sprintf("status=%d body=%s", resp.StatusCode, body)
	}, "static token login")
	var out struct {
		Kubeconfig string `json:"kubeconfig"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode token-login: %v", err)
	}
	kubeconfig, err := base64.StdEncoding.DecodeString(out.Kubeconfig)
	if err != nil {
		t.Fatalf("decode token-login kubeconfig: %v", err)
	}
	for _, line := range strings.Split(string(kubeconfig), "\n") {
		if index := strings.Index(line, "/clusters/"); index >= 0 {
			cluster := strings.TrimSpace(line[index+len("/clusters/"):])
			cluster = strings.Trim(cluster, " /")
			return cluster
		}
	}
	t.Fatalf("token-login kubeconfig has no cluster URL: %s", kubeconfig)
	return ""
}

func postJSON(t *testing.T, url, token string, payload any, extraHeaders ...map[string]string) (int, string) {
	return doJSON(t, http.MethodPost, url, token, payload, extraHeaders...)
}

func patchJSON(t *testing.T, url, token string, payload any, extraHeaders ...map[string]string) (int, string) {
	return doJSON(t, http.MethodPatch, url, token, payload, extraHeaders...)
}

func doJSON(t *testing.T, method, url, token string, payload any, extraHeaders ...map[string]string) (int, string) {
	t.Helper()
	var requestBody io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("encode %s: %v", method, err)
		}
		requestBody = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, requestBody)
	if err != nil {
		t.Fatalf("build %s: %v", method, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, headers := range extraHeaders {
		for key, value := range headers {
			req.Header.Set(key, value)
		}
	}
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	responseBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(responseBody)
}
