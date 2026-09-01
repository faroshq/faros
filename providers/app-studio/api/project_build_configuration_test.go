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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	asclient "github.com/faroshq/provider-app-studio/client"
)

func TestNormalizeProjectBuildConfigurationValidatesRepositoryWorkflow(t *testing.T) {
	for _, workflowPath := range []string{
		"build.yaml",
		".github/workflows/nested/build.yaml",
		".github/workflows/build.json",
		"../.github/workflows/build.yaml",
	} {
		if _, err := normalizeProjectBuildConfiguration(projectBuildConfigurationRequest{WorkflowPath: workflowPath}); err == nil {
			t.Fatalf("workflowPath %q accepted, want validation error", workflowPath)
		}
	}
	if _, err := normalizeProjectBuildConfiguration(projectBuildConfigurationRequest{WorkflowPath: ".github/workflows/build.yaml", Clear: true}); err == nil {
		t.Fatal("clear with workflowPath accepted")
	}
	build, err := normalizeProjectBuildConfiguration(projectBuildConfigurationRequest{WorkflowPath: " .github/workflows/release.yml "})
	if err != nil || build == nil || build.WorkflowPath != ".github/workflows/release.yml" {
		t.Fatalf("normalized build = %+v err=%v", build, err)
	}
	if build, err := normalizeProjectBuildConfiguration(projectBuildConfigurationRequest{Clear: true}); err != nil || build != nil {
		t.Fatalf("clear build = %+v err=%v", build, err)
	}
}

func TestProjectBuildConfigurationHTTPRoundTripAndClear(t *testing.T) {
	project := developmentServicesTestProject("shop", "project-uid")
	client := developmentServicesTestClient(project)
	server := &Server{projectClientFor: func(identity) (*asclient.Client, error) { return client, nil }}

	put := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/projects/shop/build", strings.NewReader(body))
		req = mux.SetURLVars(req, map[string]string{"project": "shop"})
		setDevelopmentServicesTestIdentity(req)
		response := httptest.NewRecorder()
		server.putProjectBuildConfiguration(response, req)
		return response
	}

	response := put(`{"workflowPath":".github/workflows/release.yaml"}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"workflowPath":".github/workflows/release.yaml"`) {
		t.Fatalf("set response status=%d body=%s", response.Code, response.Body.String())
	}
	stored, err := client.Projects().Get(context.Background(), project.Name, metav1.GetOptions{})
	if err != nil || stored.Spec.Build == nil || stored.Spec.Build.WorkflowPath != ".github/workflows/release.yaml" {
		t.Fatalf("stored build = %+v err=%v", stored.Spec.Build, err)
	}

	response = put(`{"clear":true}`)
	if response.Code != http.StatusOK || response.Body.String() != "{}\n" {
		t.Fatalf("clear response status=%d body=%q", response.Code, response.Body.String())
	}
	stored, err = client.Projects().Get(context.Background(), project.Name, metav1.GetOptions{})
	if err != nil || stored.Spec.Build != nil {
		t.Fatalf("stored build after clear = %+v err=%v", stored.Spec.Build, err)
	}
}

func TestProjectBuildConfigurationAssistantToolsDeclareRiskAndPersist(t *testing.T) {
	project := developmentServicesTestProject("shop", "project-uid")
	client := developmentServicesTestClient(project)
	server := &Server{projectClientFor: func(identity) (*asclient.Client, error) { return client, nil }}
	registry := server.projectAssistantToolRegistry()
	read, ok := registry.Spec(projectToolGetProjectBuildConfiguration)
	if !ok || read.Risk != projectAssistantToolRiskRead || !read.ParallelSafe {
		t.Fatalf("read build tool = %+v found=%t", read, ok)
	}
	write, ok := registry.Spec(projectToolSetProjectBuildWorkflow)
	if !ok || write.Risk != projectAssistantToolRiskWrite || !strings.Contains(write.Description, ".github/workflows/") || !strings.Contains(string(write.Parameters), `"clear"`) {
		t.Fatalf("write build tool = %+v found=%t", write, ok)
	}
	tool, ok := registry.Get(projectToolSetProjectBuildWorkflow)
	if !ok {
		t.Fatal("set build workflow tool is not callable")
	}
	result, err := tool.Call(context.Background(), projectAssistantToolCallRequest{
		Project:   project,
		Identity:  identity{tenantPath: "root:faros:tenants:org:workspace", clusterID: "cluster-a", user: "alice"},
		Arguments: map[string]any{"workflowPath": ".github/workflows/build.yml"},
	})
	if err != nil || !strings.Contains(result, ".github/workflows/build.yml") {
		t.Fatalf("tool result=%q err=%v", result, err)
	}
	if project.Spec.Build == nil || project.Spec.Build.WorkflowPath != ".github/workflows/build.yml" {
		t.Fatalf("tool snapshot build = %+v", project.Spec.Build)
	}
}
