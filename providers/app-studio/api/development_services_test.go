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
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	asclient "github.com/faroshq/provider-app-studio/client"
)

func developmentServicesTestClient(objects ...runtime.Object) *asclient.Client {
	scheme := runtime.NewScheme()
	if err := aiv1alpha1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		scheme,
		map[schema.GroupVersionResource]string{
			asclient.ProjectGVR:             "ProjectList",
			templatesGVR:                    "TemplateList",
			runSandboxInstancesResource.GVR: "InstanceList",
			developmentServicesResource.GVR: "DevelopmentServiceList",
		},
		objects...,
	)
	return asclient.NewFromDynamic(dynamicClient)
}

func developmentServicesTestProject(name, uid string) *aiv1alpha1.Project {
	return &aiv1alpha1.Project{
		TypeMeta: metav1.TypeMeta{APIVersion: aiv1alpha1.SchemeGroupVersion.String(), Kind: "Project"},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			UID:  types.UID(uid),
		},
		Spec: aiv1alpha1.ProjectSpec{
			Environments: []aiv1alpha1.ProjectEnvironmentSpec{{
				Name: "development",
				Mode: aiv1alpha1.ProjectEnvironmentModeLive,
			}},
		},
	}
}

func developmentServicesTestMutationRequest() projectDevelopmentServiceMutationRequest {
	enabled := true
	return projectDevelopmentServiceMutationRequest{
		Enabled: &enabled,
		Command: &projectDevelopmentServiceCommandRequest{
			Argv:             []string{"npm", "run", "dev"},
			WorkingDirectory: ".",
		},
		Endpoint: &projectDevelopmentServiceEndpointRequest{
			Protocol:   "HTTP",
			Port:       3000,
			HealthPath: "/healthz",
		},
		Exposure:      &projectDevelopmentServiceExposureRequest{Visibility: projectDevelopmentServicePrivate},
		RestartPolicy: "Always",
	}
}

func newDevelopmentServicesTestObject(t *testing.T, project *aiv1alpha1.Project, logicalName string, ready bool, url string) *unstructured.Unstructured {
	t.Helper()
	mutation := developmentServicesTestMutationRequest()
	request, err := projectDevelopmentServiceNormalizeRequest(&mutation, nil)
	if err != nil {
		t.Fatal(err)
	}
	obj, err := projectDevelopmentServiceObject(project, logicalName, "sandbox", "sandbox-uid", request, nil)
	if err != nil {
		t.Fatal(err)
	}
	phase := "Pending"
	if ready {
		phase = "Ready"
	}
	if err := unstructured.SetNestedField(obj.Object, map[string]any{
		"phase": phase,
		"ready": ready,
		"url":   url,
		"process": map[string]any{
			"phase":         phase,
			"running":       ready,
			"portListening": ready,
			"reachable":     ready,
		},
	}, "status"); err != nil {
		t.Fatal(err)
	}
	return obj
}

func setDevelopmentServicesTestIdentity(request *http.Request) {
	request.Header.Set("X-Faros-Tenant", "root:faros:tenants:org:workspace")
	request.Header.Set("X-Faros-Cluster", "cluster-a")
	request.Header.Set("X-Faros-User", "alice")
	request.Header.Set("Authorization", "Bearer test-token")
}

type developmentServicesRoundTripFunc func(*http.Request) (*http.Response, error)

