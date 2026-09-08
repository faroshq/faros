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
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	asclient "github.com/faroshq/provider-app-studio/client"
)

func validateProjectRepositoryMode(req CreateProjectRequest) error {
	switch req.RepositoryMode {
	case "", "auto", "create":
	case "none":
		if strings.TrimSpace(req.ConnectionRef) != "" || strings.TrimSpace(req.ExistingRepositoryRef) != "" {
			return newValidationError("repositoryMode none cannot include connectionRef or existingRepositoryRef")
		}
	default:
		return newValidationError("repositoryMode must be auto, none, or create")
	}
	return nil
}

func (s *Server) prepareOptionalProjectRepository(ctx context.Context, c *asclient.Client, req CreateProjectRequest, repoBase string) (projectRepositoryPlan, error) {
	if req.RepositoryMode == "none" {
		return projectRepositoryPlan{}, nil
	}
	if req.ConnectionRef == "" && req.RepositoryMode != "create" {
		readiness, err := inspectCodeConnectionReadiness(ctx, c)
		if err != nil {
			return projectRepositoryPlan{}, err
		}
		if !readiness.Ready {
			return projectRepositoryPlan{}, nil
		}
		req.ConnectionRef = readiness.ConnectionRef
	}
	return s.prepareProjectRepository(ctx, c, req.ConnectionRef, repoBase, req.DisplayName, req.Description)
}

// putProjectRepository records explicit permission to create a private repository
// and persist this project's current source. ResourceVersion fences concurrent
// attachments; the reconciler owns provisioning and retryable initial commits.
func (s *Server) putProjectRepository(w http.ResponseWriter, r *http.Request) {
	c, id, p, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	var req struct {
		ConnectionRef string `json:"connectionRef"`
	}
	if !decodeStrictJSON(w, r, &req) {
		return
	}
	req.ConnectionRef = strings.TrimSpace(req.ConnectionRef)
	if req.ConnectionRef == "" {
		writeProjectError(w, newValidationError("connectionRef is required"))
		return
	}
	if p.Spec.Repository != nil {
		if p.Spec.Repository.ConnectionRef != req.ConnectionRef || p.Spec.Repository.Adopted {
			writeStatus(w, http.StatusConflict, "Conflict", "This project already has a repository; it cannot be replaced here.")
			return
		}
	} else {
		plan, err := s.prepareProjectRepository(r.Context(), c, req.ConnectionRef, slugifyProjectName(p.Spec.DisplayName), p.Spec.DisplayName, p.Spec.Description)
		if err != nil {
			writeProjectError(w, err)
			return
		}
		p.Spec.Repository = plan.projectBinding()
		if p.Annotations == nil {
			p.Annotations = map[string]string{}
		}
		p.Annotations["ai.faros.sh/initialize-repository"] = plan.Ref
		p.Annotations["ai.faros.sh/org-uuid"] = id.orgUUID
		p.Annotations["ai.faros.sh/workspace-uuid"] = id.workspaceUUID
	}
	// Even an idempotent retry requires update permission as the caller.
	updated, err := c.Projects().Update(r.Context(), p, metav1.UpdateOptions{})
	if err != nil {
		writeProjectError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.projectViewWithSourceRevision(r.Context(), c, updated, id))
}
