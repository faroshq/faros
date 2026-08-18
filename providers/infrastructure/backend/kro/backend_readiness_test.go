// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package kro

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clientgotesting "k8s.io/client-go/testing"

	infrav1alpha1 "github.com/faroshq/provider-infrastructure/apis/v1alpha1"
)

func TestSetupTemplateRequiresCurrentAggregateReady(t *testing.T) {
	tests := []struct {
		name        string
		conditions  []rgdReadinessCondition
		wantReady   bool
		wantMessage string
	}{
		{
			name: "accepted and ready current generation",
			conditions: []rgdReadinessCondition{
				{conditionType: "GraphAccepted", status: "True", observedGeneration: 2, reason: "Valid"},
				{conditionType: "Ready", status: "True", observedGeneration: 2, reason: "GraphReady"},
			},
			wantReady: true, wantMessage: "GraphReady",
		},
		{
			name: "graph accepted is only intermediate",
			conditions: []rgdReadinessCondition{
				{conditionType: "GraphAccepted", status: "True", observedGeneration: 2, reason: "Valid"},
			},
			wantMessage: "to become Ready",
		},
		{
			name: "aggregate ready is stale",
			conditions: []rgdReadinessCondition{
				{conditionType: "GraphAccepted", status: "True", observedGeneration: 2, reason: "Valid"},
				{conditionType: "Ready", status: "True", observedGeneration: 1, reason: "OldReady"},
			},
			wantMessage: "to become Ready",
		},
		{
			name: "accepted stale generation",
			conditions: []rgdReadinessCondition{
				{conditionType: "GraphAccepted", status: "True", observedGeneration: 1, reason: "OldValid"},
				{conditionType: "Ready", status: "True", observedGeneration: 2, reason: "GraphReady"},
			},
			wantMessage: "waiting for kro to accept",
		},
		{
			name: "rejected current generation",
			conditions: []rgdReadinessCondition{
				{conditionType: "GraphAccepted", status: "False", observedGeneration: 2, reason: "InvalidGraph"},
			},
			wantMessage: "InvalidGraph",
		},
		{
			name: "aggregate not ready",
			conditions: []rgdReadinessCondition{
				{conditionType: "GraphAccepted", status: "True", observedGeneration: 2, reason: "Valid"},
				{conditionType: "Ready", status: "False", observedGeneration: 2, reason: "ControllerInstallPending"},
			},
			wantMessage: "ControllerInstallPending",
		},
		{name: "conditions pending", wantMessage: "waiting for kro to accept"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := backendWithRGDStatus(t, 2, tt.conditions)
			status, err := backend.SetupTemplate(context.Background(), readinessTestTemplate())
			if err != nil {
				t.Fatalf("SetupTemplate: %v", err)
			}
			if status.Ready != tt.wantReady {
				t.Fatalf("Ready = %v, want %v (message=%q)", status.Ready, tt.wantReady, status.Message)
			}
			if !strings.Contains(status.Message, tt.wantMessage) {
				t.Fatalf("message = %q, want substring %q", status.Message, tt.wantMessage)
			}
		})
	}
}

type rgdReadinessCondition struct {
	conditionType      string
	status             string
	observedGeneration int64
	reason             string
}

func backendWithRGDStatus(t *testing.T, generation int64, conditions []rgdReadinessCondition) *Backend {
	t.Helper()
	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(rgdGVR.GroupVersion().WithKind(rgdKind+"List"), &unstructured.UnstructuredList{})
	dyn := dynamicfake.NewSimpleDynamicClient(scheme)
	dyn.PrependReactor("create", rgdGVR.Resource, func(action clientgotesting.Action) (bool, runtime.Object, error) {
		obj := action.(clientgotesting.CreateAction).GetObject().(*unstructured.Unstructured).DeepCopy()
		obj.SetGeneration(generation)
		if len(conditions) > 0 {
			rawConditions := make([]any, 0, len(conditions))
			for _, condition := range conditions {
				rawConditions = append(rawConditions, map[string]any{
					"type":               condition.conditionType,
					"status":             condition.status,
					"observedGeneration": condition.observedGeneration,
					"reason":             condition.reason,
					"message":            "test graph verdict",
				})
			}
			if err := unstructured.SetNestedSlice(obj.Object, rawConditions, "status", "conditions"); err != nil {
				t.Fatalf("set conditions: %v", err)
			}
		}
		return true, obj, nil
	})
	return &Backend{runtime: dyn, tokens: testTokens()}
}

func readinessTestTemplate() *infrav1alpha1.Template {
	return &infrav1alpha1.Template{
		ObjectMeta: metav1.ObjectMeta{Name: "readiness"},
		Spec: infrav1alpha1.TemplateSpec{
			Version: "0.1.0",
			Backend: Name,
			InstanceCRD: infrav1alpha1.TemplateInstanceCRD{
				Group: "infrastructure.faros.sh", Version: "v1alpha1", Resource: "readinesses", Kind: "Readiness",
			},
			Schema:        &runtime.RawExtension{Raw: []byte(`{"type":"object","properties":{"name":{"type":"string"}}}`)},
			BackendConfig: &runtime.RawExtension{Raw: []byte(`{"resources":[{"id":"configmap","template":{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"${schema.metadata.name}"}}}]}`)},
		},
	}
}