func (f developmentServicesRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestDevelopmentServicePhysicalNameIsProjectUIDAndLogicalScoped(t *testing.T) {
	first := developmentServicesTestProject("shop", "project-uid-a")
	second := developmentServicesTestProject("shop", "project-uid-b")
	firstName := projectDevelopmentServicePhysicalName(first, "web")
	secondName := projectDevelopmentServicePhysicalName(second, "web")
	apiName := projectDevelopmentServicePhysicalName(first, "api")
	if firstName == secondName || firstName == apiName {
		t.Fatalf("physical names collided: first=%q second=%q api=%q", firstName, secondName, apiName)
	}
	for _, name := range []string{firstName, secondName, apiName} {
		if err := projectDevelopmentServiceNameValid(name); err != nil {
			t.Fatalf("physical name %q is not DNS-safe: %v", name, err)
		}
	}
	mutation := developmentServicesTestMutationRequest()
	request, err := projectDevelopmentServiceNormalizeRequest(&mutation, nil)
	if err != nil {
		t.Fatal(err)
	}
	obj, err := projectDevelopmentServiceObject(first, "web", "sandbox", "sandbox-uid", request, nil)
	if err != nil {
		t.Fatal(err)
	}
	if obj.GetName() != firstName {
		t.Fatalf("metadata.name = %q, want %q", obj.GetName(), firstName)
	}
	if got := obj.GetLabels()[projectDevelopmentServiceNameLabel]; got != "web" {
		t.Fatalf("logical-name label = %q, want web", got)
	}
	if got, _, _ := unstructured.NestedString(obj.Object, "spec", "sandboxRef", "name"); got != "sandbox" {
		t.Fatalf("sandboxRef.name = %q, want sandbox", got)
	}
	if got, _, _ := unstructured.NestedString(obj.Object, "spec", "sandboxRef", "uid"); got != "sandbox-uid" {
		t.Fatalf("sandboxRef.uid = %q, want sandbox-uid", got)
	}
}

func TestDevelopmentServiceOwnershipRequiresProjectNameAndUID(t *testing.T) {
	project := developmentServicesTestProject("shop", "project-uid")
	obj := newDevelopmentServicesTestObject(t, project, "web", false, "")
	cases := []struct {
		name   string
		mutate func(*unstructured.Unstructured, *aiv1alpha1.Project)
		want   bool
	}{
		{name: "matching name and uid", want: true},
		{
			name: "missing controller owner",
			mutate: func(obj *unstructured.Unstructured, _ *aiv1alpha1.Project) {
				obj.SetOwnerReferences(nil)
			},
		},
		{
			name: "wrong controller owner uid",
			mutate: func(obj *unstructured.Unstructured, _ *aiv1alpha1.Project) {
				owners := obj.GetOwnerReferences()
				owners[0].UID = types.UID("other-uid")
				obj.SetOwnerReferences(owners)
			},
		},
		{
			name: "wrong project uid",
			mutate: func(obj *unstructured.Unstructured, _ *aiv1alpha1.Project) {
				labels := obj.GetLabels()
				labels["faros.sh/project-uid"] = "other-uid"
				obj.SetLabels(labels)
			},
		},
		{
			name: "missing project uid",
			mutate: func(obj *unstructured.Unstructured, _ *aiv1alpha1.Project) {
				labels := obj.GetLabels()
				delete(labels, "faros.sh/project-uid")
				obj.SetLabels(labels)
				unstructured.RemoveNestedField(obj.Object, "spec", "projectRef", "uid")
			},
		},
		{
			name: "wrong project name",
			mutate: func(obj *unstructured.Unstructured, _ *aiv1alpha1.Project) {
				labels := obj.GetLabels()
				labels["faros.sh/project"] = "other-project"
				obj.SetLabels(labels)
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			candidate := obj.DeepCopy()
			if test.mutate != nil {
				test.mutate(candidate, project)
			}
			if got := projectDevelopmentServiceBelongsToProject(candidate, project); got != test.want {
				t.Fatalf("belongsToProject = %t, want %t; labels=%v", got, test.want, candidate.GetLabels())
			}
		})
	}
}

func TestDevelopmentServiceMutationValidationAndVisibilityTranslation(t *testing.T) {
	privateMutation := developmentServicesTestMutationRequest()
	private, err := projectDevelopmentServiceNormalizeRequest(&privateMutation, nil)
	if err != nil {
		t.Fatalf("private request normalization: %v", err)
	}
	if private.Command.WorkingDirectory != "." || private.Endpoint.HealthPath != "/healthz" || private.RestartPolicy != "Always" {
		t.Fatalf("normalized private request = %+v", private)
	}
	public := developmentServicesTestMutationRequest()
	public.Exposure = &projectDevelopmentServiceExposureRequest{Visibility: projectDevelopmentServicePublic}
	if _, err := projectDevelopmentServiceNormalizeRequest(&public, nil); err == nil || !strings.Contains(err.Error(), "confirmPublic") {
		t.Fatalf("public request without confirmation error = %v, want confirmation requirement", err)
	}
	public.ConfirmPublic = true
	normalized, err := projectDevelopmentServiceNormalizeRequest(&public, nil)
	if err != nil {
		t.Fatalf("confirmed public request normalization: %v", err)
	}
	if got := projectDevelopmentServiceVisibilityProvider(normalized.Exposure.Visibility); got != projectDevelopmentInfrastructurePublic {
		t.Fatalf("provider visibility = %q, want %q", got, projectDevelopmentInfrastructurePublic)
	}
	for _, value := range []string{"Private", "private"} {
		if got := projectDevelopmentServiceVisibilityHTTP(value); got != projectDevelopmentServicePrivate {
			t.Fatalf("HTTP visibility(%q) = %q, want private", value, got)
		}
	}
	if got := projectDevelopmentServiceVisibilityHTTP("Public"); got != projectDevelopmentServicePublic {
		t.Fatalf("HTTP visibility(Public) = %q, want public", got)
	}
	project := developmentServicesTestProject("shop", "project-uid")
	project.Spec.Components = []aiv1alpha1.ProjectComponentSpec{{Name: "web", Kind: aiv1alpha1.ProjectComponentKindService, SourcePath: "."}}
	if !projectDevelopmentServiceComponentExists(project, "web") || projectDevelopmentServiceComponentExists(project, "missing") {
		t.Fatal("componentRef existence check does not match Project.spec.components")
	}
	badPort := developmentServicesTestMutationRequest()
	badPort.Endpoint.Port = 7070
	if _, err := projectDevelopmentServiceNormalizeRequest(&badPort, nil); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("reserved port validation error = %v", err)
	}
}

