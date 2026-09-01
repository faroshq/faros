/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

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

func TestProjectDependencyUpsertRoundTripAndDeleteRetainsSource(t *testing.T) {
	p := projectDependencyTestProject()
	database := projectDependencyTemplate("database", "default", "postgresql", true)
	consumer := projectDependencyTemplate("universal-coding-sandbox", "postgresql", "postgresql", false)
	service := newDevelopmentServicesTestObject(t, p, "web", true, "https://web.example")
	server, client := projectDependencyTestServer(p, database, consumer, service)
	body := `{"template":"database","values":{"version":"16"},"sourceInterface":"default","targetRef":{"kind":"developmentService","name":"web"},"targetInterface":"postgresql","mappings":[{"sourceKey":"uri","targetKey":"DATABASE_URL"}]}`
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
