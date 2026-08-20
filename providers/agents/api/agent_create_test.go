// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	agentsv1alpha1 "github.com/faroshq/provider-agents/apis/v1alpha1"
	"github.com/faroshq/provider-agents/llm"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMCPCreateDefaultsAndBounds(t *testing.T) {
	if got, err := normalizeAgentAutonomy(""); err != nil || got != agentsv1alpha1.AutonomyAsk {
		t.Fatalf("empty autonomy = %q, %v; want ask", got, err)
	}
	for _, tc := range []struct {
		name    string
		turns   int32
		timeout int32
	}{
		{name: "zero", turns: 0, timeout: 0},
		{name: "maximum", turns: AgentCreateMaxToolTurns, timeout: AgentCreateMaxTimeoutSeconds},
	} {
		req := &createAgentRequest{MaxToolTurns: &tc.turns, TimeoutSeconds: &tc.timeout}
		if err := validateAgentCreateLimits(req); err != nil {
			t.Errorf("%s limits rejected: %v", tc.name, err)
		}
	}
	tooManyTurns := AgentCreateMaxToolTurns + 1
	if err := validateAgentCreateLimits(&createAgentRequest{MaxToolTurns: &tooManyTurns}); err == nil {
		t.Error("maxToolTurns above the create ceiling was accepted")
	}
	tooLong := AgentCreateMaxTimeoutSeconds + 1
	if err := validateAgentCreateLimits(&createAgentRequest{TimeoutSeconds: &tooLong}); err == nil {
		t.Error("timeoutSeconds above the create ceiling was accepted")
	}
	if AgentCreateMaxBudgetTokens != 100000 || AgentCreateMaxBudgetUSD != 25.0 {
		t.Fatalf("MCP budget ceilings drifted: tokens=%d usd=%v", AgentCreateMaxBudgetTokens, AgentCreateMaxBudgetUSD)
	}
}

func TestMCPCreateRequiresUsableNamedCredential(t *testing.T) {
	if err := validateModelCredentialProfile("missing", llm.Profile{}); err == nil {
		t.Fatal("empty credential profile was accepted")
	}
	if err := validateModelCredentialProfile("named", llm.Profile{Model: "gpt-test"}); err == nil {
		t.Fatal("credential without an API key was accepted")
	}
	if err := validateModelCredentialProfile("named", llm.Profile{APIKey: "secret"}); err == nil {
		t.Fatal("credential without a model was accepted")
	}
	if err := validateModelCredentialProfile("named", llm.Profile{Model: "gpt-test", APIKey: "secret"}); err != nil {
		t.Fatalf("usable credential rejected: %v", err)
	}
}

func TestMCPCreateRecordsBoundedProvenanceAndOnlySafeOutput(t *testing.T) {
	annotations := agentCreateAnnotations(agentCreateOptions{
		createdVia: "mcp",
		provenance: map[string]string{
			"X-Faros-User":      "alice@example.com",
			"X-Faros-Org":       "org-1",
			"X-Faros-Workspace": "workspace-1",
			"source":            "app-studio",
			"projectName":       "demo",
			"projectUID":        "project-uid",
			"runID":             "run-1",
			"toolCallID":        "call-1",
			"Authorization":     "Bearer should-not-be-recorded",
		},
	})
	if annotations[AgentCreatedViaAnnotation] != "mcp" || annotations[AgentProvenanceUserAnnotation] != "alice@example.com" ||
		annotations[AgentProvenanceOrgAnnotation] != "org-1" || annotations[AgentProvenanceWorkspaceAnnotation] != "workspace-1" {
		t.Fatalf("provenance annotations = %#v", annotations)
	}
	if annotations[AgentProvenanceSourceAnnotation] != "app-studio" || annotations[AgentProvenanceProjectNameAnnotation] != "demo" ||
		annotations[AgentProvenanceProjectUIDAnnotation] != "project-uid" || annotations[AgentProvenanceRunIDAnnotation] != "run-1" ||
		annotations[AgentProvenanceToolCallIDAnnotation] != "call-1" {
		t.Fatalf("App Studio provenance annotations = %#v", annotations)
	}
	if len(annotations) != 9 {
		t.Fatalf("unexpected annotations (possible secret/header leakage): %#v", annotations)
	}

	agent := &agentsv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{
			Name: "safe-agent",
			Annotations: map[string]string{
				AgentCreatedViaAnnotation: "mcp",
				"agents.faros.sh/secret":  "do-not-return",
			},
		},
		Spec: agentsv1alpha1.AgentSpec{Models: map[string]string{"chat": "named-credential"}},
	}
	encoded, err := json.Marshal(settingsView(agent))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "do-not-return") || strings.Contains(string(encoded), "secret") {
		t.Fatalf("settings output leaked an annotation secret: %s", encoded)
	}
	if !strings.Contains(string(encoded), AgentCreatedViaAnnotation) {
		t.Fatalf("settings output omitted server provenance: %s", encoded)
	}
}

func TestMCPCreateSchemaHasNoBroadMutationFields(t *testing.T) {
	typ := reflect.TypeOf(createAgentInput{})
	forbidden := []string{
		"Channels", "Delegates", "ModelFallbacks", "InteractiveFamilies", "BackgroundFamilies",
		"InteractiveConnections", "BackgroundConnections", "InteractiveToolsets", "BackgroundToolsets",
	}
	for _, field := range forbidden {
		if _, ok := typ.FieldByName(field); ok {
			t.Errorf("create_agent input exposes unsafe field %q", field)
		}
	}
}
