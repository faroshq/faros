/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	asclient "github.com/faroshq/provider-app-studio/client"
)

func projectDependencyTemplate(name, interfaceName, interfaceType string, provides bool) *unstructured.Unstructured {
	connection := map[string]any{"name": interfaceName, "type": interfaceType}
	connections := map[string]any{}
	if provides {
		connection["secretRefPath"] = "status.connectionSecretRef"
		connection["keys"] = []any{"host", "password", "uri"}
		connections["provides"] = []any{connection}
	} else {
		connection["mappings"] = []any{
			map[string]any{"sourceKey": "uri", "targetKey": "DATABASE_URL"},
			map[string]any{"sourceKey": "host", "targetKey": "DATABASE_HOST"},
		}
		connections["consumes"] = []any{connection}
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "infrastructure.faros.sh/v1alpha1", "kind": "Template",
		"metadata": map[string]any{"name": name},
		"spec": map[string]any{
			"displayName": name, "description": "test template", "category": "Data",
			"lifecycle": map[string]any{"defaultDeletionPolicy": "Retain"},
			"schema": map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
				"name":     map[string]any{"type": "string", "pattern": "^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"},
				"version":  map[string]any{"type": "string", "enum": []any{"15", "16"}, "default": "16"},
				"password": map[string]any{"type": "string", "description": "Even a declared credential field is never accepted by App Studio."},
			}, "required": []any{"name"}},
			"sampleValues": map[string]any{"version": "16", "password": "must-not-leak", "nested": map[string]any{"token": "must-not-leak"}},
			"connections":  connections,
		},
	}}
}

func projectDependencyTestProject() *aiv1alpha1.Project {
	p := developmentServicesTestProject("shop", "project-uid")
	p.Spec.Environments[0].Bindings = []aiv1alpha1.ProjectProviderBindingSpec{{
		Name: projectDevelopmentBindingName, Provider: projectDevelopmentProviderAppStudio, Kind: aiv1alpha1.ProjectBindingKindProviderResource,
		TemplateRef: &aiv1alpha1.ProjectTemplateSpec{Name: "universal-coding-sandbox"},
		ResourceRef: &aiv1alpha1.ProjectProviderResourceReference{Name: "shop-sandbox", APIVersion: "infrastructure.faros.sh/v1alpha1", Kind: "Instance", Resource: "instances"},
	}}
	return p
}

func projectDependencyMetadataLightProject() *aiv1alpha1.Project {
	p := developmentServicesTestProject("shop", "project-uid")
	p.Spec.Template = nil
	p.Spec.Environments[0].Bindings = nil
	return p
}

func projectDependencySandboxInstance(project *aiv1alpha1.Project, name, uid, templateName string) *unstructured.Unstructured {
	controller := true
	instance := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "infrastructure.faros.sh/v1alpha1",
		"kind":       "Instance",
		"metadata": map[string]any{
			"name": name,
		},
		"spec": map[string]any{
			"template": templateName,
		},
	}}
	instance.SetUID(types.UID(uid))
	instance.SetAnnotations(map[string]string{projectAssistantRunSandboxLabel: "true"})
	instance.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: aiv1alpha1.SchemeGroupVersion.String(), Kind: "Project", Name: project.Name, UID: project.UID, Controller: &controller,
	}})
	return instance
}

func projectDependencyTestServer(objects ...runtime.Object) (*Server, *asclient.Client) {
	client := developmentServicesTestClient(objects...)
	return &Server{projectClientFor: func(identity) (*asclient.Client, error) { return client, nil }}, client
}

