/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/mux"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
	"sigs.k8s.io/yaml"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	asclient "github.com/faroshq/provider-app-studio/client"
	"github.com/faroshq/provider-app-studio/tenant"
)

var testDatabricksTableGVR = schema.GroupVersionResource{
	Group: "databricks.kedge.faros.sh", Version: "v1alpha1", Resource: "tables",
}

func TestProviderReferenceReconcileOnlyGetsAndNeverOwnsTarget(t *testing.T) {
	project := &aiv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", UID: "project-uid"},
		Spec: aiv1alpha1.ProjectSpec{
			DisplayName: "Demo",
			Environments: []aiv1alpha1.ProjectEnvironmentSpec{{
				Name: "development", Mode: aiv1alpha1.ProjectEnvironmentModeLive,
				Bindings: []aiv1alpha1.ProjectProviderBindingSpec{{
					Name: "sales", Provider: projectIntegrationProviderDatabricks,
					Kind: aiv1alpha1.ProjectBindingKindProviderReference,
					ResourceRef: &aiv1alpha1.ProjectProviderResourceReference{
						Name: "orders", APIVersion: databricksTableAPIVersion,
						Kind: databricksTableKind, Resource: databricksTableResource,
					},
				}},
			}},
		},
	}
	table := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": databricksTableAPIVersion,
		"kind":       databricksTableKind,
		"metadata":   map[string]any{"name": "orders"},
		"spec":       map[string]any{"catalog": "sales", "schema": "gold", "table": "orders"},
	}}
	scheme := runtime.NewScheme()
	if err := aiv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add App Studio scheme: %v", err)
	}
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		asclient.ProjectGVR: "ProjectList", testDatabricksTableGVR: "TableList",
	}, project, table)
	for _, verb := range []string{"create", "update", "delete", "patch"} {
		verb := verb
		dyn.PrependReactor(verb, "tables", func(k8stesting.Action) (bool, runtime.Object, error) {
			t.Fatalf("provider reference reconcile attempted %s on the referenced Table", verb)
			return true, nil, nil
		})
	}
	c := asclient.NewFromDynamic(dyn)
	if _, err := (&Server{}).reconcileProjectLiveBindings(context.Background(), c, project, identity{}); err != nil {
		t.Fatalf("reconcileProjectLiveBindings: %v", err)
	}
	got, err := c.Resource(providerBindingResource(testDatabricksTableGVR, databricksTableKind), "").Get(context.Background(), "orders", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get referenced Table: %v", err)
	}
	if got.GetOwnerReferences() != nil {
		t.Fatalf("referenced Table gained owner references: %+v", got.GetOwnerReferences())
	}
}

func TestProviderReferenceProjectCleanupDoesNotDeleteTarget(t *testing.T) {
	project := &aiv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "demo"},
		Spec: aiv1alpha1.ProjectSpec{
			Environments: []aiv1alpha1.ProjectEnvironmentSpec{
				{
					Name: "development", Mode: aiv1alpha1.ProjectEnvironmentModeLive,
					Bindings: []aiv1alpha1.ProjectProviderBindingSpec{
						{
							Name: "sales", Provider: projectIntegrationProviderDatabricks,
							Kind: aiv1alpha1.ProjectBindingKindProviderReference,
							ResourceRef: &aiv1alpha1.ProjectProviderResourceReference{
								Name: "orders", APIVersion: databricksTableAPIVersion,
								Kind: databricksTableKind, Resource: databricksTableResource,
							},
						},
					},
				},
			},
		},
	}
	table := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": databricksTableAPIVersion, "kind": databricksTableKind,
		"metadata": map[string]any{"name": "orders"},
	}}
	scheme := runtime.NewScheme()
	if err := aiv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add App Studio scheme: %v", err)
	}
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		asclient.ProjectGVR: "ProjectList", testDatabricksTableGVR: "TableList",
	}, project, table)
	dyn.PrependReactor("delete", "tables", func(k8stesting.Action) (bool, runtime.Object, error) {
		t.Fatalf("project cleanup deleted provider-owned Table")
		return true, nil, nil
	})
	if err := (&Server{}).deleteProjectProviderResources(context.Background(), asclient.NewFromDynamic(dyn), project, identity{}); err != nil {
		t.Fatalf("deleteProjectProviderResources: %v", err)
	}
}

