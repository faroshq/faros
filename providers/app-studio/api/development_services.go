/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package api

// This file is the App Studio boundary for Infrastructure DevelopmentService
// resources.  App Studio owns project intent and the composition references;
// Infrastructure owns the process supervisor, Service, HTTPRoute, access
// policy, and all observed runtime conditions.  Keep this adapter deliberately
// unstructured so the two standalone provider modules do not need a Go import
// edge between their public APIs.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	asclient "github.com/faroshq/provider-app-studio/client"
	"github.com/faroshq/provider-app-studio/tenant"
)

const (
	projectDevelopmentServiceEnvironment    = "development"
	projectDevelopmentServicePrivate        = "private"
	projectDevelopmentServicePublic         = "public"
	projectDevelopmentServiceHTTP           = "HTTP"
	projectDevelopmentServiceMax            = 8
	projectDevelopmentServicePortMin        = 1
	projectDevelopmentServicePortMax        = 65535
	projectDevelopmentServiceRestartAt      = "faros.sh/development-service-restart-at"
	projectDevelopmentServiceNameLabel      = "app-studio.faros.sh/service-name"
	projectDevelopmentServicePhysicalPrefix = "devsvc"
	projectDevelopmentInfrastructurePrivate = "Private"
	projectDevelopmentInfrastructurePublic  = "Public"
	projectDevelopmentServiceLogsMaxBytes   = 512 << 10
)

var developmentServicesResource = tenant.Resource{
	GVR: schema.GroupVersionResource{
		Group: "infrastructure.faros.sh", Version: "v1alpha1", Resource: "developmentservices",
	},
	Kind: "DevelopmentService", Plural: "DevelopmentServices",
}

// projectDevelopmentServiceView is intentionally a status-oriented view. It
// does not expose Secret data or the supervisor's internal credentials.
type projectDevelopmentServiceView struct {
	Name           string                               `json:"name"`
	ComponentRef   string                               `json:"componentRef,omitempty"`
	Enabled        bool                                 `json:"enabled"`
	Command        projectDevelopmentServiceCommand     `json:"command,omitempty"`
	Endpoint       projectDevelopmentServiceEndpoint    `json:"endpoint,omitempty"`
	Exposure       projectDevelopmentServiceExposure    `json:"exposure,omitempty"`
	RestartPolicy  string                               `json:"restartPolicy,omitempty"`
	ConnectionRefs []string                             `json:"connectionRefs,omitempty"`
	Host           string                               `json:"host,omitempty"`
	URL            string                               `json:"url,omitempty"`
	Phase          string                               `json:"phase,omitempty"`
	Ready          bool                                 `json:"ready"`
	Process        projectDevelopmentServiceProcess     `json:"process,omitempty"`
	Conditions     []projectDevelopmentServiceCondition `json:"conditions,omitempty"`
	ObservedAt     *time.Time                           `json:"observedAt,omitempty"`
	Error          string                               `json:"error,omitempty"`
}

type projectDevelopmentServiceCommand struct {
	Argv             []string `json:"argv,omitempty"`
	WorkingDirectory string   `json:"workingDirectory,omitempty"`
}

type projectDevelopmentServiceEndpoint struct {
	Protocol   string `json:"protocol,omitempty"`
	Port       int64  `json:"port,omitempty"`
	HealthPath string `json:"healthPath,omitempty"`
}

type projectDevelopmentServiceExposure struct {
	Visibility string `json:"visibility,omitempty"`
}

type projectDevelopmentServiceProcess struct {
	Running       bool   `json:"running"`
	PortListening bool   `json:"portListening"`
	Reachable     bool   `json:"reachable"`
	Phase         string `json:"phase,omitempty"`
	RestartCount  int64  `json:"restartCount,omitempty"`
	LastExitCode  *int64 `json:"lastExitCode,omitempty"`
	Message       string `json:"message,omitempty"`
}

type projectDevelopmentServiceCondition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Reason             string `json:"reason,omitempty"`
	Message            string `json:"message,omitempty"`
	ObservedGeneration int64  `json:"observedGeneration,omitempty"`
}

type projectDevelopmentListenerView struct {
	Port     int64  `json:"port"`
	Protocol string `json:"protocol,omitempty"`
	Address  string `json:"address,omitempty"`
	Process  string `json:"process,omitempty"`
}

type projectDevelopmentServicesResponse struct {
	Items             []projectDevelopmentServiceView  `json:"items"`
	PrimaryServiceRef string                           `json:"primaryServiceRef,omitempty"`
	Listeners         []projectDevelopmentListenerView `json:"listeners,omitempty"`
}

type projectDevelopmentServiceCommandRequest struct {
	Argv             []string `json:"argv"`
	WorkingDirectory string   `json:"workingDirectory,omitempty"`
}

type projectDevelopmentServiceEndpointRequest struct {
	Protocol   string `json:"protocol,omitempty"`
	Port       int64  `json:"port"`
	HealthPath string `json:"healthPath,omitempty"`
}

type projectDevelopmentServiceExposureRequest struct {
	Visibility string `json:"visibility,omitempty"`
}

type projectDevelopmentServiceMutationRequest struct {
	ComponentRef  string                                    `json:"componentRef,omitempty"`
	Enabled       *bool                                     `json:"enabled,omitempty"`
	Command       *projectDevelopmentServiceCommandRequest  `json:"command,omitempty"`
	Endpoint      *projectDevelopmentServiceEndpointRequest `json:"endpoint,omitempty"`
	Exposure      *projectDevelopmentServiceExposureRequest `json:"exposure,omitempty"`
	RestartPolicy string                                    `json:"restartPolicy,omitempty"`
	ConfirmPublic bool                                      `json:"confirmPublic,omitempty"`
}

type projectDevelopmentServicePrimaryRequest struct {
	Service string `json:"service,omitempty"`
}

type projectDevelopmentServiceMutationResponse struct {
	Service projectDevelopmentServiceView `json:"service"`
}

// projectDevelopmentServiceBelongsToProject rejects same-name resources from
// another Project. Names are not an authorization boundary because Projects
// can be deleted and recreated; when a UID is available it must match too.
func projectDevelopmentServiceBelongsToProject(obj *unstructured.Unstructured, project *aiv1alpha1.Project) bool {
	if obj == nil || project == nil {
		return false
	}
	labels := obj.GetLabels()
	projectName := strings.TrimSpace(project.Name)
	projectUID := strings.TrimSpace(string(project.UID))
	labelName := strings.TrimSpace(labels["faros.sh/project"])
	if labelName == "" {
		labelName, _, _ = unstructured.NestedString(obj.Object, "spec", "projectRef", "name")
	}
	if labelName != projectName {
		return false
	}
	if projectUID == "" {
		return false
	}
	labelUID := strings.TrimSpace(labels["faros.sh/project-uid"])
	if labelUID == "" {
		labelUID, _, _ = unstructured.NestedString(obj.Object, "spec", "projectRef", "uid")
	}
	return labelUID == projectUID
}

