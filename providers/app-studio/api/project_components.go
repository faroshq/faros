/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"

	"github.com/gorilla/mux"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/validation"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	asclient "github.com/faroshq/provider-app-studio/client"
)

const projectComponentMaxCount = 64

type projectComponentMutationRequest struct {
	Name       string                          `json:"name,omitempty"`
	Kind       aiv1alpha1.ProjectComponentKind `json:"kind"`
	SourcePath string                          `json:"sourcePath"`
	Build      *projectComponentBuildRequest   `json:"build,omitempty"`
	Ports      []projectComponentPortRequest   `json:"ports,omitempty"`
}

type projectComponentBuildRequest struct {
	ContextPath    string `json:"contextPath"`
	DockerfilePath string `json:"dockerfilePath"`
}

type projectComponentPortRequest struct {
	Name          string                              `json:"name"`
	Protocol      aiv1alpha1.ProjectComponentProtocol `json:"protocol"`
	ContainerPort int32                               `json:"containerPort"`
}

type projectComponentsResponse struct {
	Items []aiv1alpha1.ProjectComponentSpec `json:"items"`
}

func projectComponentNameValid(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("component name is required")
	}
	if len(name) > 63 {
		return errors.New("component name must be at most 63 characters")
	}
	if errs := validation.IsDNS1123Label(name); len(errs) > 0 {
		return fmt.Errorf("component name %q is invalid: %s", name, strings.Join(errs, "; "))
	}
	return nil
}

func projectComponentPathValid(value, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	if len(value) > 1024 || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "\\") {
		return "", fmt.Errorf("%s must be a project-relative path", field)
	}
	clean := path.Clean(strings.ReplaceAll(value, "\\", "/"))
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%s must remain inside the project workspace", field)
	}
	return clean, nil
}

func normalizeProjectComponentMutation(name string, req projectComponentMutationRequest) (aiv1alpha1.ProjectComponentSpec, error) {
	name = strings.TrimSpace(name)
	if req.Name != "" && strings.TrimSpace(req.Name) != name {
		return aiv1alpha1.ProjectComponentSpec{}, errors.New("component name in the body must match the URL")
	}
	if err := projectComponentNameValid(name); err != nil {
		return aiv1alpha1.ProjectComponentSpec{}, err
	}
	kind := req.Kind
	if kind == "" {
		kind = aiv1alpha1.ProjectComponentKindService
	}
	if kind != aiv1alpha1.ProjectComponentKindService && kind != aiv1alpha1.ProjectComponentKindWorker {
		return aiv1alpha1.ProjectComponentSpec{}, errors.New("component kind must be Service or Worker")
	}
	sourcePath, err := projectComponentPathValid(req.SourcePath, "sourcePath")
	if err != nil {
		return aiv1alpha1.ProjectComponentSpec{}, err
	}
	component := aiv1alpha1.ProjectComponentSpec{Name: name, Kind: kind, SourcePath: sourcePath}
	if req.Build != nil {
		contextPath, contextErr := projectComponentPathValid(req.Build.ContextPath, "build.contextPath")
		if contextErr != nil {
			return aiv1alpha1.ProjectComponentSpec{}, contextErr
		}
		dockerfilePath, dockerfileErr := projectComponentPathValid(req.Build.DockerfilePath, "build.dockerfilePath")
		if dockerfileErr != nil {
			return aiv1alpha1.ProjectComponentSpec{}, dockerfileErr
		}
		component.Build = &aiv1alpha1.ProjectComponentBuildSpec{ContextPath: contextPath, DockerfilePath: dockerfilePath}
	}
	if len(req.Ports) > 32 {
		return aiv1alpha1.ProjectComponentSpec{}, errors.New("component ports accepts at most 32 entries")
	}
	seenPorts := map[string]struct{}{}
	for _, port := range req.Ports {
		port.Name = strings.TrimSpace(port.Name)
		if err := projectComponentNameValid(port.Name); err != nil {
			return aiv1alpha1.ProjectComponentSpec{}, fmt.Errorf("port: %w", err)
		}
		if _, exists := seenPorts[port.Name]; exists {
			return aiv1alpha1.ProjectComponentSpec{}, fmt.Errorf("duplicate component port %q", port.Name)
		}
		seenPorts[port.Name] = struct{}{}
		if port.Protocol != aiv1alpha1.ProjectComponentProtocolHTTP && port.Protocol != aiv1alpha1.ProjectComponentProtocolHTTPS && port.Protocol != aiv1alpha1.ProjectComponentProtocolTCP {
			return aiv1alpha1.ProjectComponentSpec{}, fmt.Errorf("component port %q protocol must be HTTP, HTTPS, or TCP", port.Name)
		}
		if port.ContainerPort < 1 || port.ContainerPort > 65535 {
			return aiv1alpha1.ProjectComponentSpec{}, fmt.Errorf("component port %q must be between 1 and 65535", port.Name)
		}
		component.Ports = append(component.Ports, aiv1alpha1.ProjectComponentPortSpec{Name: port.Name, Protocol: port.Protocol, ContainerPort: port.ContainerPort})
	}
	return component, nil
}

