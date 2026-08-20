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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	"github.com/faroshq/provider-app-studio/store"
)

func TestProjectAssistantAgentsMCPAllowlist(t *testing.T) {
	for _, name := range []string{
		projectToolAgentsGetAgent,
		projectToolAgentsListModelCredentials,
		projectToolAgentsListToolFamilies,
		projectToolAgentsListToolsets,
		projectToolAgentsListConnections,
		projectToolAgentsCreateAgent,
		projectToolAgentsRunAgent,
		projectToolAgentsGetRun,
		projectToolAgentsListRuns,
		projectToolAgentsListAgents,
	} {
		if !projectMCPToolAllowed(name) {
			t.Fatalf("Agents tool %q should be allowed", name)
		}
	}
	for _, name := range []string{
		"agents__update_agent",
		"agents__delete_agent",
		"agents__save_model_credential",
		"agents__delete_model_credential",
		"agents__test_model_credential",
		"agents__create_connection",
		"agents__update_connection",
		"agents__delete_connection",
		"agents__test_connection",
		"agents__create_schedule",
		"agents__update_schedule",
		"agents__delete_schedule",
		"agents__run_schedule",
		"agents__create_trigger",
		"agents__update_trigger",
		"agents__delete_trigger",
		"agents__run_trigger",
		"agents__create_toolset",
		"agents__update_toolset",
		"agents__delete_toolset",
	} {
		if projectMCPToolAllowed(name) {
			t.Fatalf("Agents mutation %q must remain denied", name)
		}
	}
}