func TestDevelopmentServicePrimaryUsesLogicalNameAndPersistsInDevelopmentEnvironment(t *testing.T) {
	project := developmentServicesTestProject("shop", "project-uid")
	project.Spec.Environments = append(project.Spec.Environments, aiv1alpha1.ProjectEnvironmentSpec{Name: "production", Mode: aiv1alpha1.ProjectEnvironmentModeArtifact})
	web := newDevelopmentServicesTestObject(t, project, "web", false, "")
	api := newDevelopmentServicesTestObject(t, project, "api", true, "https://api.preview.test")
	if got := projectDevelopmentPrimaryService(project, []*unstructured.Unstructured{web, api}); got != "api" {
		t.Fatalf("automatic primary = %q, want api logical name", got)
	}
	project.Spec.Environments[0].Preview = &aiv1alpha1.ProjectEnvironmentPreviewSpec{PrimaryServiceRef: "web"}
	if got := projectDevelopmentPrimaryService(project, []*unstructured.Unstructured{web, api}); got != "web" {
		t.Fatalf("persisted primary = %q, want web logical name", got)
	}
	client := developmentServicesTestClient(project)
	updated, err := (&Server{}).patchProjectPrimaryService(context.Background(), client, project, "api")
	if err != nil {
		t.Fatalf("persist primary: %v", err)
	}
	if updated.Spec.Environments[0].Preview == nil || updated.Spec.Environments[0].Preview.PrimaryServiceRef != "api" {
		t.Fatalf("updated primary = %+v, want api", updated.Spec.Environments[0].Preview)
	}
	if updated.Spec.Environments[1].Name != "production" || updated.Spec.Environments[1].Mode != aiv1alpha1.ProjectEnvironmentModeArtifact {
		t.Fatalf("primary update changed another environment: %+v", updated.Spec.Environments)
	}
}

func TestDevelopmentServicePreviewSelectsRequestedReadyService(t *testing.T) {
	project := developmentServicesTestProject("shop", "project-uid")
	web := newDevelopmentServicesTestObject(t, project, "web", false, "https://web.preview.test")
	api := newDevelopmentServicesTestObject(t, project, "api", true, "https://api.preview.test")
	client := developmentServicesTestClient(project, web, api)
	server := &Server{previewEdgeProbe: func(context.Context, string) error { return nil }}
	preview, found, err := server.projectDevelopmentServicePreview(context.Background(), client, project, projectDevelopmentSyncTargetInfo{}, "api")
	if err != nil || !found {
		t.Fatalf("service preview found=%t err=%v", found, err)
	}
	if !preview.Ready || preview.ServiceName != "api" || preview.PreviewURL != "https://api.preview.test" || !preview.ProcessRunning || !preview.PortListening || !preview.Reachable {
		t.Fatalf("ready service preview = %+v", preview)
	}
	notFound, found, err := server.projectDevelopmentServicePreview(context.Background(), client, project, projectDevelopmentSyncTargetInfo{}, "missing")
	if err != nil || !found || notFound.Ready || notFound.Reason != "development_service_not_found" {
		t.Fatalf("missing service preview = %+v found=%t err=%v", notFound, found, err)
	}
}