func projectDevelopmentServiceLogicalName(obj *unstructured.Unstructured) string {
	if obj == nil {
		return ""
	}
	if value := strings.TrimSpace(obj.GetLabels()[projectDevelopmentServiceNameLabel]); value != "" {
		return value
	}
	// Resources created before the project-local physical-name contract used
	// the logical name as metadata.name. Keep those objects addressable through
	// the logical HTTP surface while never exposing their physical name.
	return strings.TrimSpace(obj.GetName())
}

// projectDevelopmentServicePhysicalName turns a Project-local logical service
// name into a cluster-scoped DevelopmentService name. The Project UID is part
// of the digest so deleting and recreating a Project with the same name cannot
// inherit its predecessor's service object.
func projectDevelopmentServicePhysicalName(project *aiv1alpha1.Project, logicalName string) string {
	if project == nil {
		return ""
	}
	logicalName = strings.TrimSpace(logicalName)
	projectPart := dnsSafeSandboxName(project.Name)
	logicalPart := dnsSafeSandboxName(logicalName)
	if len(logicalPart) > 32 {
		logicalPart = strings.TrimRight(logicalPart[:32], "-")
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{project.Name, string(project.UID), logicalName}, "\x00")))
	return fmt.Sprintf("%s-%s-%s-%s", projectDevelopmentServicePhysicalPrefix, projectPart, logicalPart, hex.EncodeToString(sum[:6]))
}

func projectDevelopmentServiceVisibilityProvider(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case projectDevelopmentServicePublic:
		return projectDevelopmentInfrastructurePublic
	default:
		return projectDevelopmentInfrastructurePrivate
	}
}

func projectDevelopmentServiceVisibilityHTTP(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), projectDevelopmentInfrastructurePublic) || strings.EqualFold(strings.TrimSpace(value), projectDevelopmentServicePublic) {
		return projectDevelopmentServicePublic
	}
	return projectDevelopmentServicePrivate
}

func nestedInt64OrZero(obj *unstructured.Unstructured, fields ...string) int64 {
	if obj == nil {
		return 0
	}
	value, found, _ := unstructured.NestedFieldNoCopy(obj.Object, fields...)
	if !found {
		return 0
	}
	switch typed := value.(type) {
	case int64:
		return typed
	case int32:
		return int64(typed)
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	case float32:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	default:
		return 0
	}
}

func projectDevelopmentServiceNameValid(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("development service name is required")
	}
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		return fmt.Errorf("development service name %q is invalid: %s", name, strings.Join(errs, "; "))
	}
	return nil
}

func projectDevelopmentServiceComponentValid(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		return fmt.Errorf("componentRef %q is invalid: %s", name, strings.Join(errs, "; "))
	}
	return nil
}

func projectDevelopmentServiceComponentExists(project *aiv1alpha1.Project, name string) bool {
	if project == nil || strings.TrimSpace(name) == "" {
		return false
	}
	for _, component := range project.Spec.Components {
		if strings.TrimSpace(component.Name) == strings.TrimSpace(name) {
			return true
		}
	}
	return false
}

func projectDevelopmentServiceNormalizeRequest(req *projectDevelopmentServiceMutationRequest, existing *unstructured.Unstructured) (projectDevelopmentServiceMutationRequest, error) {
	if req == nil {
		return projectDevelopmentServiceMutationRequest{}, errors.New("development service request is required")
	}
	out := *req
	if existing != nil {
		if out.ComponentRef == "" {
			out.ComponentRef, _, _ = unstructured.NestedString(existing.Object, "spec", "componentRef")
		}
		if out.Enabled == nil {
			value, found, _ := unstructured.NestedBool(existing.Object, "spec", "enabled")
			if found {
				out.Enabled = &value
			}
		}
		if out.Command == nil {
			argv, _, _ := unstructured.NestedStringSlice(existing.Object, "spec", "command", "argv")
			wd, _, _ := unstructured.NestedString(existing.Object, "spec", "command", "workingDirectory")
			if len(argv) > 0 || wd != "" {
				out.Command = &projectDevelopmentServiceCommandRequest{Argv: argv, WorkingDirectory: wd}
			}
		}
		if out.Endpoint == nil {
			protocol, _, _ := unstructured.NestedString(existing.Object, "spec", "endpoint", "protocol")
			port := nestedInt64OrZero(existing, "spec", "endpoint", "port")
			healthPath, _, _ := unstructured.NestedString(existing.Object, "spec", "endpoint", "healthPath")
			if protocol != "" || port != 0 || healthPath != "" {
				out.Endpoint = &projectDevelopmentServiceEndpointRequest{Protocol: protocol, Port: port, HealthPath: healthPath}
			}
		}
		if out.Exposure == nil {
			visibility, _, _ := unstructured.NestedString(existing.Object, "spec", "exposure", "visibility")
			if visibility != "" {
				out.Exposure = &projectDevelopmentServiceExposureRequest{Visibility: projectDevelopmentServiceVisibilityHTTP(visibility)}
			}
		}
		if out.RestartPolicy == "" {
			out.RestartPolicy, _, _ = unstructured.NestedString(existing.Object, "spec", "restartPolicy")
		}
	}
	if out.Enabled == nil {
		value := true
		out.Enabled = &value
	}
	if out.Command == nil || len(out.Command.Argv) == 0 {
		return projectDevelopmentServiceMutationRequest{}, errors.New("command.argv is required")
	}
	if len(out.Command.Argv) > 32 {
		return projectDevelopmentServiceMutationRequest{}, errors.New("command.argv accepts at most 32 arguments")
	}
	for _, arg := range out.Command.Argv {
		if strings.TrimSpace(arg) == "" || len(arg) > 256 {
			return projectDevelopmentServiceMutationRequest{}, errors.New("command arguments must be non-empty and at most 256 bytes")
		}
	}
	workingDirectory := strings.TrimSpace(out.Command.WorkingDirectory)
	if workingDirectory == "" {
		workingDirectory = "."
	}
	if strings.HasPrefix(workingDirectory, "/") || strings.Contains(workingDirectory, "..") {
		return projectDevelopmentServiceMutationRequest{}, errors.New("command.workingDirectory must remain inside the project workspace")
	}
	out.Command.WorkingDirectory = workingDirectory
	if out.Endpoint == nil {
		return projectDevelopmentServiceMutationRequest{}, errors.New("endpoint is required")
	}
	protocol := strings.TrimSpace(out.Endpoint.Protocol)
	if protocol == "" {
		protocol = projectDevelopmentServiceHTTP
	}
	if !strings.EqualFold(protocol, projectDevelopmentServiceHTTP) {
		return projectDevelopmentServiceMutationRequest{}, errors.New("endpoint.protocol must be HTTP")
	}
	out.Endpoint.Protocol = projectDevelopmentServiceHTTP
	if out.Endpoint.Port < projectDevelopmentServicePortMin || out.Endpoint.Port > projectDevelopmentServicePortMax {
		return projectDevelopmentServiceMutationRequest{}, fmt.Errorf("endpoint.port must be between %d and %d", projectDevelopmentServicePortMin, projectDevelopmentServicePortMax)
	}
	if out.Endpoint.Port >= 7070 && out.Endpoint.Port <= 7073 {
		return projectDevelopmentServiceMutationRequest{}, errors.New("endpoint.port is reserved by the sandbox control plane")
	}
	healthPath := strings.TrimSpace(out.Endpoint.HealthPath)
	if healthPath == "" {
		healthPath = "/"
	}
	if !strings.HasPrefix(healthPath, "/") || strings.HasPrefix(healthPath, "//") || len(healthPath) > 512 {
		return projectDevelopmentServiceMutationRequest{}, errors.New("endpoint.healthPath must be an absolute path of at most 512 bytes")
	}
	out.Endpoint.HealthPath = healthPath
	visibility := projectDevelopmentServicePrivate
	if out.Exposure != nil && strings.TrimSpace(out.Exposure.Visibility) != "" {
		visibility = strings.ToLower(strings.TrimSpace(out.Exposure.Visibility))
	}
	if visibility != projectDevelopmentServicePrivate && visibility != projectDevelopmentServicePublic {
		return projectDevelopmentServiceMutationRequest{}, errors.New("exposure.visibility must be private or public")
	}
	if visibility == projectDevelopmentServicePublic && !out.ConfirmPublic {
		return projectDevelopmentServiceMutationRequest{}, errors.New("public development exposure requires confirmPublic=true")
	}
	out.Exposure = &projectDevelopmentServiceExposureRequest{Visibility: visibility}
	if err := projectDevelopmentServiceComponentValid(out.ComponentRef); err != nil {
		return projectDevelopmentServiceMutationRequest{}, err
	}
	if out.RestartPolicy == "" {
		out.RestartPolicy = "Always"
	}
	if !strings.EqualFold(out.RestartPolicy, "Always") && !strings.EqualFold(out.RestartPolicy, "OnFailure") && !strings.EqualFold(out.RestartPolicy, "Never") {
		return projectDevelopmentServiceMutationRequest{}, errors.New("restartPolicy must be Always, OnFailure, or Never")
	}
	return out, nil
}