func TestProjectAssistantAgentsCreateSpecIsServerOwned(t *testing.T) {
	spec, ok := projectAssistantMCPToolSpec(projectMCPTool{
		Name:        projectToolAgentsCreateAgent,
		Description: "provider-owned unsafe description",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"channels":{"type":"array"},"autonomy":{"type":"string"}}}`),
	})
	if !ok {
		t.Fatal("create_agent was not discovered")
	}
	if spec.Risk != projectAssistantToolRiskRuntime {
		t.Fatalf("create_agent risk = %q, want runtime", spec.Risk)
	}
	if spec.Description != projectAssistantAgentsCreateDescription {
		t.Fatalf("create_agent description was not server-owned: %q", spec.Description)
	}
	var schema map[string]any
	if err := json.Unmarshal(spec.Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema properties = %#v", schema["properties"])
	}
	for _, forbidden := range []string{"channels", "delegates", "interactiveFamilies", "backgroundFamilies", "interactiveToolsets", "backgroundToolsets", "interactiveConnections", "backgroundConnections", "modelFallbacks", "provenance", "toolGrants"} {
		if _, found := properties[forbidden]; found {
			t.Fatalf("server-owned create schema exposes forbidden field %q", forbidden)
		}
	}
	required, ok := schema["required"].([]any)
	if !ok || len(required) != 2 || required[0] != "name" || required[1] != "modelCredential" {
		t.Fatalf("required = %#v, want name and modelCredential", schema["required"])
	}
}

func TestProjectAssistantAgentsCreateSanitizerBoundsAndOwnsFields(t *testing.T) {
	req := projectAssistantToolCallRequest{
		Project:        &aiv1alpha1.Project{ObjectMeta: metav1.ObjectMeta{Name: "demo", UID: "project-uid"}},
		AssistantRunID: "run-1",
		ToolCallID:     "call-1",
	}
	args := map[string]any{
		"name":                   "research-bot",
		"displayName":            " Research Bot ",
		"description":            " bounded worker ",
		"systemPrompt":           " do careful work ",
		"modelCredential":        "openai-main",
		"autonomy":               "auto",
		"modelFallbacks":         []any{"other"},
		"channels":               []any{"secret-channel"},
		"delegates":              []any{"powerful-agent"},
		"interactiveFamilies":    []any{"web"},
		"interactiveToolsets":    []any{"everything"},
		"interactiveConnections": []any{"github"},
		"budgetTokens":           float64(999999),
		"budgetUSD":              "999999.999",
		"maxToolTurns":           float64(999),
		"timeoutSeconds":         float64(99999),
		"provenance":             map[string]any{"projectName": "forged"},
	}
	got, err := projectAssistantSanitizeAgentsCreateArguments(args, req)
	if err != nil {
		t.Fatal(err)
	}
	if got["name"] != "research-bot" || got["modelCredential"] != "openai-main" || got["autonomy"] != "ask" {
		t.Fatalf("identity fields = %#v", got)
	}
	if got["budgetTokens"] != projectAssistantAgentsCreateMaxBudgetTokens {
		t.Fatalf("budgetTokens = %#v, want max bound", got["budgetTokens"])
	}
	if got["budgetUSD"] != "25.00" {
		t.Fatalf("budgetUSD = %#v, want 25.00", got["budgetUSD"])
	}
	if got["maxToolTurns"] != projectAssistantAgentsCreateMaxMaxToolTurns || got["timeoutSeconds"] != projectAssistantAgentsCreateMaxTimeoutSeconds {
		t.Fatalf("run limits = %#v, want bounded maxima", got)
	}
	for _, forbidden := range []string{"modelFallbacks", "channels", "delegates", "interactiveFamilies", "interactiveToolsets", "interactiveConnections"} {
		if _, found := got[forbidden]; found {
			t.Fatalf("sanitized create args retained forbidden field %q: %#v", forbidden, got)
		}
	}
	provenance, ok := got["provenance"].(map[string]string)
	if !ok {
		t.Fatalf("provenance type = %T, want map[string]string", got["provenance"])
	}
	if provenance["source"] != "app-studio" || provenance["projectName"] != "demo" || provenance["projectUID"] != "project-uid" || provenance["runID"] != "run-1" || provenance["toolCallID"] != "call-1" {
		t.Fatalf("provenance = %#v", provenance)
	}

	defaults, err := projectAssistantSanitizeAgentsCreateArguments(map[string]any{
		"name": "bounded-agent", "modelCredential": "credential",
	}, req)
	if err != nil {
		t.Fatal(err)
	}
	if defaults["budgetTokens"] != projectAssistantAgentsCreateDefaultBudgetTokens || defaults["budgetUSD"] != projectAssistantAgentsCreateDefaultBudgetUSD || defaults["maxToolTurns"] != projectAssistantAgentsCreateDefaultMaxToolTurns || defaults["timeoutSeconds"] != projectAssistantAgentsCreateDefaultTimeoutSeconds {
		t.Fatalf("defaults = %#v", defaults)
	}
	for _, invalid := range []map[string]any{
		{"modelCredential": "credential"},
		{"name": "Bad_Name", "modelCredential": "credential"},
		{"name": "bounded-agent", "modelCredential": ""},
		{"name": "bounded-agent", "modelCredential": "credential", "budgetTokens": -1},
		{"name": "bounded-agent", "modelCredential": "credential", "budgetUSD": "not-money"},
	} {
		if _, err := projectAssistantSanitizeAgentsCreateArguments(invalid, req); err == nil {
			t.Fatalf("invalid args unexpectedly sanitized: %#v", invalid)
		}
	}
}

func TestProjectAssistantAgentsCreateModeAndApproval(t *testing.T) {
	spec, ok := projectAssistantMCPToolSpec(projectMCPTool{Name: projectToolAgentsCreateAgent})
	if !ok {
		t.Fatal("create_agent was not discovered")
	}
	tool := projectAssistantToolFunc{spec: spec}
	for _, mode := range []projectAssistantCollaborationMode{projectAssistantCollaborationModePlan, projectAssistantCollaborationModeReview} {
		if got := projectAssistantToolsForCollaborationMode([]projectAssistantTool{tool}, mode); len(got) != 0 {
			t.Fatalf("create_agent remained available in %s mode", mode)
		}
	}
	for _, mode := range []store.AssistantApprovalMode{store.AssistantApprovalModeOnRequest, store.AssistantApprovalModeAutoApprove, store.AssistantApprovalModeAlwaysAsk} {
		if got := projectAssistantPermissionForV2(spec, mode, nil, nil, false); got != projectAssistantPermissionAsk {
			t.Fatalf("approval mode %s returned %q, want ask", mode, got)
		}
	}
	if got := projectAssistantPermissionForV2(spec, store.AssistantApprovalModeNever, nil, nil, false); got != projectAssistantPermissionDeny {
		t.Fatalf("Never returned %q, want deny", got)
	}
}

func TestProjectAssistantAgentsCreatePromptRequiresCredentialDiscovery(t *testing.T) {
	create := chatTool{Type: "function", Function: chatToolFunction{Name: projectToolAgentsCreateAgent}}
	credentials := chatTool{Type: "function", Function: chatToolFunction{Name: projectToolAgentsListModelCredentials}}
	if got := projectMCPToolsPrompt([]chatTool{create}); got != "" {
		t.Fatalf("create-only prompt should stay silent, got %q", got)
	}
	got := projectMCPToolsPrompt([]chatTool{create, credentials})
	for _, want := range []string{"list_model_credentials", "create_agent", "autonomy to ask", "always requires explicit approval"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt missing %q: %s", want, got)
		}
	}
}

func TestProjectAssistantAgentsCreateForwardsSanitizedArgsAndCallerHeaders(t *testing.T) {
	var received map[string]any
	var receivedHeaders http.Header
	server := newProjectAssistantAgentsTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode forwarded MCP request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"created"}]}}`))
	}))
	defer server.Close()

	tools := projectAssistantMCPToolsForSpecs([]projectMCPTool{{
		Name:        projectToolAgentsCreateAgent,
		Description: "ignored provider description",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"channels":{}}}`),
	}})
	if len(tools) != 1 {
		t.Fatalf("create tools = %d, want one", len(tools))
	}
	request := httptest.NewRequest(http.MethodPost, "https://app-studio.example/assistant", nil)
	request.Header.Set("Authorization", "Bearer caller-token")
	request.Header.Set("X-Faros-User", "alice")
	request.Header.Set("X-Faros-Org", "org-a")
	request.Header.Set("X-Faros-Workspace", "ws-a")
	request.Header.Set("X-Faros-Cluster", "cluster-a")
	request.Header.Set("X-Faros-Tenant", "spoofed")
	result, err := tools[0].Call(context.Background(), projectAssistantToolCallRequest{
		Identity:       identity{tenantPath: "org-a/ws-a"},
		Project:        &aiv1alpha1.Project{ObjectMeta: metav1.ObjectMeta{Name: "demo", UID: "project-uid"}},
		MCPEndpoint:    server.URL,
		HTTPRequest:    request,
		AssistantRunID: "run-1",
		ToolCallID:     "call-1",
		Arguments: map[string]any{
			"name": "safe-agent", "modelCredential": "credential", "autonomy": "auto",
			"channels": []any{"secret"}, "delegates": []any{"other"}, "budgetTokens": float64(999999),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != "created" {
		t.Fatalf("result = %q, want created", result)
	}
	if receivedHeaders.Get("Authorization") != "Bearer caller-token" || receivedHeaders.Get("X-Faros-User") != "alice" || receivedHeaders.Get("X-Faros-Org") != "org-a" || receivedHeaders.Get("X-Faros-Workspace") != "ws-a" || receivedHeaders.Get("X-Faros-Cluster") != "cluster-a" {
		t.Fatalf("caller headers were not preserved: %#v", receivedHeaders)
	}
	if receivedHeaders.Get("X-Faros-Tenant") != "org-a/ws-a" {
		t.Fatalf("resolved tenant header = %q, want org-a/ws-a", receivedHeaders.Get("X-Faros-Tenant"))
	}
	params, ok := received["params"].(map[string]any)
	if !ok {
		t.Fatalf("params = %#v", received["params"])
	}
	arguments, ok := params["arguments"].(map[string]any)
	if !ok {
		t.Fatalf("arguments = %#v", params["arguments"])
	}
	if arguments["autonomy"] != "ask" || arguments["budgetTokens"] != float64(projectAssistantAgentsCreateMaxBudgetTokens) {
		t.Fatalf("forwarded safety fields = %#v", arguments)
	}
	for _, forbidden := range []string{"channels", "delegates", "modelFallbacks", "interactiveFamilies", "provenance"} {
		if _, found := arguments[forbidden]; found && forbidden != "provenance" {
			t.Fatalf("forwarded args retained %q: %#v", forbidden, arguments)
		}
	}
	provenance, ok := arguments["provenance"].(map[string]any)
	if !ok || provenance["projectName"] != "demo" || provenance["runID"] != "run-1" || provenance["toolCallID"] != "call-1" {
		t.Fatalf("forwarded provenance = %#v", arguments["provenance"])
	}
}

func newProjectAssistantAgentsTestServer(t *testing.T, handler http.Handler) (server *httptest.Server) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Skipf("loopback listeners are unavailable in this test environment: %v", recovered)
		}
	}()
	return httptest.NewServer(handler)
}