func TestIntegrationQueryArgumentsRejectProviderControlledFields(t *testing.T) {
	table := &unstructured.Unstructured{Object: map[string]any{
		"status": map[string]any{"columns": []any{map[string]any{"name": "id"}}},
	}}
	if _, err := projectIntegrationQueryArguments(json.RawMessage(`{"tableRef":"other"}`), table); err == nil {
		t.Fatal("query input accepted caller-controlled tableRef")
	}
	if _, err := projectIntegrationQueryArguments(json.RawMessage(`{"sql":"select 1"}`), table); err == nil {
		t.Fatal("query input accepted raw SQL")
	}
	if _, err := projectIntegrationQueryArguments(json.RawMessage(`{"columns":["secret"]}`), table); err == nil {
		t.Fatal("query input accepted a column absent from bound Table metadata")
	}
	args, err := projectIntegrationQueryArguments(json.RawMessage(`{"columns":["id"],"limit":25}`), table)
	if err != nil {
		t.Fatalf("valid query input rejected: %v", err)
	}
	if args["limit"] != 25 {
		t.Fatalf("limit argument = %#v, want 25", args["limit"])
	}
}

func TestIntegrationActionNormalizationAndRevocation(t *testing.T) {
	name, version, err := normalizeIntegrationAction("query_table/v1", "")
	if err != nil || name != "query_table" || version != "v1" {
		t.Fatalf("normalize action = %q/%q/%v", name, version, err)
	}
	if _, _, err := normalizeIntegrationAction("query_table/v1", "v2"); err == nil {
		t.Fatal("mismatched explicit action version was accepted")
	}
	actions, err := normalizeProjectIntegrationActions([]aiv1alpha1.ProjectProviderActionSpec{{Name: "query_table", Version: "v1", Revoked: true}})
	if err != nil || len(actions) != 1 || !actions[0].Revoked {
		t.Fatalf("normalized revoked action = %#v, err %v", actions, err)
	}
}

// integrationHTTPFixture backs the provider's GraphQL client and MCP
// federation endpoint without involving a real hub. It intentionally keeps
// the project and Table as serialized tenant resources: this exercises the
// same GraphQL-backed client path used by the HTTP handlers.
type integrationHTTPFixture struct {
	mu sync.Mutex

	projectYAML string
	tableYAML   string

	graphql *httptest.Server
	mcp     *httptest.Server
	mcpReqs []integrationMCPRequest
}

type integrationMCPRequest struct {
	URL     string
	Headers http.Header
	Body    map[string]any
}

func newIntegrationHTTPFixture(t *testing.T, project *aiv1alpha1.Project) *integrationHTTPFixture {
	t.Helper()
	projectObject, err := runtime.DefaultUnstructuredConverter.ToUnstructured(project)
	if err != nil {
		t.Fatalf("convert project: %v", err)
	}
	projectYAML, err := yaml.Marshal(projectObject)
	if err != nil {
		t.Fatalf("marshal project: %v", err)
	}
	table := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": databricksTableAPIVersion,
		"kind":       databricksTableKind,
		"metadata":   map[string]any{"name": "orders"},
		"spec": map[string]any{
			"catalog": "sales", "schema": "gold", "table": "orders",
		},
		"status": map[string]any{
			"columns": []any{
				map[string]any{"name": "id"},
				map[string]any{"name": "total"},
			},
			"conditions": []any{map[string]any{"type": "Ready", "status": "True"}},
		},
	}}
	tableYAML, err := yaml.Marshal(table.Object)
	if err != nil {
		t.Fatalf("marshal table: %v", err)
	}
	f := &integrationHTTPFixture{projectYAML: string(projectYAML), tableYAML: string(tableYAML)}
	f.graphql = httptest.NewServer(http.HandlerFunc(f.serveGraphQL))
	f.mcp = httptest.NewServer(http.HandlerFunc(f.serveMCP))
	t.Cleanup(func() {
		f.graphql.Close()
		f.mcp.Close()
	})
	return f
}