func projectDevelopmentServiceObject(project *aiv1alpha1.Project, logicalName, sandboxName, sandboxUID string, req projectDevelopmentServiceMutationRequest, existing *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	if project == nil {
		return nil, errors.New("project is required")
	}
	logicalName = strings.TrimSpace(logicalName)
	if err := projectDevelopmentServiceNameValid(logicalName); err != nil {
		return nil, err
	}
	if strings.TrimSpace(sandboxName) == "" || strings.TrimSpace(sandboxUID) == "" {
		return nil, errors.New("development service must reference the project universal sandbox")
	}
	physicalName := projectDevelopmentServicePhysicalName(project, logicalName)
	if existing != nil && strings.TrimSpace(existing.GetName()) != "" {
		// Preserve a legacy project-owned object while it is being migrated. New
		// resources always use the deterministic physical name above.
		physicalName = existing.GetName()
	}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "infrastructure.faros.sh/v1alpha1",
		"kind":       "DevelopmentService",
		"metadata": map[string]any{
			"name": physicalName,
		},
	}}
	if existing != nil {
		obj = existing.DeepCopy()
	}
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels["faros.sh/project"] = project.Name
	if project.UID != "" {
		labels["faros.sh/project-uid"] = string(project.UID)
	}
	labels["faros.sh/environment"] = projectDevelopmentServiceEnvironment
	labels[projectDevelopmentServiceNameLabel] = logicalName
	obj.SetLabels(labels)
	obj.SetAPIVersion("infrastructure.faros.sh/v1alpha1")
	obj.SetKind("DevelopmentService")
	obj.SetName(physicalName)
	if project.UID != "" {
		controller := true
		blockOwnerDeletion := true
		obj.SetOwnerReferences([]metav1.OwnerReference{{
			APIVersion: aiv1alpha1.SchemeGroupVersion.String(), Kind: "Project", Name: project.Name,
			UID: project.UID, Controller: &controller, BlockOwnerDeletion: &blockOwnerDeletion,
		}})
	}
	_ = unstructured.SetNestedField(obj.Object, map[string]any{
		"name": project.Name,
		"uid":  string(project.UID),
	}, "spec", "projectRef")
	_ = unstructured.SetNestedField(obj.Object, projectDevelopmentServiceEnvironment, "spec", "environment")
	_ = unstructured.SetNestedField(obj.Object, map[string]any{
		"name": sandboxName,
		"uid":  sandboxUID,
	}, "spec", "sandboxRef")
	if req.ComponentRef != "" {
		_ = unstructured.SetNestedField(obj.Object, req.ComponentRef, "spec", "componentRef")
	} else {
		unstructured.RemoveNestedField(obj.Object, "spec", "componentRef")
	}
	_ = unstructured.SetNestedField(obj.Object, *req.Enabled, "spec", "enabled")
	_ = unstructured.SetNestedField(obj.Object, map[string]any{
		"argv":             stringSliceAny(req.Command.Argv),
		"workingDirectory": req.Command.WorkingDirectory,
	}, "spec", "command")
	_ = unstructured.SetNestedField(obj.Object, map[string]any{
		"protocol":   req.Endpoint.Protocol,
		"port":       req.Endpoint.Port,
		"healthPath": req.Endpoint.HealthPath,
	}, "spec", "endpoint")
	_ = unstructured.SetNestedField(obj.Object, map[string]any{
		"visibility": projectDevelopmentServiceVisibilityProvider(req.Exposure.Visibility),
	}, "spec", "exposure")
	_ = unstructured.SetNestedField(obj.Object, req.RestartPolicy, "spec", "restartPolicy")
	// connectionRefs is controller-owned Project composition state. Preserve an
	// existing derived value on service edits; callers cannot author it through
	// this mutation surface.
	return obj, nil
}

func stringSliceAny(values []string) []any {
	out := make([]any, len(values))
	for i := range values {
		out[i] = values[i]
	}
	return out
}

