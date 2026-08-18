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
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"

	asclient "github.com/faroshq/provider-app-studio/client"
)

func TestProjectCreateReadinessRequiresValidatedGitConnection(t *testing.T) {
	client := newCodeRepositoryTestClient()

	readiness, err := projectCreateReadiness(context.Background(), client)
	if err != nil {
		t.Fatalf("projectCreateReadiness returned error: %v", err)
	}
	if readiness.GitConnection.Ready {
		t.Fatalf("GitConnection.Ready = true, want false")
	}
	if readiness.GitConnection.ConnectionRef != "" {
		t.Fatalf("GitConnection.ConnectionRef = %q, want empty", readiness.GitConnection.ConnectionRef)
	}
	if readiness.GitConnection.Message != "You need to connect to a Git account before you can continue" {
		t.Fatalf("GitConnection.Message = %q, want missing connection guidance", readiness.GitConnection.Message)
	}
}

func TestProjectCreateReadinessSelectsValidatedGitConnection(t *testing.T) {
	client := newCodeRepositoryTestClient(
		codeConnectionObjectWithValidated("github", metav1.ConditionTrue),
	)

	readiness, err := projectCreateReadiness(context.Background(), client)
	if err != nil {
		t.Fatalf("projectCreateReadiness returned error: %v", err)
	}
	if !readiness.GitConnection.Ready {
		t.Fatalf("GitConnection.Ready = false, want true")
	}
	if readiness.GitConnection.ConnectionRef != "github" {
		t.Fatalf("GitConnection.ConnectionRef = %q, want github", readiness.GitConnection.ConnectionRef)
	}
	if readiness.GitConnection.Message != "" {
		t.Fatalf("GitConnection.Message = %q, want empty", readiness.GitConnection.Message)
	}
}

func TestProjectCreateReadinessReportsGitOpsBindingAndClaims(t *testing.T) {
	client := newCodeRepositoryTestClient(
		codeConnectionObjectWithValidated("github", metav1.ConditionTrue),
		apiBindingObject("deployments", deploymentsAPIExportName, "Bound", deploymentsGitOpsClaims...),
		apiBindingObject("app-studio", appStudioAPIExportName, "Bound", appStudioGitOpsClaims...),
	)

	readiness, err := projectCreateReadiness(context.Background(), client)
	if err != nil {
		t.Fatalf("projectCreateReadiness returned error: %v", err)
	}
	if !readiness.GitOps.Available {
		t.Fatalf("GitOps.Available = false, reason=%q message=%q", readiness.GitOps.Reason, readiness.GitOps.Message)
	}
	if !readiness.GitOps.Deployments.Ready || !readiness.GitOps.AppStudio.Ready {
		t.Fatalf("provider readiness = %#v, want both providers ready", readiness.GitOps)
	}
	if len(readiness.GitOps.Deployments.MissingClaims) != 0 || len(readiness.GitOps.AppStudio.MissingClaims) != 0 {
		t.Fatalf("ready bindings report missing claims: %#v", readiness.GitOps)
	}
}

func TestProjectCreateReadinessReportsMissingAppliedGitOpsClaims(t *testing.T) {
	client := newCodeRepositoryTestClient(
		codeConnectionObjectWithValidated("github", metav1.ConditionTrue),
		apiBindingObject("deployments", deploymentsAPIExportName, "Bound", deploymentsGitOpsClaims[0]),
		apiBindingObject("app-studio", appStudioAPIExportName, "Binding", appStudioGitOpsClaims...),
	)

	readiness, err := projectCreateReadiness(context.Background(), client)
	if err != nil {
		t.Fatalf("projectCreateReadiness returned error: %v", err)
	}
	if readiness.GitOps.Available {
		t.Fatal("GitOps.Available = true, want false for unready bindings")
	}
	if readiness.GitOps.Deployments.Ready {
		t.Fatal("Deployments.Ready = true, want false with missing instances claim")
	}
	if got := readiness.GitOps.Deployments.MissingClaims; len(got) != 1 || got[0] != "infrastructure.faros.sh/instances" {
		t.Fatalf("Deployments.MissingClaims = %#v, want infrastructure instances", got)
	}
	if readiness.GitOps.AppStudio.Bound {
		t.Fatal("AppStudio.Bound = true, want false for Binding phase")
	}
	if !strings.Contains(readiness.GitOps.Reason, "Deployments APIBinding is missing applied claims") ||
		!strings.Contains(readiness.GitOps.Reason, "App Studio APIBinding is not Bound") {
		t.Fatalf("GitOps.Reason = %q, want actionable binding and claim details", readiness.GitOps.Reason)
	}
}

func TestEnsureProjectGitOpsReadinessRejectsMissingBinding(t *testing.T) {
	client := newCodeRepositoryTestClient()
	if err := ensureProjectGitOpsReadiness(context.Background(), client); err == nil ||
		!strings.Contains(err.Error(), "Deployments APIBinding is not Bound") ||
		!strings.Contains(err.Error(), "choose Direct delivery") {
		t.Fatalf("ensureProjectGitOpsReadiness() = %v, want actionable GitOps conflict", err)
	}
}

func newCodeRepositoryTestClient(objects ...runtime.Object) *asclient.Client {
	return asclient.NewFromDynamic(fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			codeConnectionsGVR:      "ConnectionList",
			apiBindingsResource.GVR: "APIBindingList",
		},
		objects...,
	))
}

func codeConnectionObjectWithValidated(name string, status metav1.ConditionStatus) *unstructured.Unstructured {
	u := &unstructured.Unstructured{
		Object: map[string]any{
			"status": map[string]any{
				"conditions": []any{
					map[string]any{"type": codeConditionValidated, "status": string(status)},
				},
			},
		},
	}
	u.SetAPIVersion(codeSchemeGroupVersion.String())
	u.SetKind("Connection")
	u.SetName(name)
	return u
}

func apiBindingObject(name, exportName, phase string, claims ...projectGitOpsRequiredClaim) *unstructured.Unstructured {
	applied := make([]any, 0, len(claims))
	for _, claim := range claims {
		applied = append(applied, map[string]any{
			"group":    claim.Group,
			"resource": claim.Resource,
		})
	}
	u := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{
			"reference": map[string]any{
				"export": map[string]any{"path": projectGitOpsExportPath(exportName), "name": exportName},
			},
		},
		"status": map[string]any{
			"phase":                   phase,
			"appliedPermissionClaims": applied,
		},
	}}
	u.SetAPIVersion(apiBindingsResource.GVR.GroupVersion().String())
	u.SetKind(apiBindingsResource.Kind)
	u.SetName(name)
	return u
}

func projectGitOpsExportPath(exportName string) string {
	if exportName == deploymentsAPIExportName {
		return deploymentsAPIExportPath
	}
	return appStudioAPIExportPath
}
