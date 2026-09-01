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
	"errors"
	"net/http"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	asclient "github.com/faroshq/provider-app-studio/client"
)

type projectBuildConfigurationRequest struct {
	WorkflowPath string `json:"workflowPath,omitempty"`
	Clear        bool   `json:"clear,omitempty"`
}

type projectBuildConfigurationResponse struct {
	WorkflowPath string `json:"workflowPath,omitempty"`
}

func projectBuildConfigurationView(project *aiv1alpha1.Project) projectBuildConfigurationResponse {
	if project == nil || project.Spec.Build == nil {
		return projectBuildConfigurationResponse{}
	}
	return projectBuildConfigurationResponse{WorkflowPath: strings.TrimSpace(project.Spec.Build.WorkflowPath)}
}

func normalizeProjectBuildConfiguration(req projectBuildConfigurationRequest) (*aiv1alpha1.ProjectBuildSpec, error) {
	workflowPath := strings.TrimSpace(req.WorkflowPath)
	if req.Clear {
		if workflowPath != "" {
			return nil, newValidationError("workflowPath must be omitted when clear is true")
		}
		return nil, nil
	}
	if workflowPath == "" {
		return nil, newValidationError("workflowPath is required; use clear=true to remove the build workflow")
	}
	candidate := &aiv1alpha1.Project{Spec: aiv1alpha1.ProjectSpec{Build: &aiv1alpha1.ProjectBuildSpec{WorkflowPath: workflowPath}}}
	if _, err := projectBuildWorkflowPath(candidate); err != nil {
		return nil, err
	}
	return candidate.Spec.Build, nil
}

func updateProjectBuildConfiguration(ctx context.Context, c *asclient.Client, project *aiv1alpha1.Project, build *aiv1alpha1.ProjectBuildSpec) (*aiv1alpha1.Project, error) {
	if c == nil || project == nil {
		return nil, errors.New("project client and project are required")
	}
	for attempt := 0; attempt < 3; attempt++ {
		current, err := c.Projects().Get(ctx, project.Name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		next := current.DeepCopy()
		if build == nil {
			next.Spec.Build = nil
		} else {
			next.Spec.Build = build.DeepCopy()
		}
		updated, err := c.Projects().Update(ctx, next, metav1.UpdateOptions{})
		if err == nil {
			return updated, nil
		}
		if !apierrors.IsConflict(err) || attempt == 2 {
			return nil, err
		}
	}
	return nil, errors.New("Project build configuration update conflicted")
}

func (s *Server) getProjectBuildConfiguration(w http.ResponseWriter, r *http.Request) {
	_, _, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, projectBuildConfigurationView(project))
}

func (s *Server) putProjectBuildConfiguration(w http.ResponseWriter, r *http.Request) {
	c, _, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	var req projectBuildConfigurationRequest
	if !decodeStrictJSON(w, r, &req) {
		return
	}
	build, err := normalizeProjectBuildConfiguration(req)
	if err != nil {
		writeProjectError(w, err)
		return
	}
	updated, err := updateProjectBuildConfiguration(r.Context(), c, project, build)
	if err != nil {
		writeProjectError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, projectBuildConfigurationView(updated))
}

func (s *Server) assistantGetProjectBuildConfiguration(_ context.Context, req projectAssistantToolCallRequest) (string, error) {
	if req.Project == nil {
		return "", errors.New("no project on this run")
	}
	return projectAssistantToolJSONResult(projectBuildConfigurationView(req.Project), nil)
}

func (s *Server) assistantSetProjectBuildWorkflow(ctx context.Context, req projectAssistantToolCallRequest) (string, error) {
	if req.Project == nil {
		return "", errors.New("no project on this run")
	}
	request := projectBuildConfigurationRequest{
		WorkflowPath: projectToolString(req.Arguments["workflowPath"]),
		Clear:        projectToolBool(req.Arguments["clear"]),
	}
	build, err := normalizeProjectBuildConfiguration(request)
	if err != nil {
		return "", err
	}
	c, err := s.clientFor(req.Identity)
	if err != nil {
		return "", err
	}
	updated, err := updateProjectBuildConfiguration(ctx, c, req.Project, build)
	if err != nil {
		return "", err
	}
	refreshProjectToolSnapshot(req.Project, updated)
	return projectAssistantToolJSONResult(projectBuildConfigurationView(updated), nil)
}