func projectDevelopmentServiceViewFromUnstructured(obj *unstructured.Unstructured) projectDevelopmentServiceView {
	view := projectDevelopmentServiceView{}
	if obj == nil {
		return view
	}
	view.Name = projectDevelopmentServiceLogicalName(obj)
	view.ComponentRef, _, _ = unstructured.NestedString(obj.Object, "spec", "componentRef")
	view.Enabled, _, _ = unstructured.NestedBool(obj.Object, "spec", "enabled")
	if _, found, _ := unstructured.NestedFieldNoCopy(obj.Object, "spec", "enabled"); !found {
		view.Enabled = true
	}
	view.Command.Argv, _, _ = unstructured.NestedStringSlice(obj.Object, "spec", "command", "argv")
	view.Command.WorkingDirectory, _, _ = unstructured.NestedString(obj.Object, "spec", "command", "workingDirectory")
	view.Endpoint.Protocol, _, _ = unstructured.NestedString(obj.Object, "spec", "endpoint", "protocol")
	view.Endpoint.Port = nestedInt64OrZero(obj, "spec", "endpoint", "port")
	view.Endpoint.HealthPath, _, _ = unstructured.NestedString(obj.Object, "spec", "endpoint", "healthPath")
	view.Exposure.Visibility, _, _ = unstructured.NestedString(obj.Object, "spec", "exposure", "visibility")
	view.Exposure.Visibility = projectDevelopmentServiceVisibilityHTTP(view.Exposure.Visibility)
	view.RestartPolicy, _, _ = unstructured.NestedString(obj.Object, "spec", "restartPolicy")
	view.ConnectionRefs, _, _ = unstructured.NestedStringSlice(obj.Object, "spec", "connectionRefs")
	view.Host, _, _ = unstructured.NestedString(obj.Object, "status", "host")
	view.URL, _, _ = unstructured.NestedString(obj.Object, "status", "url")
	if view.URL == "" {
		view.URL, _, _ = unstructured.NestedString(obj.Object, "status", "previewURL")
	}
	view.Phase, _, _ = unstructured.NestedString(obj.Object, "status", "phase")
	view.Ready, _, _ = unstructured.NestedBool(obj.Object, "status", "ready")
	view.Process.Phase, _, _ = unstructured.NestedString(obj.Object, "status", "process", "phase")
	view.Process.Running, _, _ = unstructured.NestedBool(obj.Object, "status", "process", "running")
	view.Process.PortListening, _, _ = unstructured.NestedBool(obj.Object, "status", "process", "portListening")
	view.Process.Reachable, _, _ = unstructured.NestedBool(obj.Object, "status", "process", "reachable")
	view.Process.RestartCount = nestedInt64OrZero(obj, "status", "process", "restartCount")
	if _, found, _ := unstructured.NestedFieldNoCopy(obj.Object, "status", "process", "lastExitCode"); found {
		value := nestedInt64OrZero(obj, "status", "process", "lastExitCode")
		view.Process.LastExitCode = &value
	}
	view.Process.Message, _, _ = unstructured.NestedString(obj.Object, "status", "process", "message")
	if !view.Process.Running {
		view.Process.Running, _, _ = unstructured.NestedBool(obj.Object, "status", "running")
	}
	if !view.Process.PortListening {
		view.Process.PortListening, _, _ = unstructured.NestedBool(obj.Object, "status", "portListening")
	}
	if !view.Process.Reachable {
		view.Process.Reachable, _, _ = unstructured.NestedBool(obj.Object, "status", "reachable")
	}
	if view.Phase == "" {
		view.Phase = view.Process.Phase
	}
	if !view.Ready {
		for _, condition := range projectDevelopmentServiceConditions(obj) {
			if strings.EqualFold(condition.Type, "Ready") && strings.EqualFold(condition.Status, "True") {
				view.Ready = true
				break
			}
		}
	}
	view.Conditions = projectDevelopmentServiceConditions(obj)
	if raw, found, _ := unstructured.NestedString(obj.Object, "status", "observedAt"); found {
		if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			view.ObservedAt = &parsed
		}
	}
	view.Error, _, _ = unstructured.NestedString(obj.Object, "status", "message")
	if view.Error == "" {
		view.Error = view.Process.Message
	}
	return view
}

func projectDevelopmentServiceConditions(obj *unstructured.Unstructured) []projectDevelopmentServiceCondition {
	raw, found, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if !found {
		return nil
	}
	out := make([]projectDevelopmentServiceCondition, 0, len(raw))
	for _, item := range raw {
		condition, ok := item.(map[string]any)
		if !ok {
			continue
		}
		view := projectDevelopmentServiceCondition{}
		view.Type, _ = condition["type"].(string)
		view.Status, _ = condition["status"].(string)
		view.Reason, _ = condition["reason"].(string)
		view.Message, _ = condition["message"].(string)
		view.ObservedGeneration, _ = projectAssistantRunSandboxObservedGenerationValue(condition["observedGeneration"])
		if strings.TrimSpace(view.Type) == "" {
			continue
		}
		out = append(out, view)
		if len(out) >= 32 {
			break
		}
	}
	return out
}

func projectDevelopmentListenerViewsFromUnstructured(obj *unstructured.Unstructured) []projectDevelopmentListenerView {
	if obj == nil {
		return nil
	}
	paths := [][]string{{"status", "discoveredListeners"}, {"status", "listeners"}, {"status", "process", "listeners"}}
	seen := map[string]struct{}{}
	out := make([]projectDevelopmentListenerView, 0)
	for _, objectPath := range paths {
		raw, found, _ := unstructured.NestedSlice(obj.Object, objectPath...)
		if !found {
			continue
		}
		for _, item := range raw {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			port := int64(0)
			switch value := entry["port"].(type) {
			case int64:
				port = value
			case int:
				port = int64(value)
			case float64:
				port = int64(value)
			}
			if port < projectDevelopmentServicePortMin || port > projectDevelopmentServicePortMax {
				continue
			}
			view := projectDevelopmentListenerView{Port: port}
			view.Protocol, _ = entry["protocol"].(string)
			view.Address, _ = entry["address"].(string)
			view.Process, _ = entry["process"].(string)
			key := fmt.Sprintf("%d|%s|%s|%s", view.Port, view.Protocol, view.Address, view.Process)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, view)
			if len(out) >= 64 {
				break
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].Process < out[j].Process
	})
	return out
}

func (s *Server) listOwnedDevelopmentServices(ctx context.Context, c *asclient.Client, project *aiv1alpha1.Project) ([]*unstructured.Unstructured, error) {
	if c == nil {
		return nil, errors.New("project client is not configured")
	}
	list, err := c.Resource(developmentServicesResource, "").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	items := make([]*unstructured.Unstructured, 0, len(list.Items))
	for i := range list.Items {
		item := list.Items[i].DeepCopy()
		if projectDevelopmentServiceBelongsToProject(item, project) {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return projectDevelopmentServiceLogicalName(items[i]) < projectDevelopmentServiceLogicalName(items[j])
	})
	return items, nil
}

func projectDevelopmentPrimaryRef(project *aiv1alpha1.Project) string {
	if project == nil {
		return ""
	}
	for _, environment := range project.Spec.Environments {
		if strings.EqualFold(strings.TrimSpace(environment.Name), projectDevelopmentServiceEnvironment) && environment.Preview != nil {
			return strings.TrimSpace(environment.Preview.PrimaryServiceRef)
		}
	}
	return ""
}