func (f *integrationHTTPFixture) serveGraphQL(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Query     string                     `json:"query"`
		Variables map[string]json.RawMessage `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid graphql request", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch {
	case strings.Contains(request.Query, "ProjectYaml"):
		writeIntegrationGraphQLData(w, map[string]any{
			"ai_kedge_faros_sh": map[string]any{
				"v1alpha1": map[string]any{"ProjectYaml": f.projectYAML},
			},
		})
	case strings.Contains(request.Query, "TableYaml"):
		writeIntegrationGraphQLData(w, map[string]any{
			"databricks_kedge_faros_sh": map[string]any{
				"v1alpha1": map[string]any{"TableYaml": f.tableYAML},
			},
		})
	case strings.Contains(request.Query, "applyStatusYaml"):
		writeIntegrationGraphQLData(w, map[string]any{"applyStatusYaml": f.projectYAML})
	case strings.Contains(request.Query, "applyYaml"):
		raw, ok := request.Variables["yaml"]
		if !ok {
			http.Error(w, "applyYaml missing yaml variable", http.StatusBadRequest)
			return
		}
		var applied string
		if err := json.Unmarshal(raw, &applied); err != nil {
			http.Error(w, "applyYaml yaml variable is not a string", http.StatusBadRequest)
			return
		}
		var object map[string]any
		if err := yaml.Unmarshal([]byte(applied), &object); err != nil {
			http.Error(w, "applyYaml payload is not YAML", http.StatusBadRequest)
			return
		}
		if object["kind"] == "Project" {
			f.projectYAML = applied
		}
		writeIntegrationGraphQLData(w, map[string]any{"applyYaml": applied})
	default:
		http.Error(w, fmt.Sprintf("unexpected GraphQL query: %s", request.Query), http.StatusInternalServerError)
	}
}

func writeIntegrationGraphQLData(w http.ResponseWriter, data map[string]any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}

func (f *integrationHTTPFixture) serveMCP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid MCP request", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.mcpReqs = append(f.mcpReqs, integrationMCPRequest{
		URL: r.URL.String(), Headers: r.Header.Clone(), Body: body,
	})
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"{\"actionVersion\":\"v1\",\"tableRef\":\"orders\",\"rows\":[{\"id\":1}]}"}]}}`))
}

func (f *integrationHTTPFixture) setProject(t *testing.T, project *aiv1alpha1.Project) {
	t.Helper()
	object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(project)
	if err != nil {
		t.Fatalf("convert project: %v", err)
	}
	raw, err := yaml.Marshal(object)
	if err != nil {
		t.Fatalf("marshal project: %v", err)
	}
	f.mu.Lock()
	f.projectYAML = string(raw)
	f.mcpReqs = nil
	f.mu.Unlock()
}

func (f *integrationHTTPFixture) project(t *testing.T) *aiv1alpha1.Project {
	t.Helper()
	f.mu.Lock()
	raw := []byte(f.projectYAML)
	f.mu.Unlock()
	var object map[string]any
	if err := yaml.Unmarshal(raw, &object); err != nil {
		t.Fatalf("decode project YAML: %v", err)
	}
	project := &aiv1alpha1.Project{}
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(object, project); err != nil {
		t.Fatalf("convert project YAML: %v", err)
	}
	return project
}

func (f *integrationHTTPFixture) table(t *testing.T) *unstructured.Unstructured {
	t.Helper()
	f.mu.Lock()
	raw := []byte(f.tableYAML)
	f.mu.Unlock()
	var object map[string]any
	if err := yaml.Unmarshal(raw, &object); err != nil {
		t.Fatalf("decode Table YAML: %v", err)
	}
	return &unstructured.Unstructured{Object: object}
}