func TestDevelopmentServiceListenersUseDistinctListenersDataPlaneVerb(t *testing.T) {
	var observed *http.Request
	server := &Server{
		hubBase: "https://hub.internal",
		sandboxDataPlaneClientFactory: func(time.Duration) *http.Client {
			return &http.Client{Transport: developmentServicesRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				observed = request
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"listeners":[{"port":3000,"protocol":"TCP"}]}`)),
					Header:     make(http.Header),
				}, nil
			})}
		},
	}
	body, status, err := server.dataPlaneGet(context.Background(), identity{clusterID: "cluster-a", token: "test-token"}, dataPlaneRef{Resource: "instances", Name: "sandbox", Component: "workspace"}, dataPlaneVerbListeners, 16<<10)
	if err != nil || status != http.StatusOK {
		t.Fatalf("listeners data-plane GET status=%d err=%v", status, err)
	}
	if observed == nil || observed.Method != http.MethodGet || !strings.HasSuffix(observed.URL.Path, "/components/workspace/listeners") || strings.Contains(observed.URL.Path, "/process") {
		t.Fatalf("listeners request = %#v, want GET .../listeners", observed)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil || payload["listeners"] == nil {
		t.Fatalf("listeners response = %q err=%v", body, err)
	}
}

func TestDevelopmentServiceLogsResolveLogicalNameToPhysicalDataPlanePath(t *testing.T) {
	project := developmentServicesTestProject("shop", "project-uid")
	service := newDevelopmentServicesTestObject(t, project, "web", true, "https://web.preview.test")
	client := developmentServicesTestClient(project, service)
	var observed *http.Request
	server := &Server{
		hubBase:          "https://hub.internal",
		projectClientFor: func(identity) (*asclient.Client, error) { return client, nil },
		sandboxDataPlaneClientFactory: func(time.Duration) *http.Client {
			return &http.Client{Transport: developmentServicesRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				observed = request
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("web logs\n")), Header: make(http.Header)}, nil
			})}
		},
	}
	request := httptest.NewRequest(http.MethodGet, "/api/projects/shop/development-services/web/logs", nil)
	request = mux.SetURLVars(request, map[string]string{"project": "shop", "service": "web"})
	setDevelopmentServicesTestIdentity(request)
	response := httptest.NewRecorder()
	server.logsProjectDevelopmentService(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "web logs\n" {
		t.Fatalf("logs response status=%d body=%q", response.Code, response.Body.String())
	}
	wantPath := server.dataPlaneURL("cluster-a", dataPlaneRef{Resource: developmentServicesResource.GVR.Resource, Name: service.GetName()}, dataPlaneVerbDevelopmentServiceLogs, "")
	if observed == nil || observed.URL.String() != wantPath {
		t.Fatalf("logs request = %v, want %s", observed, wantPath)
	}
	if observed.Header.Get("Authorization") != "Bearer test-token" || observed.Header.Get("X-Sandbox-Control-Token") != "" {
		t.Fatalf("logs request headers expose unexpected credentials: %v", observed.Header)
	}
}

func TestDevelopmentServiceToolRegistryDeclaresRuntimeRiskAndPublicConfirmation(t *testing.T) {
	registry := projectAssistantLocalToolRegistry(&Server{})
	upsert, ok := registry.Spec(projectToolUpsertDevelopmentService)
	if !ok || upsert.Risk != projectAssistantToolRiskRuntime {
		t.Fatalf("upsert tool spec = %+v found=%t, want runtime risk", upsert, ok)
	}
	if !strings.Contains(upsert.Description, "confirmPublic=true") || !strings.Contains(upsert.Description, "never auto-exposed") || !strings.Contains(string(upsert.Parameters), "confirmPublic") {
		t.Fatalf("upsert tool metadata omits public confirmation/suggestion contract: %+v", upsert)
	}
	listeners, ok := registry.Spec(projectToolListDetectedListeners)
	if !ok || listeners.Risk != projectAssistantToolRiskRead || !strings.Contains(listeners.Description, "listeners endpoint") || !strings.Contains(listeners.Description, "never auto-exposed") {
		t.Fatalf("listener tool metadata = %+v found=%t", listeners, ok)
	}
	componentDelete, ok := registry.Spec(projectToolDeleteProjectComponent)
	if !ok || !strings.Contains(componentDelete.Description, "componentRef") {
		t.Fatalf("component delete metadata = %+v found=%t", componentDelete, ok)
	}
}

func TestDeleteProjectComponentRejectsDanglingDevelopmentServiceReference(t *testing.T) {
	project := developmentServicesTestProject("shop", "project-uid")
	project.Spec.Components = []aiv1alpha1.ProjectComponentSpec{{Name: "web", Kind: aiv1alpha1.ProjectComponentKindService, SourcePath: "."}}
	service := newDevelopmentServicesTestObject(t, project, "web-preview", true, "https://web.preview.test")
	_ = unstructured.SetNestedField(service.Object, "web", "spec", "componentRef")
	client := developmentServicesTestClient(project, service)
	server := &Server{projectClientFor: func(identity) (*asclient.Client, error) { return client, nil }}
	request := httptest.NewRequest(http.MethodDelete, "/api/projects/shop/components/web", nil)
	request = mux.SetURLVars(request, map[string]string{"project": "shop", "component": "web"})
	setDevelopmentServicesTestIdentity(request)
	response := httptest.NewRecorder()
	server.deleteProjectComponent(response, request)
	if response.Code == http.StatusNoContent {
		t.Fatal("component deletion succeeded while DevelopmentService still references it")
	}
	if !strings.Contains(response.Body.String(), "referenced by DevelopmentService") || !strings.Contains(response.Body.String(), "remove or change componentRef first") {
		t.Fatalf("dangling component error = %q", response.Body.String())
	}
	stored, err := client.Projects().Get(context.Background(), project.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Spec.Components) != 1 || stored.Spec.Components[0].Name != "web" {
		t.Fatalf("Project components after rejected deletion = %+v", stored.Spec.Components)
	}
}