func projectDevelopmentPrimaryService(project *aiv1alpha1.Project, items []*unstructured.Unstructured) string {
	if value := projectDevelopmentPrimaryRef(project); value != "" {
		for _, item := range items {
			if projectDevelopmentServiceLogicalName(item) == value {
				return value
			}
		}
	}
	for _, item := range items {
		view := projectDevelopmentServiceViewFromUnstructured(item)
		if view.Ready && strings.TrimSpace(view.URL) != "" {
			return view.Name
		}
	}
	if len(items) > 0 {
		return projectDevelopmentServiceLogicalName(items[0])
	}
	return ""
}

func (s *Server) projectDevelopmentServicesResponse(ctx context.Context, c *asclient.Client, project *aiv1alpha1.Project) (projectDevelopmentServicesResponse, error) {
	items, err := s.listOwnedDevelopmentServices(ctx, c, project)
	if err != nil {
		return projectDevelopmentServicesResponse{}, err
	}
	response := projectDevelopmentServicesResponse{Items: make([]projectDevelopmentServiceView, 0, len(items))}
	for _, item := range items {
		response.Items = append(response.Items, projectDevelopmentServiceViewFromUnstructured(item))
		response.Listeners = append(response.Listeners, projectDevelopmentListenerViewsFromUnstructured(item)...)
	}
	response.PrimaryServiceRef = projectDevelopmentPrimaryService(project, items)
	return response, nil
}

// projectDevelopmentServicePreview resolves one observed DevelopmentService
// into the preview URL contract used by the existing browser/authorization
// paths. found is false when the Infrastructure API is absent or the project
// has no DevelopmentService objects, allowing legacy Template previews to
// continue unchanged. Once a service object exists, readiness is never guessed
// from object creation: the service must report Ready and a URL, then the
// external preview edge must answer the same probe used by Template routes.
func (s *Server) projectDevelopmentServicePreview(ctx context.Context, c *asclient.Client, project *aiv1alpha1.Project, _ projectDevelopmentSyncTargetInfo, requested string) (projectSandboxPreviewURLResponse, bool, error) {
	items, err := s.listOwnedDevelopmentServices(ctx, c, project)
	if apierrors.IsNotFound(err) {
		return projectSandboxPreviewURLResponse{}, false, nil
	}
	if err != nil {
		// A catalog that predates DevelopmentService is a legacy Template
		// project. Preserve its preview rather than converting a provider API
		// rollout into a hard preview outage.
		return projectSandboxPreviewURLResponse{}, false, nil
	}
	if len(items) == 0 {
		return projectSandboxPreviewURLResponse{}, false, nil
	}
	name := strings.TrimSpace(requested)
	if name == "" {
		name = projectDevelopmentPrimaryService(project, items)
	}
	var selected *unstructured.Unstructured
	for _, item := range items {
		if projectDevelopmentServiceLogicalName(item) == name {
			selected = item
			break
		}
	}
	if selected == nil {
		return projectSandboxPreviewURLResponse{
			Ready:       false,
			ServiceName: name,
			Reason:      "development_service_not_found",
			Message:     fmt.Sprintf("Development service %q is not configured.", name),
		}, true, nil
	}
	view := projectDevelopmentServiceViewFromUnstructured(selected)
	response := projectSandboxPreviewURLResponse{
		Ready:          view.Ready,
		PreviewURL:     strings.TrimSpace(view.URL),
		ObservedAccess: firstNonEmpty(view.Exposure.Visibility, projectDevelopmentServicePrivate),
		ServiceName:    view.Name,
		ServicePhase:   view.Phase,
		ProcessRunning: view.Process.Running,
		PortListening:  view.Process.PortListening,
		Reachable:      view.Process.Reachable,
	}
	if response.PreviewURL == "" {
		response.Ready = false
		response.Reason = "development_service_url_not_ready"
		response.Message = firstNonEmpty(view.Error, "Development service is waiting for its route and process to become ready.")
		return response, true, nil
	}
	if !view.Ready {
		response.Ready = false
		response.Reason = "development_service_not_ready"
		response.Message = firstNonEmpty(view.Error, developmentServiceConditionMessage(view.Conditions), "Development service is not ready yet.")
		return response, true, nil
	}
	if !s.previewEdgeReady(ctx, response.PreviewURL) {
		response.Ready = false
		response.Reason = previewReasonEdgeProvisioning
		response.Message = previewEdgeProvisioningMessage
		return response, true, nil
	}
	return response, true, nil
}

func developmentServiceConditionMessage(conditions []projectDevelopmentServiceCondition) string {
	for _, condition := range conditions {
		if strings.EqualFold(strings.TrimSpace(condition.Status), "False") && strings.TrimSpace(condition.Message) != "" {
			return strings.TrimSpace(condition.Message)
		}
	}
	return ""
}

// resolveProjectSandboxRuntimeForService is the optional-service variant used
// by get_preview_url and browser inspection. Keeping the existing resolver as
// the empty-service wrapper preserves all legacy call sites and test seams.
func (s *Server) resolveProjectSandboxRuntimeForService(ctx context.Context, c *asclient.Client, id identity, project *aiv1alpha1.Project, service string) (projectSandboxPreviewURLResponse, bool) {
	if strings.TrimSpace(service) == "" {
		return s.resolveProjectSandboxRuntime(ctx, c, id, project)
	}
	if s == nil || c == nil || project == nil {
		return projectSandboxPreviewURLResponse{}, false
	}
	target, err := s.projectDevelopmentTarget(ctx, c, project, id)
	if err != nil {
		var validationErr *ValidationError
		if errors.As(err, &validationErr) {
			return projectSandboxPreviewURLResponse{}, false
		}
		return projectSandboxPreviewURLResponse{Ready: false, Reason: "runtime_unavailable", Message: "Runtime status is temporarily unavailable: " + err.Error()}, true
	}
	preview, found, previewErr := s.projectDevelopmentServicePreview(ctx, c, project, target, service)
	if previewErr != nil {
		return projectSandboxPreviewURLResponse{Ready: false, Reason: "runtime_unavailable", Message: "Runtime status is temporarily unavailable: " + previewErr.Error()}, true
	}
	if found {
		return preview, true
	}
	// A requested service cannot silently fall back to another service or a
	// Template URL. The caller asked for a specific origin, so report its
	// absence explicitly.
	return projectSandboxPreviewURLResponse{Ready: false, ServiceName: strings.TrimSpace(service), Reason: "development_service_not_found", Message: fmt.Sprintf("Development service %q is not configured.", strings.TrimSpace(service))}, true
}