func (f *integrationHTTPFixture) mcpRequests() []integrationMCPRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	requests := make([]integrationMCPRequest, len(f.mcpReqs))
	copy(requests, f.mcpReqs)
	return requests
}

func projectWithTableIntegration(revoked bool) *aiv1alpha1.Project {
	return &aiv1alpha1.Project{
		TypeMeta:   metav1.TypeMeta{APIVersion: aiv1alpha1.SchemeGroupVersion.String(), Kind: "Project"},
		ObjectMeta: metav1.ObjectMeta{Name: "demo", UID: "project-uid"},
		Spec: aiv1alpha1.ProjectSpec{
			DisplayName: "Demo",
			Environments: []aiv1alpha1.ProjectEnvironmentSpec{{
				Name: "development", Mode: aiv1alpha1.ProjectEnvironmentModeLive,
				Bindings: []aiv1alpha1.ProjectProviderBindingSpec{{
					Name: "sales", Provider: projectIntegrationProviderDatabricks,
					Kind: aiv1alpha1.ProjectBindingKindProviderReference,
					ResourceRef: &aiv1alpha1.ProjectProviderResourceReference{
						Name: "orders", APIVersion: databricksTableAPIVersion,
						Kind: databricksTableKind, Resource: databricksTableResource,
					},
					AllowedActions: []aiv1alpha1.ProjectProviderActionSpec{{Name: projectIntegrationActionQueryTable, Version: projectIntegrationActionVersionV1, Revoked: revoked}},
				}},
			}},
		},
	}
}

func integrationHTTPTestRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer caller-token")
	request.Header.Set("X-Kedge-User", "alice@example.com")
	request.Header.Set("X-Kedge-Tenant", "root:kedge:tenants:org-a:workspace-a")
	request.Header.Set("X-Kedge-Org", "org-a")
	request.Header.Set("X-Kedge-Workspace", "workspace-a")
	request.Header.Set("X-Kedge-Cluster", "cluster-a")
	return request
}