func TestProjectDependencyCatalogIsTypedAndSecretValueFree(t *testing.T) {
	p := projectDependencyTestProject()
	database := projectDependencyTemplate("database", "default", "postgresql", true)
	consumer := projectDependencyTemplate("universal-coding-sandbox", "postgresql", "postgresql", false)
	plain := projectDependencyTemplate("plain", "default", "none", true)
	unstructured.RemoveNestedField(plain.Object, "spec", "connections")
	server, _ := projectDependencyTestServer(p, database, consumer, plain)

	request := httptest.NewRequest(http.MethodGet, "/api/projects/shop/dependencies/catalog", nil)
	request = mux.SetURLVars(request, map[string]string{"project": "shop"})
	setDevelopmentServicesTestIdentity(request)
	response := httptest.NewRecorder()
	server.getProjectDependencyCatalog(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var catalog projectDependencyCatalogResponse
	if err := json.Unmarshal(response.Body.Bytes(), &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Templates) != 1 || catalog.Templates[0].Name != "database" || len(catalog.TargetInterfaces) != 1 || catalog.TargetInterfaces[0].Name != "postgresql" {
		t.Fatalf("catalog=%+v", catalog)
	}
	properties, _ := catalog.Templates[0].Schema["properties"].(map[string]any)
	if _, found := properties["password"]; found {
		t.Fatalf("credential field survived catalog schema sanitization: %v", properties)
	}
	text := response.Body.String()
	for _, forbidden := range []string{"must-not-leak", "secretRefPath", "connectionSecretRef"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("catalog leaked %q: %s", forbidden, text)
		}
	}
}

func TestProjectDependencyResolvesMetadataLightDevelopmentServiceSandboxTemplate(t *testing.T) {
	p := projectDependencyMetadataLightProject()
	database := projectDependencyTemplate("database", "default", "postgresql", true)
	consumer := projectDependencyTemplate("universal-coding-sandbox", "postgresql", "postgresql", false)
	service := newDevelopmentServicesTestObject(t, p, "api", true, "https://api.example")
	instance := projectDependencySandboxInstance(p, "sandbox", "sandbox-uid", "universal-coding-sandbox")
	server, client := projectDependencyTestServer(p, database, consumer, service, instance)

	interfaces, err := server.projectDependencyTargetInterfaces(t.Context(), client, p, projectDependencyEnvironmentDefault)
	if err != nil {
		t.Fatal(err)
	}
	if len(interfaces) != 1 || interfaces[0].Name != "postgresql" || interfaces[0].Type != "postgresql" {
		t.Fatalf("target interfaces=%+v, want postgresql", interfaces)
	}

	_, connection, err := server.normalizeProjectDependency(t.Context(), client, p, "database", projectDependencyMutationRequest{
		Template: "database", Values: map[string]any{"version": "16"}, SourceInterface: "default",
		TargetRef:       aiv1alpha1.ProjectConnectionEndpointReference{Kind: aiv1alpha1.ProjectConnectionReferenceDevelopmentService, Name: "api"},
		TargetInterface: "postgresql", Mappings: []aiv1alpha1.ProjectConnectionMappingSpec{{SourceKey: "uri", TargetKey: "DATABASE_URL"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if connection.TargetRef.Name != "api" || connection.TargetInterface != "postgresql" {
		t.Fatalf("connection=%+v, want api postgresql target", connection)
	}
}

func TestProjectDependencyRejectsUnsafeDevelopmentServiceSandboxTemplateFallback(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*aiv1alpha1.Project, *unstructured.Unstructured)
		wantError string
	}{
		{
			name: "UID mismatch",
			mutate: func(_ *aiv1alpha1.Project, instance *unstructured.Unstructured) {
				instance.SetUID(types.UID("replacement-uid"))
			},
			wantError: "UID does not match",
		},
		{
			name: "foreign owner",
			mutate: func(_ *aiv1alpha1.Project, instance *unstructured.Unstructured) {
				owners := instance.GetOwnerReferences()
				owners[0].UID = types.UID("other-project-uid")
				instance.SetOwnerReferences(owners)
			},
			wantError: "is not owned by this Project",
		},
		{
			name: "missing template",
			mutate: func(_ *aiv1alpha1.Project, instance *unstructured.Unstructured) {
				unstructured.RemoveNestedField(instance.Object, "spec", "template")
			},
			wantError: "has no Infrastructure Template",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := projectDependencyMetadataLightProject()
			service := newDevelopmentServicesTestObject(t, p, "api", true, "")
			instance := projectDependencySandboxInstance(p, "sandbox", "sandbox-uid", "universal-coding-sandbox")
			tt.mutate(p, instance)
			server, client := projectDependencyTestServer(p, service, instance)

			_, err := server.resolveProjectDependencyTargetTemplateName(t.Context(), client, p, projectDependencyEnvironmentDefault, aiv1alpha1.ProjectConnectionEndpointReference{
				Kind: aiv1alpha1.ProjectConnectionReferenceDevelopmentService, Name: "api",
			})
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error=%v, want %q", err, tt.wantError)
			}
		})
	}
}

