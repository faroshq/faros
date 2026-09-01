/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package api

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

	"github.com/gorilla/mux"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	asclient "github.com/faroshq/provider-app-studio/client"
)

const (
	projectDependencyEnvironmentDefault = "development"
	projectDependencyMaxCount           = 32
)

type projectDependencyInterfaceView struct {
	Name     string                                    `json:"name"`
	Type     string                                    `json:"type"`
	Keys     []string                                  `json:"keys,omitempty"`
	Mappings []aiv1alpha1.ProjectConnectionMappingSpec `json:"mappings,omitempty"`
}

type projectDependencyTemplateView struct {
	Name                  string                           `json:"name"`
	DisplayName           string                           `json:"displayName,omitempty"`
	Description           string                           `json:"description,omitempty"`
	Category              string                           `json:"category,omitempty"`
	Schema                map[string]any                   `json:"schema,omitempty"`
	SampleValues          map[string]any                   `json:"sampleValues,omitempty"`
	DefaultDeletionPolicy string                           `json:"defaultDeletionPolicy,omitempty"`
	Provides              []projectDependencyInterfaceView `json:"provides"`
}

type projectDependencyCatalogResponse struct {
	Templates        []projectDependencyTemplateView  `json:"templates"`
	TargetInterfaces []projectDependencyInterfaceView `json:"targetInterfaces"`
}

type projectDependencyMutationRequest struct {
	Environment     string                                        `json:"environment,omitempty"`
	Template        string                                        `json:"template"`
	Values          map[string]any                                `json:"values"`
	SourceInterface string                                        `json:"sourceInterface"`
	TargetRef       aiv1alpha1.ProjectConnectionEndpointReference `json:"targetRef"`
	TargetInterface string                                        `json:"targetInterface"`
	Mappings        []aiv1alpha1.ProjectConnectionMappingSpec     `json:"mappings,omitempty"`
}

type projectDependencyView struct {
	Name            string                                         `json:"name"`
	Environment     string                                         `json:"environment"`
	Template        string                                         `json:"template,omitempty"`
	Values          map[string]any                                 `json:"values,omitempty"`
	SourceRef       aiv1alpha1.ProjectConnectionEndpointReference  `json:"sourceRef"`
	TargetRef       aiv1alpha1.ProjectConnectionEndpointReference  `json:"targetRef"`
	SourceInterface string                                         `json:"sourceInterface"`
	TargetInterface string                                         `json:"targetInterface"`
	Mappings        []aiv1alpha1.ProjectConnectionMappingSpec      `json:"mappings,omitempty"`
	DeletionPolicy  aiv1alpha1.ProjectBindingDeletionPolicy        `json:"deletionPolicy"`
	Status          *aiv1alpha1.ProjectEnvironmentConnectionStatus `json:"status,omitempty"`
}

type projectDependenciesResponse struct {
	Items []projectDependencyView `json:"items"`
}

func projectDependencyNameValid(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("dependency name is required")
	}
	if len(name) > 63 {
		return errors.New("dependency name must be at most 63 characters")
	}
	if errs := validation.IsDNS1123Label(name); len(errs) > 0 {
		return fmt.Errorf("dependency name %q is invalid: %s", name, strings.Join(errs, "; "))
	}
	return nil
}

func projectDependencyBindingName(project, environment, name string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{project, environment, name}, "\x00")))
	base := strings.TrimRight(name, "-")
	if len(base) > 42 {
		base = strings.TrimRight(base[:42], "-")
	}
	return "dep-" + base + "-" + hex.EncodeToString(sum[:4])
}

func projectDependencyInstanceName(project *aiv1alpha1.Project, environment, name, template string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{project.Name, string(project.UID), environment, name, template}, "\x00")))
	base := strings.TrimRight(strings.ToLower(project.Name+"-"+name), "-")
	if len(base) > 42 {
		base = strings.TrimRight(base[:42], "-")
	}
	return "depinst-" + base + "-" + hex.EncodeToString(sum[:5])
}