func TestProjectIntegrationCRUDInvokeAndForwardingContract(t *testing.T) {
	fixture := newIntegrationHTTPFixture(t, &aiv1alpha1.Project{
		TypeMeta:   metav1.TypeMeta{APIVersion: aiv1alpha1.SchemeGroupVersion.String(), Kind: "Project"},
		ObjectMeta: metav1.ObjectMeta{Name: "demo", UID: "project-uid"},
		Spec:       aiv1alpha1.ProjectSpec{DisplayName: "Demo"},
	})
	server := NewWithWorkspace(tenant.NewGraphQLClient(fixture.graphql.URL, false), nil, nil, fixture.mcp.URL, false)
	router := mux.NewRouter()
	server.Register(router)

	add := integrationHTTPTestRequest(http.MethodPost, "/api/projects/demo/integrations", `{"alias":"sales","provider":"databricks","resourceRef":{"name":"orders","apiVersion":"databricks.kedge.faros.sh/v1alpha1","kind":"Table","resource":"tables"},"allowedActions":[{"name":"query_table","version":"v1"}]}`)
	addResponse := httptest.NewRecorder()
	router.ServeHTTP(addResponse, add)
	if addResponse.Code != http.StatusCreated {
		t.Fatalf("add integration status = %d: %s", addResponse.Code, addResponse.Body.String())
	}
	created := fixture.project(t)
	binding := created.Spec.Environments[0].Bindings[0]
	if binding.Kind != aiv1alpha1.ProjectBindingKindProviderReference || binding.ResourceRef == nil || binding.ResourceRef.Name != "orders" {
		t.Fatalf("created binding = %#v, want non-owning reference to orders", binding)
	}

	listResponse := httptest.NewRecorder()
	router.ServeHTTP(listResponse, integrationHTTPTestRequest(http.MethodGet, "/api/projects/demo/integrations", ""))
	if listResponse.Code != http.StatusOK || !strings.Contains(listResponse.Body.String(), `"alias":"sales"`) {
		t.Fatalf("list integration response = %d %s", listResponse.Code, listResponse.Body.String())
	}

	patchResponse := httptest.NewRecorder()
	router.ServeHTTP(patchResponse, integrationHTTPTestRequest(http.MethodPatch, "/api/projects/demo/integrations/sales", `{"allowedActions":[{"name":"query_table","version":"v1"}]}`))
	if patchResponse.Code != http.StatusOK {
		t.Fatalf("patch integration status = %d: %s", patchResponse.Code, patchResponse.Body.String())
	}

	invokeResponse := httptest.NewRecorder()
	router.ServeHTTP(invokeResponse, integrationHTTPTestRequest(http.MethodPost, "/api/projects/demo/integrations/sales/invoke", `{"action":"query_table/v1","input":{"columns":["id"],"limit":2}}`))
	if invokeResponse.Code != http.StatusOK {
		t.Fatalf("invoke status = %d: %s", invokeResponse.Code, invokeResponse.Body.String())
	}
	var invokeBody projectIntegrationInvokeResult
	if err := json.Unmarshal(invokeResponse.Body.Bytes(), &invokeBody); err != nil {
		t.Fatalf("decode invoke response: %v", err)
	}
	if invokeBody.Action != "query_table" || invokeBody.ActionVersion != "v1" || invokeBody.Environment != "development" {
		t.Fatalf("invoke response = %#v, want query_table/v1 development", invokeBody)
	}
	requests := fixture.mcpRequests()
	if len(requests) != 1 {
		t.Fatalf("MCP calls = %d, want exactly one aggregate call", len(requests))
	}
	mcpRequest := requests[0]
	if !strings.HasPrefix(mcpRequest.URL, "/services/mcpserver/cluster-a/apis/kedge.faros.sh/v1alpha1/mcpservers/default/mcp") {
		t.Fatalf("MCP URL = %q, want hub aggregate endpoint", mcpRequest.URL)
	}
	if mcpRequest.Headers.Get("Authorization") != "Bearer caller-token" ||
		mcpRequest.Headers.Get("X-Kedge-Tenant") != "root:kedge:tenants:org-a:workspace-a" ||
		mcpRequest.Headers.Get("X-Kedge-User") != "alice@example.com" {
		t.Fatalf("MCP caller headers = %#v, want original authorization and tenant identity", mcpRequest.Headers)
	}
	params, ok := mcpRequest.Body["params"].(map[string]any)
	if !ok || params["name"] != "databricks__query_table" {
		t.Fatalf("MCP params = %#v, want aggregate databricks__query_table", mcpRequest.Body["params"])
	}
	arguments, ok := params["arguments"].(map[string]any)
	if !ok || arguments["tableRef"] != "orders" || arguments["actionVersion"] != "v1" || arguments["limit"] != float64(2) {
		t.Fatalf("MCP arguments = %#v, want server-injected orders/v1 and bounded input", params["arguments"])
	}
	if _, exposed := arguments["sql"]; exposed {
		t.Fatal("MCP arguments exposed raw SQL")
	}

	deleteResponse := httptest.NewRecorder()
	router.ServeHTTP(deleteResponse, integrationHTTPTestRequest(http.MethodDelete, "/api/projects/demo/integrations/sales", ""))
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("delete integration status = %d: %s", deleteResponse.Code, deleteResponse.Body.String())
	}
	removed := fixture.project(t)
	if len(removed.Spec.Environments) != 1 || len(removed.Spec.Environments[0].Bindings) != 0 || len(fixture.mcpRequests()) != 1 {
		t.Fatalf("after deletion project/invocation state = %#v/%d, want empty development bindings and no extra MCP call", removed.Spec.Environments, len(fixture.mcpRequests()))
	}
	if got := fixture.table(t).GetName(); got != "orders" {
		t.Fatalf("referenced Table after binding removal = %q, want orders", got)
	}
}