func (s *Server) listProjectDevelopmentServices(w http.ResponseWriter, r *http.Request) {
	c, _, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	response, err := s.projectDevelopmentServicesResponse(r.Context(), c, project)
	if err != nil {
		if apierrors.IsNotFound(err) {
			writeJSON(w, http.StatusOK, projectDevelopmentServicesResponse{Items: []projectDevelopmentServiceView{}})
			return
		}
		writeProjectError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) getOwnedDevelopmentService(ctx context.Context, c *asclient.Client, project *aiv1alpha1.Project, name string) (*unstructured.Unstructured, error) {
	if err := projectDevelopmentServiceNameValid(name); err != nil {
		return nil, err
	}
	physicalName := projectDevelopmentServicePhysicalName(project, name)
	obj, err := c.Resource(developmentServicesResource, "").Get(ctx, physicalName, metav1.GetOptions{})
	if err == nil {
		if projectDevelopmentServiceBelongsToProject(obj, project) && projectDevelopmentServiceLogicalName(obj) == name {
			return obj, nil
		}
		return nil, apierrors.NewNotFound(developmentServicesResource.GVR.GroupResource(), name)
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}
	// Migrate/address older objects whose metadata.name was the logical name,
	// or whose physical name was generated by an earlier App Studio revision.
	items, listErr := s.listOwnedDevelopmentServices(ctx, c, project)
	if listErr != nil {
		return nil, listErr
	}
	for _, item := range items {
		if projectDevelopmentServiceLogicalName(item) == name {
			return item, nil
		}
	}
	return nil, apierrors.NewNotFound(developmentServicesResource.GVR.GroupResource(), name)
}

func (s *Server) resolveProjectDevelopmentSandboxRef(ctx context.Context, c *asclient.Client, id identity, project *aiv1alpha1.Project) (string, string, error) {
	if c == nil || project == nil {
		return "", "", errors.New("project client and project are required")
	}
	if strings.TrimSpace(string(project.UID)) == "" {
		return "", "", errors.New("project UID is required before configuring a development service")
	}
	name := projectAssistantRunSandboxName(projectWorkspaceScope(id, project), project, "")
	instance, err := c.Resource(runSandboxInstancesResource, "").Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return "", "", fmt.Errorf("project universal sandbox %q is not provisioned; start an assistant run before configuring a development service", name)
	}
	if err != nil {
		return "", "", fmt.Errorf("resolve project universal sandbox %q: %w", name, err)
	}
	if instance.GetUID() == "" {
		return "", "", fmt.Errorf("project universal sandbox %q has no UID", name)
	}
	if instance.GetAnnotations()[projectAssistantRunSandboxLabel] != "true" {
		return "", "", fmt.Errorf("project universal sandbox %q is not App Studio-owned", name)
	}
	if !ensureProjectDevelopmentSandboxOwner(instance, project) {
		return "", "", fmt.Errorf("project universal sandbox %q belongs to another Project", name)
	}
	return instance.GetName(), string(instance.GetUID()), nil
}

func ensureProjectDevelopmentSandboxOwner(instance *unstructured.Unstructured, project *aiv1alpha1.Project) bool {
	if instance == nil || project == nil || project.UID == "" {
		return false
	}
	for _, owner := range instance.GetOwnerReferences() {
		if owner.APIVersion == aiv1alpha1.SchemeGroupVersion.String() && owner.Kind == "Project" && owner.Name == project.Name && owner.UID == project.UID {
			return true
		}
	}
	return false
}

func (s *Server) upsertProjectDevelopmentService(w http.ResponseWriter, r *http.Request) {
	c, id, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(developmentServiceMuxVar(r, "service"))
	if err := projectDevelopmentServiceNameValid(name); err != nil {
		writeProjectError(w, newValidationError(err.Error()))
		return
	}
	var request projectDevelopmentServiceMutationRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		message := "invalid development service JSON: " + err.Error()
		// writeProjectError treats provider-schema "unknown field" failures as
		// transient workspace initialization. This is local request validation,
		// so avoid that phrase and return the correct 400 contract.
		if strings.Contains(err.Error(), "unknown field") {
			message = "development service JSON contains an unsupported field"
		}
		writeProjectError(w, newValidationError(message))
		return
	}
	resource := c.Resource(developmentServicesResource, "")
	existing, err := s.getOwnedDevelopmentService(r.Context(), c, project, name)
	if apierrors.IsNotFound(err) {
		existing = nil
	} else if err != nil {
		writeProjectError(w, err)
		return
	}
	if existing != nil {
		oldVisibility, _, _ := unstructured.NestedString(existing.Object, "spec", "exposure", "visibility")
		if strings.EqualFold(strings.TrimSpace(oldVisibility), projectDevelopmentServicePrivate) && request.Exposure != nil && strings.EqualFold(strings.TrimSpace(request.Exposure.Visibility), projectDevelopmentServicePublic) && !request.ConfirmPublic {
			writeProjectError(w, newValidationError("public development exposure requires confirmPublic=true"))
			return
		}
	}
	normalized, err := projectDevelopmentServiceNormalizeRequest(&request, existing)
	if err != nil {
		writeProjectError(w, newValidationError(err.Error()))
		return
	}
	if normalized.ComponentRef != "" && !projectDevelopmentServiceComponentExists(project, normalized.ComponentRef) {
		writeProjectError(w, newValidationError(fmt.Sprintf("componentRef %q is not declared on the Project", normalized.ComponentRef)))
		return
	}
	sandboxName, sandboxUID, err := s.resolveProjectDevelopmentSandboxRef(r.Context(), c, id, project)
	if err != nil {
		writeProjectError(w, newValidationError(err.Error()))
		return
	}
	obj, err := projectDevelopmentServiceObject(project, name, sandboxName, sandboxUID, normalized, existing)
	if err != nil {
		writeProjectError(w, newValidationError(err.Error()))
		return
	}
	var result *unstructured.Unstructured
	if existing == nil {
		result, err = resource.Create(r.Context(), obj, metav1.CreateOptions{})
	} else {
		result, err = resource.Update(r.Context(), obj, metav1.UpdateOptions{})
	}
	if err != nil {
		writeProjectError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, projectDevelopmentServiceMutationResponse{Service: projectDevelopmentServiceViewFromUnstructured(result)})
}