func projectDependencyRuntimeName(project *aiv1alpha1.Project, name string) string {
	base := strings.ToLower(project.Name + "-" + name)
	if len(base) > 49 {
		sum := sha256.Sum256([]byte(base))
		base = strings.TrimRight(base[:40], "-") + "-" + hex.EncodeToString(sum[:4])
	}
	return strings.Trim(base, "-")
}

func projectDependencyProvidedInterfaces(obj *unstructured.Unstructured) []projectDependencyInterfaceView {
	raw, _, _ := unstructured.NestedSlice(obj.Object, "spec", "connections", "provides")
	out := make([]projectDependencyInterfaceView, 0, len(raw))
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := entry["name"].(string)
		interfaceType, _ := entry["type"].(string)
		keys := projectSchemaStrings(entry["keys"])
		if strings.TrimSpace(name) == "" || strings.TrimSpace(interfaceType) == "" || len(keys) == 0 {
			continue
		}
		sort.Strings(keys)
		out = append(out, projectDependencyInterfaceView{Name: name, Type: interfaceType, Keys: keys})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func projectDependencyConsumedInterfaces(obj *unstructured.Unstructured) []projectDependencyInterfaceView {
	raw, _, _ := unstructured.NestedSlice(obj.Object, "spec", "connections", "consumes")
	out := make([]projectDependencyInterfaceView, 0, len(raw))
	for _, item := range raw {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := entry["name"].(string)
		interfaceType, _ := entry["type"].(string)
		mappingRaw, _ := entry["mappings"].([]any)
		view := projectDependencyInterfaceView{Name: name, Type: interfaceType}
		for _, mappingItem := range mappingRaw {
			mapping, ok := mappingItem.(map[string]any)
			if !ok {
				continue
			}
			sourceKey, _ := mapping["sourceKey"].(string)
			targetKey, _ := mapping["targetKey"].(string)
			if sourceKey != "" && targetKey != "" {
				view.Mappings = append(view.Mappings, aiv1alpha1.ProjectConnectionMappingSpec{SourceKey: sourceKey, TargetKey: targetKey})
			}
		}
		if strings.TrimSpace(name) != "" && strings.TrimSpace(interfaceType) != "" && len(view.Mappings) > 0 {
			out = append(out, view)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func projectDependencySafeSample(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for key, nested := range typed {
			if projectDependencyCredentialKey(key) {
				continue
			}
			out[key] = projectDependencySafeSample(nested)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = projectDependencySafeSample(typed[i])
		}
		return out
	default:
		return value
	}
}

func projectDependencyCredentialKey(key string) bool {
	normalized := strings.NewReplacer("-", "", "_", "", ".", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(key)))
	for _, marker := range []string{"password", "passwd", "secret", "token", "credential", "privatekey", "accesskey", "apikey", "clientsecret"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func projectDependencyRejectCredentialValues(value any, path string) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if projectDependencyCredentialKey(key) {
				return fmt.Errorf("%s.%s is credential material and cannot be stored in Project dependency values", path, key)
			}
			if err := projectDependencyRejectCredentialValues(nested, path+"."+key); err != nil {
				return err
			}
		}
	case []any:
		for i, nested := range typed {
			if err := projectDependencyRejectCredentialValues(nested, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

// projectDependencySafeSchema keeps public configuration metadata while
// removing credential-shaped inputs before the schema reaches a browser or
// model. Rejecting them only at mutation time would still render a password or
// token field in the generic form and imply that App Studio stores it.
func projectDependencySafeSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	out := make(map[string]any, len(schema))
	for key, value := range schema {
		switch key {
		case "example", "examples":
			continue
		case "properties":
			properties, _ := value.(map[string]any)
			safe := map[string]any{}
			for name, raw := range properties {
				if projectDependencyCredentialKey(name) {
					continue
				}
				if field, ok := raw.(map[string]any); ok {
					safe[name] = projectDependencySafeSchema(field)
				}
			}
			out[key] = safe
		case "required":
			var required []any
			for _, raw := range projectSchemaStrings(value) {
				if !projectDependencyCredentialKey(raw) {
					required = append(required, raw)
				}
			}
			out[key] = required
		case "items", "additionalProperties":
			if nested, ok := value.(map[string]any); ok {
				out[key] = projectDependencySafeSchema(nested)
			} else {
				out[key] = value
			}
		default:
			out[key] = value
		}
	}
	return out
}

func projectDependencyTemplateViews(items []unstructured.Unstructured) []projectDependencyTemplateView {
	out := make([]projectDependencyTemplateView, 0, len(items))
	for i := range items {
		obj := &items[i]
		provides := projectDependencyProvidedInterfaces(obj)
		if len(provides) == 0 {
			continue
		}
		view := projectDependencyTemplateView{Name: obj.GetName(), Provides: provides}
		view.DisplayName, _, _ = unstructured.NestedString(obj.Object, "spec", "displayName")
		view.Description, _, _ = unstructured.NestedString(obj.Object, "spec", "description")
		view.Category, _, _ = unstructured.NestedString(obj.Object, "spec", "category")
		schema, _, _ := unstructured.NestedMap(obj.Object, "spec", "schema")
		view.Schema = projectDependencySafeSchema(schema)
		sample, _, _ := unstructured.NestedMap(obj.Object, "spec", "sampleValues")
		if safe, ok := projectDependencySafeSample(sample).(map[string]any); ok {
			view.SampleValues = safe
		}
		view.DefaultDeletionPolicy, _, _ = unstructured.NestedString(obj.Object, "spec", "lifecycle", "defaultDeletionPolicy")
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func projectDependencyTargetTemplateName(project *aiv1alpha1.Project, envName string, ref aiv1alpha1.ProjectConnectionEndpointReference) string {
	if project == nil {
		return ""
	}
	for _, env := range project.Spec.Environments {
		if env.Name != envName {
			continue
		}
		if ref.Kind == aiv1alpha1.ProjectConnectionReferenceBinding {
			for _, binding := range env.Bindings {
				if binding.Name == ref.Name && binding.TemplateRef != nil {
					return strings.TrimSpace(binding.TemplateRef.Name)
				}
			}
			return ""
		}
		for _, binding := range env.Bindings {
			if env.Name == projectDevelopmentEnvironmentName && binding.Name == projectDevelopmentBindingName && binding.Provider == projectDevelopmentProviderAppStudio && binding.TemplateRef != nil {
				return strings.TrimSpace(binding.TemplateRef.Name)
			}
		}
	}
	if ref.Kind == aiv1alpha1.ProjectConnectionReferenceDevelopmentService && project.Spec.Template != nil {
		return strings.TrimSpace(project.Spec.Template.Name)
	}
	return ""
}

func projectDependencyTargetInterfaces(ctx context.Context, c *asclient.Client, project *aiv1alpha1.Project, environment string) ([]projectDependencyInterfaceView, error) {
	name := projectDependencyTargetTemplateName(project, environment, aiv1alpha1.ProjectConnectionEndpointReference{Kind: aiv1alpha1.ProjectConnectionReferenceDevelopmentService})
	if name == "" {
		return []projectDependencyInterfaceView{}, nil
	}
	obj, err := c.Resource(templateResource, "").Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return projectDependencyConsumedInterfaces(obj), nil
}

func (s *Server) getProjectDependencyCatalog(w http.ResponseWriter, r *http.Request) {
	c, _, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	list, err := c.Resource(templateResource, "").List(r.Context(), metav1.ListOptions{})
	if err != nil {
		writeProjectError(w, err)
		return
	}
	targets, err := projectDependencyTargetInterfaces(r.Context(), c, project, projectDependencyEnvironmentDefault)
	if err != nil && !apierrors.IsNotFound(err) {
		writeProjectError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, projectDependencyCatalogResponse{Templates: projectDependencyTemplateViews(list.Items), TargetInterfaces: targets})
}

func projectDependencyFindBinding(env *aiv1alpha1.ProjectEnvironmentSpec, name string) *aiv1alpha1.ProjectProviderBindingSpec {
	if env == nil {
		return nil
	}
	for i := range env.Bindings {
		if env.Bindings[i].Name == name {
			return &env.Bindings[i]
		}
	}
	return nil
}

func projectDependenciesView(project *aiv1alpha1.Project) projectDependenciesResponse {
	response := projectDependenciesResponse{Items: []projectDependencyView{}}
	if project == nil {
		return response
	}
	statusByEnv := map[string]map[string]aiv1alpha1.ProjectEnvironmentConnectionStatus{}
	for _, env := range project.Status.Environments {
		statusByEnv[env.Name] = map[string]aiv1alpha1.ProjectEnvironmentConnectionStatus{}
		for _, status := range env.Connections {
			statusByEnv[env.Name][status.Name] = status
		}
	}
	for envIndex := range project.Spec.Environments {
		env := &project.Spec.Environments[envIndex]
		for _, connection := range env.Connections {
			view := projectDependencyView{Name: connection.Name, Environment: env.Name, SourceRef: connection.SourceRef, TargetRef: connection.TargetRef, SourceInterface: connection.SourceInterface, TargetInterface: connection.TargetInterface, Mappings: connection.Mappings, DeletionPolicy: aiv1alpha1.ProjectBindingDeletionPolicyRetain}
			if binding := projectDependencyFindBinding(env, connection.SourceRef.Name); binding != nil {
				if binding.TemplateRef != nil {
					view.Template = binding.TemplateRef.Name
				}
				_ = json.Unmarshal(binding.Values.Raw, &view.Values)
				if binding.Lifecycle != nil && binding.Lifecycle.DeletionPolicy != "" {
					view.DeletionPolicy = binding.Lifecycle.DeletionPolicy
				}
			}
			if status, ok := statusByEnv[env.Name][connection.Name]; ok {
				copy := status
				view.Status = &copy
			}
			response.Items = append(response.Items, view)
		}
	}
	sort.Slice(response.Items, func(i, j int) bool {
		if response.Items[i].Environment == response.Items[j].Environment {
			return response.Items[i].Name < response.Items[j].Name
		}
		return response.Items[i].Environment < response.Items[j].Environment
	})
	return response
}

func projectDependencyViewByName(project *aiv1alpha1.Project, environment, name string) (projectDependencyView, bool) {
	for _, item := range projectDependenciesView(project).Items {
		if item.Environment == environment && item.Name == name {
			return item, true
		}
	}
	return projectDependencyView{}, false
}

func (s *Server) listProjectDependencies(w http.ResponseWriter, r *http.Request) {
	_, _, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, projectDependenciesView(project))
}

func projectDependencyValidateMappings(provided projectDependencyInterfaceView, consumed projectDependencyInterfaceView, mappings []aiv1alpha1.ProjectConnectionMappingSpec) error {
	if len(mappings) > 32 {
		return errors.New("mappings accepts at most 32 entries")
	}
	allowedSources := map[string]struct{}{}
	for _, key := range provided.Keys {
		allowedSources[key] = struct{}{}
	}
	allowedPairs := map[string]struct{}{}
	for _, mapping := range consumed.Mappings {
		allowedPairs[mapping.SourceKey+"\x00"+mapping.TargetKey] = struct{}{}
	}
	seenTargets := map[string]struct{}{}
	for _, mapping := range mappings {
		if _, ok := allowedSources[mapping.SourceKey]; !ok {
			return fmt.Errorf("source key %q is not exported by interface %q", mapping.SourceKey, provided.Name)
		}
		if _, ok := allowedPairs[mapping.SourceKey+"\x00"+mapping.TargetKey]; !ok {
			return fmt.Errorf("mapping %q to %q is not allowed by target interface %q", mapping.SourceKey, mapping.TargetKey, consumed.Name)
		}
		if _, duplicate := seenTargets[mapping.TargetKey]; duplicate {
			return fmt.Errorf("target key %q is duplicated", mapping.TargetKey)
		}
		seenTargets[mapping.TargetKey] = struct{}{}
	}
	return nil
}

func projectDependencyTemplateInterface(obj *unstructured.Unstructured, name string, provided bool) (projectDependencyInterfaceView, bool) {
	items := projectDependencyConsumedInterfaces(obj)
	if provided {
		items = projectDependencyProvidedInterfaces(obj)
	}
	for _, item := range items {
		if item.Name == name {
			return item, true
		}
	}
	return projectDependencyInterfaceView{}, false
}

func projectDependencyValuesValid(schema map[string]any, values map[string]any) error {
	if err := projectDependencyRejectCredentialValues(values, "dependency values"); err != nil {
		return err
	}
	properties, _ := schema["properties"].(map[string]any)
	for key := range values {
		if _, ok := properties[key]; !ok {
			return fmt.Errorf("dependency values.%s is not supported by the selected Template", key)
		}
	}
	return validateProjectProductionValue(schema, values, "dependency values")
}

func (s *Server) normalizeProjectDependency(ctx context.Context, c *asclient.Client, project *aiv1alpha1.Project, name string, request projectDependencyMutationRequest) (aiv1alpha1.ProjectProviderBindingSpec, aiv1alpha1.ProjectEnvironmentConnectionSpec, error) {
	if err := projectDependencyNameValid(name); err != nil {
		return aiv1alpha1.ProjectProviderBindingSpec{}, aiv1alpha1.ProjectEnvironmentConnectionSpec{}, err
	}
	environment := strings.TrimSpace(request.Environment)
	if environment == "" {
		environment = projectDependencyEnvironmentDefault
	}
	if err := projectDependencyNameValid(environment); err != nil {
		return aiv1alpha1.ProjectProviderBindingSpec{}, aiv1alpha1.ProjectEnvironmentConnectionSpec{}, fmt.Errorf("environment: %w", err)
	}
	templateName := strings.TrimSpace(request.Template)
	if templateName == "" {
		return aiv1alpha1.ProjectProviderBindingSpec{}, aiv1alpha1.ProjectEnvironmentConnectionSpec{}, errors.New("template is required")
	}
	sourceTemplate, err := c.Resource(templateResource, "").Get(ctx, templateName, metav1.GetOptions{})
	if err != nil {
		return aiv1alpha1.ProjectProviderBindingSpec{}, aiv1alpha1.ProjectEnvironmentConnectionSpec{}, err
	}
	sourceInterface, ok := projectDependencyTemplateInterface(sourceTemplate, strings.TrimSpace(request.SourceInterface), true)
	if !ok {
		return aiv1alpha1.ProjectProviderBindingSpec{}, aiv1alpha1.ProjectEnvironmentConnectionSpec{}, fmt.Errorf("Template %q does not provide interface %q", templateName, request.SourceInterface)
	}
	request.TargetRef.Name = strings.TrimSpace(request.TargetRef.Name)
	if err := projectDependencyNameValid(request.TargetRef.Name); err != nil {
		return aiv1alpha1.ProjectProviderBindingSpec{}, aiv1alpha1.ProjectEnvironmentConnectionSpec{}, fmt.Errorf("targetRef.name: %w", err)
	}
	if request.TargetRef.Kind != aiv1alpha1.ProjectConnectionReferenceDevelopmentService && request.TargetRef.Kind != aiv1alpha1.ProjectConnectionReferenceBinding {
		return aiv1alpha1.ProjectProviderBindingSpec{}, aiv1alpha1.ProjectEnvironmentConnectionSpec{}, errors.New("targetRef.kind must be developmentService or binding")
	}
	if request.TargetRef.Kind == aiv1alpha1.ProjectConnectionReferenceDevelopmentService {
		if environment != projectDependencyEnvironmentDefault {
			return aiv1alpha1.ProjectProviderBindingSpec{}, aiv1alpha1.ProjectEnvironmentConnectionSpec{}, errors.New("DevelopmentService targets are supported only in the development environment")
		}
		services, listErr := s.listOwnedDevelopmentServices(ctx, c, project)
		if listErr != nil {
			return aiv1alpha1.ProjectProviderBindingSpec{}, aiv1alpha1.ProjectEnvironmentConnectionSpec{}, listErr
		}
		found := false
		for _, service := range services {
			found = found || projectDevelopmentServiceLogicalName(service) == request.TargetRef.Name
		}
		if !found {
			return aiv1alpha1.ProjectProviderBindingSpec{}, aiv1alpha1.ProjectEnvironmentConnectionSpec{}, fmt.Errorf("DevelopmentService %q does not exist", request.TargetRef.Name)
		}
	}
	targetTemplateName := projectDependencyTargetTemplateName(project, environment, request.TargetRef)
	if targetTemplateName == "" {
		return aiv1alpha1.ProjectProviderBindingSpec{}, aiv1alpha1.ProjectEnvironmentConnectionSpec{}, errors.New("target has no Infrastructure Template connection contract")
	}
	targetTemplate, err := c.Resource(templateResource, "").Get(ctx, targetTemplateName, metav1.GetOptions{})
	if err != nil {
		return aiv1alpha1.ProjectProviderBindingSpec{}, aiv1alpha1.ProjectEnvironmentConnectionSpec{}, err
	}
	targetInterface, ok := projectDependencyTemplateInterface(targetTemplate, strings.TrimSpace(request.TargetInterface), false)
	if !ok {
		return aiv1alpha1.ProjectProviderBindingSpec{}, aiv1alpha1.ProjectEnvironmentConnectionSpec{}, fmt.Errorf("target Template %q does not consume interface %q", targetTemplateName, request.TargetInterface)
	}
	if sourceInterface.Type != targetInterface.Type {
		return aiv1alpha1.ProjectProviderBindingSpec{}, aiv1alpha1.ProjectEnvironmentConnectionSpec{}, fmt.Errorf("source interface type %q is incompatible with target type %q", sourceInterface.Type, targetInterface.Type)
	}
	if err := projectDependencyValidateMappings(sourceInterface, targetInterface, request.Mappings); err != nil {
		return aiv1alpha1.ProjectProviderBindingSpec{}, aiv1alpha1.ProjectEnvironmentConnectionSpec{}, err
	}
	values := request.Values
	if values == nil {
		values = map[string]any{}
	}
	schema, _, _ := unstructured.NestedMap(sourceTemplate.Object, "spec", "schema")
	properties, _ := schema["properties"].(map[string]any)
	if _, declaresName := properties["name"]; declaresName {
		if _, set := values["name"]; !set {
			values["name"] = projectDependencyRuntimeName(project, name)
		}
	}
	if err := projectDependencyValuesValid(schema, values); err != nil {
		return aiv1alpha1.ProjectProviderBindingSpec{}, aiv1alpha1.ProjectEnvironmentConnectionSpec{}, err
	}
	rawValues, err := json.Marshal(values)
	if err != nil {
		return aiv1alpha1.ProjectProviderBindingSpec{}, aiv1alpha1.ProjectEnvironmentConnectionSpec{}, err
	}
	bindingName := projectDependencyBindingName(project.Name, environment, name)
	binding := aiv1alpha1.ProjectProviderBindingSpec{
		Name: bindingName, Provider: "infrastructure", Kind: aiv1alpha1.ProjectBindingKindProviderResource,
		TemplateRef: &aiv1alpha1.ProjectTemplateSpec{Name: templateName},
		ResourceRef: &aiv1alpha1.ProjectProviderResourceReference{Name: projectDependencyInstanceName(project, environment, name, templateName), APIVersion: "infrastructure.faros.sh/v1alpha1", Kind: "Instance", Resource: "instances"},
		Values:      runtime.RawExtension{Raw: rawValues},
		Lifecycle:   &aiv1alpha1.ProjectBindingLifecycleSpec{DeletionPolicy: aiv1alpha1.ProjectBindingDeletionPolicyRetain},
	}
	connection := aiv1alpha1.ProjectEnvironmentConnectionSpec{
		Name: name, SourceRef: aiv1alpha1.ProjectConnectionEndpointReference{Kind: aiv1alpha1.ProjectConnectionReferenceBinding, Name: bindingName}, TargetRef: request.TargetRef,
		SourceInterface: sourceInterface.Name, TargetInterface: targetInterface.Name, Mappings: request.Mappings,
	}
	return binding, connection, nil
}

func findProjectEnvironment(project *aiv1alpha1.Project, name string) *aiv1alpha1.ProjectEnvironmentSpec {
	for i := range project.Spec.Environments {
		if project.Spec.Environments[i].Name == name {
			return &project.Spec.Environments[i]
		}
	}
	return nil
}

func removeUnusedProjectDependencyBinding(env *aiv1alpha1.ProjectEnvironmentSpec, bindingName string) {
	if env == nil || bindingName == "" {
		return
	}
	for _, connection := range env.Connections {
		if connection.SourceRef.Kind == aiv1alpha1.ProjectConnectionReferenceBinding && connection.SourceRef.Name == bindingName {
			return
		}
	}
	for i := range env.Bindings {
		if env.Bindings[i].Name == bindingName {
			env.Bindings = append(env.Bindings[:i], env.Bindings[i+1:]...)
			return
		}
	}
}

func (s *Server) updateProjectDependency(ctx context.Context, c *asclient.Client, projectName, name string, normalized *aiv1alpha1.ProjectEnvironmentConnectionSpec, binding *aiv1alpha1.ProjectProviderBindingSpec, environment string, remove bool) (*aiv1alpha1.Project, error) {
	for attempt := 0; attempt < 3; attempt++ {
		current, err := c.Projects().Get(ctx, projectName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		next := current.DeepCopy()
		env := findProjectEnvironment(next, environment)
		if env == nil {
			if remove {
				return nil, apierrors.NewNotFound(aiv1alpha1.SchemeGroupVersion.WithResource("projects").GroupResource(), name)
			}
			next.Spec.Environments = append(next.Spec.Environments, aiv1alpha1.ProjectEnvironmentSpec{Name: environment, Mode: aiv1alpha1.ProjectEnvironmentModeLive})
			env = &next.Spec.Environments[len(next.Spec.Environments)-1]
		}
		index := -1
		oldBindingName := ""
		for i := range env.Connections {
			if env.Connections[i].Name == name {
				index = i
				oldBindingName = env.Connections[i].SourceRef.Name
				break
			}
		}
		if remove {
			if index < 0 {
				return nil, apierrors.NewNotFound(aiv1alpha1.SchemeGroupVersion.WithResource("projects").GroupResource(), name)
			}
			env.Connections = append(env.Connections[:index], env.Connections[index+1:]...)
			removeUnusedProjectDependencyBinding(env, oldBindingName)
		} else {
			if index < 0 && len(env.Connections) >= projectDependencyMaxCount {
				return nil, errors.New("Project environment already has the maximum number of dependencies")
			}
			if index >= 0 {
				env.Connections[index] = *normalized
			} else {
				env.Connections = append(env.Connections, *normalized)
			}
			bindingIndex := -1
			for i := range env.Bindings {
				if env.Bindings[i].Name == binding.Name {
					bindingIndex = i
					break
				}
			}
			if bindingIndex >= 0 {
				env.Bindings[bindingIndex] = *binding
			} else {
				env.Bindings = append(env.Bindings, *binding)
			}
			if oldBindingName != "" && oldBindingName != binding.Name {
				removeUnusedProjectDependencyBinding(env, oldBindingName)
			}
		}
		updated, err := c.Projects().Update(ctx, next, metav1.UpdateOptions{})
		if err == nil {
			return updated, nil
		}
		if !apierrors.IsConflict(err) || attempt == 2 {
			return nil, err
		}
	}
	return nil, errors.New("Project dependency update conflicted")
}

func (s *Server) upsertProjectDependency(w http.ResponseWriter, r *http.Request) {
	c, _, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(mux.Vars(r)["dependency"])
	var request projectDependencyMutationRequest
	if !decodeStrictJSON(w, r, &request) {
		return
	}
	binding, connection, err := s.normalizeProjectDependency(r.Context(), c, project, name, request)
	if err != nil {
		writeProjectError(w, newValidationError(err.Error()))
		return
	}
	environment := strings.TrimSpace(request.Environment)
	if environment == "" {
		environment = projectDependencyEnvironmentDefault
	}
	updated, err := s.updateProjectDependency(r.Context(), c, project.Name, name, &connection, &binding, environment, false)
	if err != nil {
		writeProjectError(w, err)
		return
	}
	view, _ := projectDependencyViewByName(updated, environment, name)
	writeJSON(w, http.StatusOK, map[string]any{"dependency": view, "items": projectDependenciesView(updated).Items})
}

func (s *Server) assistantListProjectDependencyTemplates(ctx context.Context, req projectAssistantToolCallRequest) (string, error) {
	if req.Project == nil {
		return "", errors.New("no project on this run")
	}
	c, err := s.clientFor(req.Identity)
	if err != nil {
		return "", err
	}
	list, err := c.Resource(templateResource, "").List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", err
	}
	targets, err := projectDependencyTargetInterfaces(ctx, c, req.Project, projectDependencyEnvironmentDefault)
	if err != nil && !apierrors.IsNotFound(err) {
		return "", err
	}
	return projectAssistantToolJSONResult(projectDependencyCatalogResponse{Templates: projectDependencyTemplateViews(list.Items), TargetInterfaces: targets}, nil)
}

func (s *Server) assistantListProjectDependencies(_ context.Context, req projectAssistantToolCallRequest) (string, error) {
	if req.Project == nil {
		return "", errors.New("no project on this run")
	}
	return projectAssistantToolJSONResult(projectDependenciesView(req.Project), nil)
}

func (s *Server) assistantUpsertProjectDependency(ctx context.Context, req projectAssistantToolCallRequest) (string, error) {
	if req.Project == nil {
		return "", errors.New("no project on this run")
	}
	name := projectToolString(req.Arguments["dependency"])
	raw, err := json.Marshal(req.Arguments)
	if err != nil {
		return "", err
	}
	var request projectDependencyMutationRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return "", err
	}
	c, err := s.clientFor(req.Identity)
	if err != nil {
		return "", err
	}
	binding, connection, err := s.normalizeProjectDependency(ctx, c, req.Project, name, request)
	if err != nil {
		return "", err
	}
	environment := strings.TrimSpace(request.Environment)
	if environment == "" {
		environment = projectDependencyEnvironmentDefault
	}
	updated, err := s.updateProjectDependency(ctx, c, req.Project.Name, name, &connection, &binding, environment, false)
	if err != nil {
		return "", err
	}
	refreshProjectToolSnapshot(req.Project, updated)
	view, _ := projectDependencyViewByName(updated, environment, name)
	return projectAssistantToolJSONResult(map[string]any{"dependency": view, "items": projectDependenciesView(updated).Items}, nil)
}

func (s *Server) assistantDeleteProjectDependency(ctx context.Context, req projectAssistantToolCallRequest) (string, error) {
	if req.Project == nil {
		return "", errors.New("no project on this run")
	}
	name := projectToolString(req.Arguments["dependency"])
	if err := projectDependencyNameValid(name); err != nil {
		return "", err
	}
	environment := projectToolString(req.Arguments["environment"])
	if environment == "" {
		environment = projectDependencyEnvironmentDefault
	}
	c, err := s.clientFor(req.Identity)
	if err != nil {
		return "", err
	}
	updated, err := s.updateProjectDependency(ctx, c, req.Project.Name, name, nil, nil, environment, true)
	if err != nil {
		return "", err
	}
	refreshProjectToolSnapshot(req.Project, updated)
	return projectAssistantToolJSONResult(map[string]any{"deleted": true, "dependency": name, "retainedSource": true}, nil)
}

func (s *Server) deleteProjectDependency(w http.ResponseWriter, r *http.Request) {
	c, _, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	name := strings.TrimSpace(mux.Vars(r)["dependency"])
	if err := projectDependencyNameValid(name); err != nil {
		writeProjectError(w, newValidationError(err.Error()))
		return
	}
	environment := strings.TrimSpace(r.URL.Query().Get("environment"))
	if environment == "" {
		environment = projectDependencyEnvironmentDefault
	}
	if _, err := s.updateProjectDependency(r.Context(), c, project.Name, name, nil, nil, environment, true); err != nil {
		writeProjectError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