func TestProjectDependencyUpsertRoundTripAndDeleteRetainsSource(t *testing.T) {
	p := projectDependencyTestProject()
	database := projectDependencyTemplate("database", "default", "postgresql", true)
	consumer := projectDependencyTemplate("universal-coding-sandbox", "postgresql", "postgresql", false)
	service := newDevelopmentServicesTestObject(t, p, "web", true, "https://web.example")
	server, client := projectDependencyTestServer(p, database, consumer, service)
	body := `{"template":"database","values":{"version":"16"},"targetRef":{"kind":"developmentService","name":"web"}}`
	request := httptest.NewRequest(http.MethodPut, "/api/projects/shop/dependencies/database", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request = mux.SetURLVars(request, map[string]string{"project": "shop", "dependency": "database"})
	setDevelopmentServicesTestIdentity(request)
	response := httptest.NewRecorder()
	server.upsertProjectDependency(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var result struct {
		Dependency projectDependencyView   `json:"dependency"`
		Items      []projectDependencyView `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Dependency.Name != "database" || len(result.Items) != 1 || result.Dependency.DeletionPolicy != aiv1alpha1.ProjectBindingDeletionPolicyRetain {
		t.Fatalf("response=%+v", result)
	}
	updated, err := client.Projects().Get(t.Context(), "shop", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	env := &updated.Spec.Environments[0]
	if len(env.Connections) != 1 || len(env.Bindings) != 2 {
		t.Fatalf("environment=%+v", env)
	}
	connection := env.Connections[0]
	if connection.SourceInterface != "default" || connection.TargetInterface != "postgresql" || len(connection.Mappings) != 2 || connection.Mappings[0].TargetKey != "DATABASE_URL" {
		t.Fatalf("inferred connection=%+v", connection)
	}
	binding := projectDependencyFindBinding(env, connection.SourceRef.Name)
	if binding == nil || binding.TemplateRef == nil || binding.TemplateRef.Name != "database" || binding.Lifecycle == nil || binding.Lifecycle.DeletionPolicy != aiv1alpha1.ProjectBindingDeletionPolicyRetain {
		t.Fatalf("source binding=%+v", binding)
	}
	var values map[string]any
	if err := json.Unmarshal(binding.Values.Raw, &values); err != nil {
		t.Fatal(err)
	}
	if values["name"] == "" || values["version"] != "16" {
		t.Fatalf("values=%v", values)
	}
	for key := range values {
		if strings.Contains(strings.ToLower(key), "password") || strings.Contains(strings.ToLower(key), "secret") {
			t.Fatalf("credential-like value persisted: %v", values)
		}
	}

	request = httptest.NewRequest(http.MethodDelete, "/api/projects/shop/dependencies/database", nil)
	request = mux.SetURLVars(request, map[string]string{"project": "shop", "dependency": "database"})
	setDevelopmentServicesTestIdentity(request)
	response = httptest.NewRecorder()
	server.deleteProjectDependency(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", response.Code, response.Body.String())
	}
	updated, err = client.Projects().Get(t.Context(), "shop", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	env = &updated.Spec.Environments[0]
	if len(env.Connections) != 0 || len(env.Bindings) != 1 || env.Bindings[0].Name != projectDevelopmentBindingName {
		t.Fatalf("delete left environment=%+v", env)
	}
}

func TestWaitForProjectDependencyReadyIgnoresTransientFailure(t *testing.T) {
	p := projectDependencyTestProject()
	p.Spec.Environments[0].Connections = []aiv1alpha1.ProjectEnvironmentConnectionSpec{{
		Name:      "database",
		SourceRef: aiv1alpha1.ProjectConnectionEndpointReference{Kind: aiv1alpha1.ProjectConnectionReferenceBinding, Name: "database"},
		TargetRef: aiv1alpha1.ProjectConnectionEndpointReference{Kind: aiv1alpha1.ProjectConnectionReferenceDevelopmentService, Name: "web"},
	}}
	p.Status.Environments = []aiv1alpha1.ProjectEnvironmentStatus{{
		Name:        projectDependencyEnvironmentDefault,
		Connections: []aiv1alpha1.ProjectEnvironmentConnectionStatus{{Name: "database", Phase: "Failed", Message: "source credentials are not available yet"}},
	}}
	_, client := projectDependencyTestServer(p)

	go func() {
		time.Sleep(20 * time.Millisecond)
		current, err := client.Projects().Get(context.Background(), p.Name, metav1.GetOptions{})
		if err != nil {
			return
		}
		current.Status.Environments[0].Connections[0] = aiv1alpha1.ProjectEnvironmentConnectionStatus{Name: "database", Phase: "Ready", Reason: "Ready", Revision: "revision-1"}
		_, _ = client.Projects().UpdateStatus(context.Background(), current, metav1.UpdateOptions{})
	}()

	updated, view, err := waitForProjectDependencyReady(t.Context(), client, p, projectDependencyEnvironmentDefault, "database", 5*time.Millisecond, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if updated == nil || view.Status == nil || view.Status.Phase != "Ready" || view.Status.Revision != "revision-1" {
		t.Fatalf("dependency view=%+v project=%+v", view, updated)
	}
}

func TestProjectDependencyInferenceRejectsAmbiguousCompatibleInterfaces(t *testing.T) {
	p := projectDependencyTestProject()
	database := projectDependencyTemplate("database", "default", "postgresql", true)
	provides, _, _ := unstructured.NestedSlice(database.Object, "spec", "connections", "provides")
	provides = append(provides, map[string]any{"name": "readonly", "type": "postgresql", "secretRefPath": "status.connectionSecretRef", "keys": []any{"host", "uri"}})
	if err := unstructured.SetNestedSlice(database.Object, provides, "spec", "connections", "provides"); err != nil {
		t.Fatal(err)
	}
	consumer := projectDependencyTemplate("universal-coding-sandbox", "postgresql", "postgresql", false)
	service := newDevelopmentServicesTestObject(t, p, "web", true, "")
	server, client := projectDependencyTestServer(p, database, consumer, service)

	request := projectDependencyMutationRequest{
		Template: "database", Values: map[string]any{"version": "16"},
		TargetRef: aiv1alpha1.ProjectConnectionEndpointReference{Kind: aiv1alpha1.ProjectConnectionReferenceDevelopmentService, Name: "web"},
	}
	_, _, err := server.normalizeProjectDependency(t.Context(), client, p, "database", request)
	if err == nil || !strings.Contains(err.Error(), "multiple compatible dependency interfaces") {
		t.Fatalf("error=%v, want explicit ambiguity", err)
	}

	request.SourceInterface = "default"
	_, connection, err := server.normalizeProjectDependency(t.Context(), client, p, "database", request)
	if err != nil {
		t.Fatal(err)
	}
	if connection.SourceInterface != "default" || connection.TargetInterface != "postgresql" || len(connection.Mappings) != 2 {
		t.Fatalf("explicitly resolved connection=%+v", connection)
	}
}

func TestProjectDependencyRejectsUnknownValuesAndIncompatibleInterfaces(t *testing.T) {
	p := projectDependencyTestProject()
	database := projectDependencyTemplate("database", "default", "postgresql", true)
	consumer := projectDependencyTemplate("universal-coding-sandbox", "redis", "redis", false)
	service := newDevelopmentServicesTestObject(t, p, "web", true, "")
	server, client := projectDependencyTestServer(p, database, consumer, service)

	_, _, err := server.normalizeProjectDependency(t.Context(), client, p, "database", projectDependencyMutationRequest{
		Template: "database", Values: map[string]any{"password": "bad"}, SourceInterface: "default",
		TargetRef: aiv1alpha1.ProjectConnectionEndpointReference{Kind: aiv1alpha1.ProjectConnectionReferenceDevelopmentService, Name: "web"}, TargetInterface: "redis",
	})
	if err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("error=%v, want incompatible", err)
	}
	consumer = projectDependencyTemplate("universal-coding-sandbox", "postgresql", "postgresql", false)
	server, client = projectDependencyTestServer(p, database, consumer, service)
	_, _, err = server.normalizeProjectDependency(t.Context(), client, p, "database", projectDependencyMutationRequest{
		Template: "database", Values: map[string]any{"password": "bad"}, SourceInterface: "default",
		TargetRef: aiv1alpha1.ProjectConnectionEndpointReference{Kind: aiv1alpha1.ProjectConnectionReferenceDevelopmentService, Name: "web"}, TargetInterface: "postgresql",
	})
	if err == nil || !strings.Contains(err.Error(), "credential material") {
		t.Fatalf("error=%v, want credential rejection", err)
	}
}

func TestProjectDependencyAssistantToolMetadataRequiresRuntimeConfirmation(t *testing.T) {
	registry := projectAssistantLocalToolRegistry(&Server{})
	for _, name := range []string{projectToolListDependencyTemplates, projectToolListProjectDependencies} {
		spec, ok := registry.Spec(name)
		if !ok || spec.Risk != projectAssistantToolRiskRead || !spec.ParallelSafe {
			t.Fatalf("read tool %q spec=%+v ok=%v", name, spec, ok)
		}
	}
	for _, name := range []string{projectToolUpsertProjectDependency, projectToolDeleteProjectDependency} {
		spec, ok := registry.Spec(name)
		if !ok || spec.Risk != projectAssistantToolRiskRuntime || spec.ParallelSafe {
			t.Fatalf("runtime tool %q spec=%+v ok=%v", name, spec, ok)
		}
	}
	upsertSpec, ok := registry.Spec(projectToolUpsertProjectDependency)
	if !ok {
		t.Fatal("dependency upsert tool is missing")
	}
	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(upsertSpec.Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(schema.Required, ","), "dependency,template,targetRef"; got != want {
		t.Fatalf("dependency upsert required fields=%q, want %q", got, want)
	}
	for _, guidance := range []string{
		"return successfully only after the connection is Ready",
		"complete project-dependency flow",
		"do not call infrastructure__provision",
		"Copy template exactly from list_dependency_templates.templates[].name",
		"use template=database, not postgres or postgresql",
		"Omit values.name unless the user explicitly chose an infrastructure name",
		"never copy it to values.farosMode",
		"App Studio infers them",
	} {
		if !strings.Contains(upsertSpec.Description, guidance) {
			t.Errorf("dependency upsert description is missing %q: %q", guidance, upsertSpec.Description)
		}
	}
	serviceSpec, ok := registry.Spec(projectToolUpsertDevelopmentService)
	if !ok || strings.Contains(string(serviceSpec.Parameters), "connectionRefs") {
		t.Fatalf("DevelopmentService mutation still exposes raw connectionRefs: %s", serviceSpec.Parameters)
	}
}

func TestDevelopmentServiceMutationRejectsCallerConnectionRefs(t *testing.T) {
	p := projectDependencyTestProject()
	server, client := projectDependencyTestServer(p)
	if _, err := client.Projects().Get(t.Context(), "shop", metav1.GetOptions{}); err != nil {
		t.Fatalf("test project get: %v", err)
	}
	request := httptest.NewRequest(http.MethodPut, "/api/projects/shop/development-services/web", strings.NewReader(`{"command":{"argv":["npm","start"]},"endpoint":{"port":3000},"connectionRefs":["raw-connection"]}`))
	request.Header.Set("Content-Type", "application/json")
	request = mux.SetURLVars(request, map[string]string{"project": "shop", "service": "web"})
	setDevelopmentServicesTestIdentity(request)
	response := httptest.NewRecorder()
	server.upsertProjectDevelopmentService(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "unsupported field") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