func projectComponentsView(project *aiv1alpha1.Project) projectComponentsResponse {
	if project == nil {
		return projectComponentsResponse{Items: []aiv1alpha1.ProjectComponentSpec{}}
	}
	items := append([]aiv1alpha1.ProjectComponentSpec(nil), project.Spec.Components...)
	sort.SliceStable(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return projectComponentsResponse{Items: items}
}

func (s *Server) updateProjectComponent(ctx context.Context, c *asclient.Client, project *aiv1alpha1.Project, name string, component *aiv1alpha1.ProjectComponentSpec, remove bool) (*aiv1alpha1.Project, error) {
	if c == nil || project == nil {
		return nil, errors.New("project client and project are required")
	}
	for attempt := 0; attempt < 3; attempt++ {
		current, err := c.Projects().Get(ctx, project.Name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		next := current.DeepCopy()
		index := -1
		for i := range next.Spec.Components {
			if next.Spec.Components[i].Name == name {
				index = i
				break
			}
		}
		if remove {
			if index < 0 {
				return nil, apierrors.NewNotFound(aiv1alpha1.SchemeGroupVersion.WithResource("projects").GroupResource(), name)
			}
			// A component is a stable production identity, so deleting it while a
			// development route still points at it would leave an invalid
			// DevelopmentService. Keep the mutation atomic from the caller's
			// perspective and require the route to be edited or removed first.
			services, servicesErr := s.listOwnedDevelopmentServices(ctx, c, current)
			if servicesErr != nil && !apierrors.IsNotFound(servicesErr) {
				return nil, fmt.Errorf("check DevelopmentService component references: %w", servicesErr)
			}
			for _, service := range services {
				componentRef, _, _ := unstructured.NestedString(service.Object, "spec", "componentRef")
				if strings.TrimSpace(componentRef) == name {
					logicalName := projectDevelopmentServiceLogicalName(service)
					return nil, fmt.Errorf("component %q is referenced by DevelopmentService %q; remove or change componentRef first", name, logicalName)
				}
			}
			next.Spec.Components = append(next.Spec.Components[:index], next.Spec.Components[index+1:]...)
		} else if index >= 0 {
			next.Spec.Components[index] = *component
		} else {
			if len(next.Spec.Components) >= projectComponentMaxCount {
				return nil, errors.New("Project already has the maximum number of components")
			}
			next.Spec.Components = append(next.Spec.Components, *component)
		}
		updated, err := c.Projects().Update(ctx, next, metav1.UpdateOptions{})
		if err == nil {
			return updated, nil
		}
		if !apierrors.IsConflict(err) || attempt == 2 {
			return nil, err
		}
	}
	return nil, errors.New("Project component update conflicted")
}

func (s *Server) listProjectComponents(w http.ResponseWriter, r *http.Request) {
	_, _, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, projectComponentsView(project))
}

func (s *Server) upsertProjectComponent(w http.ResponseWriter, r *http.Request) {
	c, _, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(mux.Vars(r)["component"])
	var request projectComponentMutationRequest
	if !decodeStrictJSON(w, r, &request) {
		return
	}
	component, err := normalizeProjectComponentMutation(name, request)
	if err != nil {
		writeProjectError(w, newValidationError(err.Error()))
		return
	}
	updated, err := s.updateProjectComponent(r.Context(), c, project, name, &component, false)
	if err != nil {
		writeProjectError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"component": component, "project": projectComponentsView(updated)})
}

func (s *Server) deleteProjectComponent(w http.ResponseWriter, r *http.Request) {
	c, _, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(mux.Vars(r)["component"])
	if err := projectComponentNameValid(name); err != nil {
		writeProjectError(w, newValidationError(err.Error()))
		return
	}
	if _, err := s.updateProjectComponent(r.Context(), c, project, name, nil, true); err != nil {
		writeProjectError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) assistantUpsertProjectComponent(ctx context.Context, req projectAssistantToolCallRequest) (string, error) {
	if req.Project == nil {
		return "", errors.New("no project on this run")
	}
	name := projectToolString(req.Arguments["component"])
	raw, err := json.Marshal(req.Arguments)
	if err != nil {
		return "", err
	}
	var request projectComponentMutationRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return "", err
	}
	component, err := normalizeProjectComponentMutation(name, request)
	if err != nil {
		return "", err
	}
	c, err := s.clientFor(req.Identity)
	if err != nil {
		return "", err
	}
	updated, err := s.updateProjectComponent(ctx, c, req.Project, name, &component, false)
	if err != nil {
		return "", err
	}
	refreshProjectToolSnapshot(req.Project, updated)
	return projectAssistantToolJSONResult(map[string]any{"component": component, "components": projectComponentsView(updated).Items}, nil)
}

func (s *Server) assistantDeleteProjectComponent(ctx context.Context, req projectAssistantToolCallRequest) (string, error) {
	if req.Project == nil {
		return "", errors.New("no project on this run")
	}
	name := projectToolString(req.Arguments["component"])
	if err := projectComponentNameValid(name); err != nil {
		return "", err
	}
	c, err := s.clientFor(req.Identity)
	if err != nil {
		return "", err
	}
	updated, err := s.updateProjectComponent(ctx, c, req.Project, name, nil, true)
	if err != nil {
		return "", err
	}
	refreshProjectToolSnapshot(req.Project, updated)
	return projectAssistantToolJSONResult(map[string]any{"deleted": true, "component": name}, nil)
}