func (s *Server) deleteProjectDevelopmentService(w http.ResponseWriter, r *http.Request) {
	c, _, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(developmentServiceMuxVar(r, "service"))
	obj, err := s.getOwnedDevelopmentService(r.Context(), c, project, name)
	if apierrors.IsNotFound(err) {
		writeStatus(w, http.StatusNotFound, "NotFound", err.Error())
		return
	}
	if err != nil {
		writeProjectError(w, newValidationError(err.Error()))
		return
	}
	if err := c.Resource(developmentServicesResource, "").Delete(r.Context(), obj.GetName(), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		writeProjectError(w, err)
		return
	}
	if projectDevelopmentPrimaryRef(project) == name {
		_, _ = s.patchProjectPrimaryService(r.Context(), c, project, "")
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) restartProjectDevelopmentService(w http.ResponseWriter, r *http.Request) {
	c, _, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(developmentServiceMuxVar(r, "service"))
	obj, err := s.getOwnedDevelopmentService(r.Context(), c, project, name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			writeStatus(w, http.StatusNotFound, "NotFound", err.Error())
		} else {
			writeProjectError(w, newValidationError(err.Error()))
		}
		return
	}
	patch, _ := json.Marshal(map[string]any{"metadata": map[string]any{"annotations": map[string]string{projectDevelopmentServiceRestartAt: time.Now().UTC().Format(time.RFC3339Nano)}}})
	updated, err := c.Resource(developmentServicesResource, "").Patch(r.Context(), obj.GetName(), types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		writeProjectError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, projectDevelopmentServiceMutationResponse{Service: projectDevelopmentServiceViewFromUnstructured(updated)})
}

// logsProjectDevelopmentService is the App Studio-side capability boundary
// for Infrastructure's DevelopmentService logs subresource. The browser uses
// the project-local logical name; only this handler resolves the owned object
// and sends its deterministic physical CR name through the authenticated
// Infrastructure data plane. The provider keeps the runtime control token
// private and bounds the returned ring buffer before it reaches the portal.
func (s *Server) logsProjectDevelopmentService(w http.ResponseWriter, r *http.Request) {
	c, id, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(developmentServiceMuxVar(r, "service"))
	obj, err := s.getOwnedDevelopmentService(r.Context(), c, project, name)
	if apierrors.IsNotFound(err) {
		writeStatus(w, http.StatusNotFound, "NotFound", err.Error())
		return
	}
	if err != nil {
		writeProjectError(w, err)
		return
	}
	body, status, err := s.dataPlaneGet(
		r.Context(),
		id,
		dataPlaneRef{Resource: developmentServicesResource.GVR.Resource, Name: obj.GetName()},
		dataPlaneVerbDevelopmentServiceLogs,
		projectDevelopmentServiceLogsMaxBytes,
	)
	if err != nil {
		writeStatus(w, http.StatusBadGateway, "BadGateway", "development service logs unavailable: "+err.Error())
		return
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		detail := strings.TrimSpace(string(body))
		if detail == "" {
			detail = fmt.Sprintf("development service logs returned status %d", status)
		}
		writeStatus(w, http.StatusBadGateway, "BadGateway", detail)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func (s *Server) patchProjectPrimaryService(ctx context.Context, c *asclient.Client, project *aiv1alpha1.Project, service string) (*aiv1alpha1.Project, error) {
	if c == nil || project == nil {
		return nil, errors.New("project client is not configured")
	}
	service = strings.TrimSpace(service)
	for attempt := 0; attempt < 3; attempt++ {
		current, err := c.Projects().Get(ctx, project.Name, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("read Project before selecting primary development service: %w", err)
		}
		next := current.DeepCopy()
		found := false
		for index := range next.Spec.Environments {
			if !strings.EqualFold(strings.TrimSpace(next.Spec.Environments[index].Name), projectDevelopmentServiceEnvironment) {
				continue
			}
			found = true
			if service == "" {
				next.Spec.Environments[index].Preview = nil
			} else {
				next.Spec.Environments[index].Preview = &aiv1alpha1.ProjectEnvironmentPreviewSpec{PrimaryServiceRef: service}
			}
			break
		}
		if !found && service != "" {
			next.Spec.Environments = append(next.Spec.Environments, aiv1alpha1.ProjectEnvironmentSpec{
				Name:    projectDevelopmentServiceEnvironment,
				Mode:    aiv1alpha1.ProjectEnvironmentModeLive,
				Preview: &aiv1alpha1.ProjectEnvironmentPreviewSpec{PrimaryServiceRef: service},
			})
		}
		updated, err := c.Projects().Update(ctx, next, metav1.UpdateOptions{})
		if err == nil {
			return updated, nil
		}
		if !apierrors.IsConflict(err) || attempt == 2 {
			return nil, fmt.Errorf("persist primary development service: %w", err)
		}
	}
	return nil, errors.New("persist primary development service: update conflicted")
}

func (s *Server) setProjectDevelopmentPrimaryService(w http.ResponseWriter, r *http.Request) {
	c, _, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	var request projectDevelopmentServicePrimaryRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeProjectError(w, newValidationError("invalid primary development service JSON: "+err.Error()))
		return
	}
	service := strings.TrimSpace(request.Service)
	if service != "" {
		if _, err := s.getOwnedDevelopmentService(r.Context(), c, project, service); err != nil {
			if apierrors.IsNotFound(err) {
				writeStatus(w, http.StatusNotFound, "NotFound", err.Error())
			} else {
				writeProjectError(w, newValidationError(err.Error()))
			}
			return
		}
	}
	updated, err := s.patchProjectPrimaryService(r.Context(), c, project, service)
	if err != nil {
		writeProjectError(w, err)
		return
	}
	if updated == nil {
		updated = project
	}
	// The project object is deliberately not returned here. The caller already
	// has the authoritative Project view and only needs the persisted selector;
	// returning a partially hydrated view would invite the portal to replace
	// newer repository/status data with an older patch response.
	_ = updated
	writeJSON(w, http.StatusOK, map[string]any{"primaryServiceRef": service})
}

func decodeProjectDevelopmentServiceMutation(args map[string]any) (projectDevelopmentServiceMutationRequest, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return projectDevelopmentServiceMutationRequest{}, err
	}
	var request projectDevelopmentServiceMutationRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return projectDevelopmentServiceMutationRequest{}, err
	}
	return request, nil
}

func (s *Server) assistantListProjectDevelopmentServices(ctx context.Context, req projectAssistantToolCallRequest) (string, error) {
	if req.Project == nil {
		return "", errors.New("no project on this run")
	}
	c, err := s.clientFor(req.Identity)
	if err != nil {
		return "", err
	}
	response, err := s.projectDevelopmentServicesResponse(ctx, c, req.Project)
	if err != nil {
		return "", err
	}
	return projectAssistantToolJSONResult(response, nil)
}