func TestProjectIntegrationInvokeRejectsBeforeMCP(t *testing.T) {
	fixture := newIntegrationHTTPFixture(t, projectWithTableIntegration(false))
	server := NewWithWorkspace(tenant.NewGraphQLClient(fixture.graphql.URL, false), nil, nil, fixture.mcp.URL, false)
	router := mux.NewRouter()
	server.Register(router)

	tests := []struct {
		name       string
		alias      string
		project    *aiv1alpha1.Project
		body       string
		wantStatus int
	}{
		{name: "unbound", alias: "missing", project: projectWithTableIntegration(false), body: `{"action":"query_table/v1","input":{}}`, wantStatus: http.StatusNotFound},
		{name: "ambiguous", alias: "sales", project: integrationAmbiguousProject(), body: `{"action":"query_table/v1","input":{}}`, wantStatus: http.StatusBadRequest},
		{name: "provider", alias: "sales", project: integrationProjectWithProvider("other"), body: `{"action":"query_table/v1","input":{}}`, wantStatus: http.StatusBadRequest},
		{name: "unknown-action", alias: "sales", project: projectWithTableIntegration(false), body: `{"action":"drop_table/v1","input":{}}`, wantStatus: http.StatusForbidden},
		{name: "mismatched-version", alias: "sales", project: projectWithTableIntegration(false), body: `{"action":"query_table/v1","actionVersion":"v2","input":{}}`, wantStatus: http.StatusBadRequest},
		{name: "revoked", alias: "sales", project: projectWithTableIntegration(true), body: `{"action":"query_table/v1","input":{}}`, wantStatus: http.StatusForbidden},
		{name: "unknown-field", alias: "sales", project: projectWithTableIntegration(false), body: `{"action":"query_table/v1","input":{"unknown":true}}`, wantStatus: http.StatusBadRequest},
		{name: "raw-sql", alias: "sales", project: projectWithTableIntegration(false), body: `{"action":"query_table/v1","input":{"sql":"select 1"}}`, wantStatus: http.StatusBadRequest},
		{name: "credentials", alias: "sales", project: projectWithTableIntegration(false), body: `{"action":"query_table/v1","input":{"credentials":"secret"}}`, wantStatus: http.StatusBadRequest},
		{name: "table-ref", alias: "sales", project: projectWithTableIntegration(false), body: `{"action":"query_table/v1","input":{"tableRef":"other"}}`, wantStatus: http.StatusBadRequest},
		{name: "invalid-column", alias: "sales", project: projectWithTableIntegration(false), body: `{"action":"query_table/v1","input":{"columns":["bad-name"]}}`, wantStatus: http.StatusBadRequest},
		{name: "unknown-column", alias: "sales", project: projectWithTableIntegration(false), body: `{"action":"query_table/v1","input":{"columns":["secret"]}}`, wantStatus: http.StatusBadRequest},
		{name: "limit-too-large", alias: "sales", project: projectWithTableIntegration(false), body: `{"action":"query_table/v1","input":{"limit":101}}`, wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture.setProject(t, tt.project)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, integrationHTTPTestRequest(http.MethodPost, "/api/projects/demo/integrations/"+tt.alias+"/invoke", tt.body))
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", response.Code, tt.wantStatus, response.Body.String())
			}
			if got := len(fixture.mcpRequests()); got != 0 {
				t.Fatalf("rejected invocation reached aggregate MCP %d time(s)", got)
			}
		})
	}
}

func integrationProjectWithProvider(provider string) *aiv1alpha1.Project {
	project := projectWithTableIntegration(false)
	project.Spec.Environments[0].Bindings[0].Provider = provider
	return project
}

func integrationAmbiguousProject() *aiv1alpha1.Project {
	project := projectWithTableIntegration(false)
	second := project.Spec.Environments[0]
	second.Name = "production"
	second.Mode = aiv1alpha1.ProjectEnvironmentModeArtifact
	project.Spec.Environments = append(project.Spec.Environments, second)
	return project
}