func (s *Server) assistantUpsertProjectDevelopmentService(ctx context.Context, req projectAssistantToolCallRequest) (string, error) {
	if req.Project == nil {
		return "", errors.New("no project on this run")
	}
	name := projectToolString(req.Arguments["service"])
	if err := projectDevelopmentServiceNameValid(name); err != nil {
		return "", err
	}
	request, err := decodeProjectDevelopmentServiceMutation(req.Arguments)
	if err != nil {
		return "", fmt.Errorf("decode development service request: %w", err)
	}
	c, err := s.clientFor(req.Identity)
	if err != nil {
		return "", err
	}
	existing, err := s.getOwnedDevelopmentService(ctx, c, req.Project, name)
	if apierrors.IsNotFound(err) {
		existing = nil
	} else if err != nil {
		return "", err
	}
	normalized, err := projectDevelopmentServiceNormalizeRequest(&request, existing)
	if err != nil {
		return "", err
	}
	if normalized.ComponentRef != "" && !projectDevelopmentServiceComponentExists(req.Project, normalized.ComponentRef) {
		return "", fmt.Errorf("componentRef %q is not declared on the Project", normalized.ComponentRef)
	}
	sandboxName, sandboxUID, err := s.resolveProjectDevelopmentSandboxRef(ctx, c, req.Identity, req.Project)
	if err != nil {
		return "", err
	}
	obj, err := projectDevelopmentServiceObject(req.Project, name, sandboxName, sandboxUID, normalized, existing)
	if err != nil {
		return "", err
	}
	resource := c.Resource(developmentServicesResource, "")
	var result *unstructured.Unstructured
	if existing == nil {
		result, err = resource.Create(ctx, obj, metav1.CreateOptions{})
	} else {
		result, err = resource.Update(ctx, obj, metav1.UpdateOptions{})
	}
	if err != nil {
		return "", err
	}
	return projectAssistantToolJSONResult(projectDevelopmentServiceMutationResponse{Service: projectDevelopmentServiceViewFromUnstructured(result)}, nil)
}

func (s *Server) assistantDeleteProjectDevelopmentService(ctx context.Context, req projectAssistantToolCallRequest) (string, error) {
	if req.Project == nil {
		return "", errors.New("no project on this run")
	}
	name := projectToolString(req.Arguments["service"])
	c, err := s.clientFor(req.Identity)
	if err != nil {
		return "", err
	}
	obj, err := s.getOwnedDevelopmentService(ctx, c, req.Project, name)
	if err != nil {
		return "", err
	}
	if err := c.Resource(developmentServicesResource, "").Delete(ctx, obj.GetName(), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return "", err
	}
	if projectDevelopmentPrimaryRef(req.Project) == name {
		if _, err := s.patchProjectPrimaryService(ctx, c, req.Project, ""); err != nil {
			return "", err
		}
	}
	return projectAssistantToolJSONResult(map[string]any{"deleted": true, "service": name}, nil)
}

func (s *Server) assistantRestartProjectDevelopmentService(ctx context.Context, req projectAssistantToolCallRequest) (string, error) {
	if req.Project == nil {
		return "", errors.New("no project on this run")
	}
	name := projectToolString(req.Arguments["service"])
	c, err := s.clientFor(req.Identity)
	if err != nil {
		return "", err
	}
	obj, err := s.getOwnedDevelopmentService(ctx, c, req.Project, name)
	if err != nil {
		return "", err
	}
	patch, err := json.Marshal(map[string]any{"metadata": map[string]any{"annotations": map[string]string{projectDevelopmentServiceRestartAt: time.Now().UTC().Format(time.RFC3339Nano)}}})
	if err != nil {
		return "", err
	}
	updated, err := c.Resource(developmentServicesResource, "").Patch(ctx, obj.GetName(), types.MergePatchType, patch, metav1.PatchOptions{})
	if err != nil {
		return "", err
	}
	return projectAssistantToolJSONResult(projectDevelopmentServiceMutationResponse{Service: projectDevelopmentServiceViewFromUnstructured(updated)}, nil)
}

func (s *Server) assistantSetProjectDevelopmentPrimaryService(ctx context.Context, req projectAssistantToolCallRequest) (string, error) {
	if req.Project == nil {
		return "", errors.New("no project on this run")
	}
	service := projectToolString(req.Arguments["service"])
	c, err := s.clientFor(req.Identity)
	if err != nil {
		return "", err
	}
	if service != "" {
		if _, err := s.getOwnedDevelopmentService(ctx, c, req.Project, service); err != nil {
			return "", err
		}
	}
	if _, err := s.patchProjectPrimaryService(ctx, c, req.Project, service); err != nil {
		return "", err
	}
	return projectAssistantToolJSONResult(map[string]any{"primaryServiceRef": service}, nil)
}

func (s *Server) projectDevelopmentListenerViews(ctx context.Context, id identity, c *asclient.Client, project *aiv1alpha1.Project) ([]projectDevelopmentListenerView, error) {
	target, err := s.projectDevelopmentTarget(ctx, c, project, id)
	if err != nil {
		return nil, err
	}
	listeners := make([]projectDevelopmentListenerView, 0)
	seen := map[string]struct{}{}
	for _, component := range target.sortedComponents() {
		// Listener discovery is a distinct observation-only data-plane action.
		// Process status may carry a historical listener field, but using it here
		// silently loses the universal sandbox's authoritative /listeners
		// response and makes a broken observer look like an empty result.
		body, status, getErr := s.dataPlaneGet(ctx, id, target.dataPlaneRefFor(component), dataPlaneVerbListeners, 16<<10)
		if getErr != nil {
			return nil, fmt.Errorf("discover listeners for component %q: %w", component, getErr)
		}
		if status < 200 || status >= 300 {
			return nil, fmt.Errorf("discover listeners for component %q returned status %d: %s", component, status, strings.TrimSpace(string(body)))
		}
		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			return nil, fmt.Errorf("decode listeners for component %q: %w", component, err)
		}
		obj := &unstructured.Unstructured{Object: payload}
		for _, listener := range projectDevelopmentListenerViewsFromUnstructured(obj) {
			if listener.Process == "" {
				listener.Process = component
			}
			key := fmt.Sprintf("%d|%s|%s|%s", listener.Port, listener.Protocol, listener.Address, listener.Process)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			listeners = append(listeners, listener)
		}
	}
	sort.Slice(listeners, func(i, j int) bool { return listeners[i].Port < listeners[j].Port })
	return listeners, nil
}

func (s *Server) assistantListProjectDevelopmentListeners(ctx context.Context, req projectAssistantToolCallRequest) (string, error) {
	if req.Project == nil {
		return "", errors.New("no project on this run")
	}
	c, err := s.clientFor(req.Identity)
	if err != nil {
		return "", err
	}
	listeners, err := s.projectDevelopmentListenerViews(ctx, req.Identity, c, req.Project)
	if err != nil {
		return "", err
	}
	return projectAssistantToolJSONResult(map[string]any{"listeners": listeners, "suggestionsOnly": true, "note": "Detected listeners are suggestions only; configure a DevelopmentService explicitly before exposing one."}, nil)
}

func developmentServiceMuxVar(r *http.Request, key string) string {
	if r == nil {
		return ""
	}
	return mux.Vars(r)[key]
}

func (s *Server) listProjectDevelopmentListeners(w http.ResponseWriter, r *http.Request) {
	c, id, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	listeners, err := s.projectDevelopmentListenerViews(r.Context(), id, c, project)
	if err != nil {
		writeStatus(w, http.StatusConflict, "ListenerObservationUnavailable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"listeners": listeners})
}