func TestProviderReferenceSurvivesTemplateSwitchPromotionAndProjectCleanup(t *testing.T) {
	project := projectWithTableIntegration(false)
	project.Spec.Environments[0].Bindings = append(project.Spec.Environments[0].Bindings,
		aiv1alpha1.ProjectProviderBindingSpec{
			Name: projectDevelopmentBindingName, Provider: projectDevelopmentProviderAppStudio,
			Kind: aiv1alpha1.ProjectBindingKindProviderResource,
			ResourceRef: &aiv1alpha1.ProjectProviderResourceReference{
				Name: "demo-dev", APIVersion: "infrastructure.kedge.faros.sh/v1alpha1", Kind: "Application", Resource: "applications",
			},
		})
	scheme := runtime.NewScheme()
	if err := aiv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add App Studio scheme: %v", err)
	}
	applicationGVR := schema.GroupVersionResource{Group: "infrastructure.kedge.faros.sh", Version: "v1alpha1", Resource: "applications"}
	oldApplication := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "infrastructure.kedge.faros.sh/v1alpha1", "kind": "Application",
		"metadata": map[string]any{"name": "demo-dev"},
	}}
	table := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": databricksTableAPIVersion, "kind": databricksTableKind,
		"metadata": map[string]any{"name": "orders"},
	}}
	dyn := fake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		asclient.ProjectGVR: "ProjectList", testDatabricksTableGVR: "TableList", applicationGVR: "ApplicationList",
	}, project, table, oldApplication)
	for _, verb := range []string{"create", "update", "delete", "patch"} {
		verb := verb
		dyn.PrependReactor(verb, "tables", func(k8stesting.Action) (bool, runtime.Object, error) {
			t.Fatalf("providerReference Table was mutated during %s", verb)
			return true, nil, nil
		})
	}
	c := asclient.NewFromDynamic(dyn)
	id := identity{tenantPath: "root:kedge:tenants:org-a:workspace-a", clusterID: "cluster-a"}

	if err := (&Server{}).deleteProjectDevelopmentBindingResources(context.Background(), c, project, id); err != nil {
		t.Fatalf("delete old template binding: %v", err)
	}
	info, err := projectTemplateInfoFromUnstructured(applicationTemplateObject())
	if err != nil {
		t.Fatalf("template info: %v", err)
	}
	if err := applyProjectDevelopmentTemplate(project, info); err != nil {
		t.Fatalf("switch template: %v", err)
	}
	upsertProjectProductionBinding(project, aiv1alpha1.ProjectProviderBindingSpec{
		Name: projectProductionBindingName, Provider: projectDevelopmentProviderAppStudio,
		Kind: aiv1alpha1.ProjectBindingKindProviderResource,
		ResourceRef: &aiv1alpha1.ProjectProviderResourceReference{
			Name: "demo-prod", APIVersion: "infrastructure.kedge.faros.sh/v1alpha1", Kind: "Application", Resource: "applications",
		},
	})
	if _, err := (&Server{}).reconcileProjectLiveBindings(context.Background(), c, project, id); err != nil {
		t.Fatalf("reconcile after template switch/promotion: %v", err)
	}
	if err := (&Server{}).deleteProjectProviderResources(context.Background(), c, project, id); err != nil {
		t.Fatalf("project cleanup: %v", err)
	}
	if _, err := c.Resource(providerBindingResource(testDatabricksTableGVR, databricksTableKind), "").Get(context.Background(), "orders", metav1.GetOptions{}); err != nil {
		t.Fatalf("referenced Table did not survive template switch, promotion, and cleanup: %v", err)
	}
	refFound := false
	for _, env := range project.Spec.Environments {
		for _, binding := range env.Bindings {
			if binding.Name == "sales" && binding.Kind == aiv1alpha1.ProjectBindingKindProviderReference {
				refFound = true
			}
		}
	}
	if !refFound {
		t.Fatal("template switch/promotion removed the providerReference binding")
	}
}
