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
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	"github.com/faroshq/provider-app-studio/bindings"
	asclient "github.com/faroshq/provider-app-studio/client"
	"github.com/faroshq/provider-app-studio/store"
	"github.com/faroshq/provider-app-studio/workspace"
)

func TestProjectInitialBootstrapPromptDigestDoesNotExposePrompt(t *testing.T) {
	digest := projectInitialBootstrapPromptDigest("Build a todo app")
	if digest == projectInitialBootstrapPromptDigest("Build an unbounded platform") || digest == "Build a todo app" {
		t.Fatalf("prompt digest did not distinguish or conceal the creation prompt: %q", digest)
	}
}

func testProjectDelivery(development, production aiv1alpha1.ProjectDeliveryMode) *aiv1alpha1.ProjectDeliverySpec {
	return &aiv1alpha1.ProjectDeliverySpec{
		Development: aiv1alpha1.ProjectEnvironmentDeliverySpec{Mode: development},
		Production:  aiv1alpha1.ProjectEnvironmentDeliverySpec{Mode: production},
	}
}

func TestProjectDeliveryForCreateDefaultsAndRejectsUnsafeAdoption(t *testing.T) {
	tests := []struct {
		name      string
		requested *aiv1alpha1.ProjectDeliverySpec
		adopted   bool
		wantDev   aiv1alpha1.ProjectDeliveryMode
		wantProd  aiv1alpha1.ProjectDeliveryMode
		wantErr   string
	}{
		{name: "new repository defaults hybrid", wantDev: aiv1alpha1.ProjectDeliveryModeDirect, wantProd: aiv1alpha1.ProjectDeliveryModeGitOps},
		{name: "new repository explicitly Direct", requested: testProjectDelivery(aiv1alpha1.ProjectDeliveryModeDirect, aiv1alpha1.ProjectDeliveryModeDirect), wantDev: aiv1alpha1.ProjectDeliveryModeDirect, wantProd: aiv1alpha1.ProjectDeliveryModeDirect},
		{name: "explicit GitOps development and Direct production", requested: testProjectDelivery(aiv1alpha1.ProjectDeliveryModeGitOps, aiv1alpha1.ProjectDeliveryModeDirect), wantDev: aiv1alpha1.ProjectDeliveryModeGitOps, wantProd: aiv1alpha1.ProjectDeliveryModeDirect},
		{name: "explicit GitOps development and production", requested: testProjectDelivery(aiv1alpha1.ProjectDeliveryModeGitOps, aiv1alpha1.ProjectDeliveryModeGitOps), wantDev: aiv1alpha1.ProjectDeliveryModeGitOps, wantProd: aiv1alpha1.ProjectDeliveryModeGitOps},
		{name: "adopted repository defaults Direct", adopted: true, wantDev: aiv1alpha1.ProjectDeliveryModeDirect, wantProd: aiv1alpha1.ProjectDeliveryModeDirect},
		{name: "adopted repository rejects any GitOps", requested: testProjectDelivery(aiv1alpha1.ProjectDeliveryModeDirect, aiv1alpha1.ProjectDeliveryModeGitOps), adopted: true, wantErr: "bootstrap migration"},
		{name: "unknown mode rejected", requested: testProjectDelivery("Automatic", aiv1alpha1.ProjectDeliveryModeDirect), wantErr: "Direct or GitOps"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delivery, err := projectDeliveryForCreate(tt.requested, tt.adopted)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if delivery.Development.Mode != tt.wantDev || delivery.Production.Mode != tt.wantProd {
				t.Fatalf("delivery = %+v, want development=%q production=%q", delivery, tt.wantDev, tt.wantProd)
			}
			if (tt.wantDev == aiv1alpha1.ProjectDeliveryModeGitOps || tt.wantProd == aiv1alpha1.ProjectDeliveryModeGitOps) && delivery.GitOps == nil {
				t.Fatal("GitOps defaults are missing")
			}
		})
	}
}

func TestProjectDeliveryUsesPolicyNotBindings(t *testing.T) {
	p := &aiv1alpha1.Project{Spec: aiv1alpha1.ProjectSpec{
		Repository: &aiv1alpha1.ProjectRepositoryBinding{RepositoryRef: "repo"},
		Environments: []aiv1alpha1.ProjectEnvironmentSpec{{Bindings: []aiv1alpha1.ProjectProviderBindingSpec{{
			Name: projectGitOpsBindingName,
			ResourceRef: &aiv1alpha1.ProjectProviderResourceReference{
				APIVersion: projectRepositorySyncAPIVersion, Resource: projectRepositorySyncResource,
			},
		}}}},
	}}
	if projectHasGitOps(p) {
		t.Fatal("an omitted delivery policy must remain Direct even when a stale RepositorySync binding exists")
	}
	p.Spec.Delivery = defaultProjectDelivery()
	p.Spec.Environments = nil
	if !projectHasGitOps(p) {
		t.Fatal("GitOps policy must remain authoritative when its RepositorySync binding is missing")
	}
	view := effectiveProjectDeliverySpec(&aiv1alpha1.Project{})
	if view.Development.Mode != aiv1alpha1.ProjectDeliveryModeDirect || view.Production.Mode != aiv1alpha1.ProjectDeliveryModeDirect {
		t.Fatalf("legacy effective delivery = %+v, want Direct everywhere", view)
	}
}

func TestEnableProjectGitOpsIsIdempotent(t *testing.T) {
	p := &aiv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "demo"},
		Spec:       aiv1alpha1.ProjectSpec{Repository: &aiv1alpha1.ProjectRepositoryBinding{RepositoryRef: "repo"}},
	}
	if err := enableProjectGitOps(p); err != nil {
		t.Fatal(err)
	}
	if err := enableProjectGitOps(p); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, environment := range p.Spec.Environments {
		for _, binding := range environment.Bindings {
			if binding.Name == projectGitOpsBindingName {
				count++
			}
		}
	}
	if count != 1 {
		t.Fatalf("GitOps bindings = %d, want one after repeated enable", count)
	}
}

func TestCreateProjectExplicitDirectKeepsWritableDevelopmentBinding(t *testing.T) {
	client := newProjectCreationTestClient(
		codeConnectionObjectWithValidated("github", metav1.ConditionTrue),
		applicationTemplateObject(),
	)
	created, err := (&Server{workspaces: workspace.NewFileStore(t.TempDir())}).createProjectFromRequest(
		context.Background(), client,
		identity{user: "alice", orgUUID: "org-a", workspaceUUID: "ws-1", tenantPath: "root:org-a:ws-1"},
		CreateProjectRequest{
			DisplayName: "Direct Demo", ConnectionRef: "github", TemplateName: "application",
			Delivery: testProjectDelivery(aiv1alpha1.ProjectDeliveryModeDirect, aiv1alpha1.ProjectDeliveryModeDirect),
		}, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if created.Spec.Delivery == nil || projectHasGitOps(created) {
		t.Fatalf("delivery = %+v, want Direct", created.Spec.Delivery)
	}
	if len(created.Spec.Environments) != 1 || len(created.Spec.Environments[0].Bindings) != 1 {
		t.Fatalf("environments = %+v, want development only", created.Spec.Environments)
	}
	binding := created.Spec.Environments[0].Bindings[0]
	if binding.Kind != aiv1alpha1.ProjectBindingKindProviderResource {
		t.Fatalf("development binding kind = %q, want providerResource", binding.Kind)
	}
	files, err := (&Server{workspaces: workspace.NewFileStore(t.TempDir())}).seedProjectScaffold(
		context.Background(), identity{orgUUID: "org-a", workspaceUUID: "ws-1"}, created, projectTemplateInfo{Name: "application"},
	)
	if err != nil || files != 0 {
		t.Fatalf("direct .faros scaffold = %d, %v, want none", files, err)
	}
}

func TestCreateProjectAdoptionDefaultsDirectAndRejectsGitOps(t *testing.T) {
	newClient := func() *asclient.Client {
		return newProjectCreationTestClient(codeRepositoryObject("existing", "existing-app", "github", true))
	}
	client := newClient()
	created, err := (&Server{}).createProjectFromRequest(
		context.Background(), client,
		identity{user: "alice", orgUUID: "org-a", workspaceUUID: "ws-1", tenantPath: "root:org-a:ws-1"},
		CreateProjectRequest{ExistingRepositoryRef: "existing"}, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if created.Spec.Delivery == nil || projectHasGitOps(created) {
		t.Fatalf("adopted delivery = %+v, want Direct", created.Spec.Delivery)
	}
	if len(created.Spec.Environments) != 1 || len(created.Spec.Environments[0].Bindings) != 0 {
		t.Fatalf("adopted environments = %+v, want no GitOps binding", created.Spec.Environments)
	}

	client = newClient()
	_, err = (&Server{}).createProjectFromRequest(
		context.Background(), client,
		identity{user: "alice", orgUUID: "org-a", workspaceUUID: "ws-1", tenantPath: "root:org-a:ws-1"},
		CreateProjectRequest{ExistingRepositoryRef: "existing", Delivery: testProjectDelivery(aiv1alpha1.ProjectDeliveryModeDirect, aiv1alpha1.ProjectDeliveryModeGitOps)}, nil, nil,
	)
	if err == nil || !strings.Contains(err.Error(), "bootstrap migration") {
		t.Fatalf("explicit adopted GitOps error = %v, want bootstrap migration rejection", err)
	}
	projects, listErr := client.Projects().List(context.Background(), metav1.ListOptions{})
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(projects.Items) != 0 {
		t.Fatalf("projects = %+v, want no side effect after rejected adoption", projects.Items)
	}
}

func TestWriteProjectErrorMapsPreflightOutageToRetryableBadGateway(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeProjectError(recorder, fmt.Errorf("%w: upstream returned 500", errProjectCreatePreflightUnavailable))
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusBadGateway, recorder.Body.String())
	}
	if recorder.Header().Get("Retry-After") != "2" || !strings.Contains(recorder.Body.String(), "temporarily unavailable") {
		t.Fatalf("response headers/body = %#v %s", recorder.Header(), recorder.Body.String())
	}
}

func TestCreateProjectPreflightTemplateCreatesBindingAndInstance(t *testing.T) {
	client := newProjectCreationTestClient(
		codeConnectionObjectWithValidated("github", metav1.ConditionTrue),
		applicationTemplateObject(),
	)
	preflight := &projectCreatePreflight{
		Naming:       projectNamingResult{DisplayName: "Customer Portal", RepositoryName: "customer-portal"},
		TemplateName: "application",
	}
	created, err := (&Server{actionsExternalURL: "https://actions.example.test", workspaces: workspace.NewFileStore(t.TempDir())}).createProjectFromRequestWithPreflight(
		context.Background(),
		client,
		identity{user: "alice", orgUUID: "org-a", workspaceUUID: "ws-1", tenantPath: "root:org-a:ws-1"},
		CreateProjectRequest{ConnectionRef: "github", InferDevelopmentTemplate: true},
		nil,
		nil,
		preflight,
	)
	if err != nil {
		t.Fatalf("createProjectFromRequestWithPreflight: %v", err)
	}
	if created.Spec.Template == nil || created.Spec.Template.Name != "application" {
		t.Fatalf("created template = %+v, want application", created.Spec.Template)
	}
	if created.Spec.Delivery == nil || projectDevelopmentDeliveryMode(created) != aiv1alpha1.ProjectDeliveryModeDirect || projectProductionDeliveryMode(created) != aiv1alpha1.ProjectDeliveryModeGitOps {
		t.Fatalf("created delivery = %+v, want Direct development and GitOps production", created.Spec.Delivery)
	}
	if len(created.Spec.Environments) != 2 || len(created.Spec.Environments[0].Bindings) != 1 || len(created.Spec.Environments[1].Bindings) != 1 {
		t.Fatalf("created environments = %+v, want development plus GitOps configuration", created.Spec.Environments)
	}
	binding := created.Spec.Environments[0].Bindings[0]
	if binding.Kind != aiv1alpha1.ProjectBindingKindProviderResource || binding.ResourceRef == nil || binding.ResourceRef.Name != created.Name+"-dev" {
		t.Fatalf("created binding = %+v, want %s-dev", binding, created.Name)
	}
	gitOpsBinding := created.Spec.Environments[1].Bindings[0]
	if gitOpsBinding.Provider != projectGitOpsProvider || gitOpsBinding.ResourceRef == nil || gitOpsBinding.ResourceRef.APIVersion != projectRepositorySyncAPIVersion {
		t.Fatalf("GitOps binding = %+v, want Deployments RepositorySync", gitOpsBinding)
	}
	// The Project reconciler owns both the direct development runtime and the
	// production RepositorySync without overlapping writers.
	want, gvr, err := bindings.Desired(created, created.Spec.Environments[1].Bindings[0])
	if err != nil {
		t.Fatalf("GitOps binding is not self-contained: %v", err)
	}
	if gvr.Resource != projectRepositorySyncResource || gvr.Group != "deployments.faros.sh" {
		t.Fatalf("binding GVR = %v, want repositorysyncs.deployments.faros.sh", gvr)
	}
	if want.GetName() != created.Name+"-gitops" {
		t.Fatalf("desired sync name = %q, want %s-gitops", want.GetName(), created.Name)
	}
}

func TestCreateDefaultHybridProjectSeedsStarterSourceWithoutDevelopmentInventory(t *testing.T) {
	scaffoldServer := giteaStyleArchive(t, map[string]string{"web/index.html": "<h1>ready</h1>"})
	defer scaffoldServer.Close()
	template := applicationTemplateObject()
	if err := unstructured.SetNestedMap(template.Object, map[string]any{
		"repository": scaffoldServer.URL + "/team/starter",
		"ref":        "main",
	}, "spec", "development", "scaffold"); err != nil {
		t.Fatal(err)
	}
	client := newProjectCreationTestClient(
		codeConnectionObjectWithValidated("github", metav1.ConditionTrue),
		template,
	)
	files := workspace.NewFileStore(t.TempDir())
	id := identity{user: "alice", orgUUID: "org-a", workspaceUUID: "ws-1", tenantPath: "root:org-a:ws-1"}
	created, err := (&Server{workspaces: files}).createProjectFromRequestWithPreflight(
		context.Background(), client, id,
		CreateProjectRequest{DisplayName: "Demo", ConnectionRef: "github", TemplateName: "application"}, nil, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	scope := projectWorkspaceScope(id, created)
	listed, err := files.ListFiles(context.Background(), scope, workspace.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"web/index.html": true}
	for _, file := range listed.Files {
		delete(want, file.Path)
	}
	if len(want) != 0 {
		t.Fatalf("initial workspace is missing %v; files = %+v", want, listed.Files)
	}
	dirty, err := files.UncommittedPaths(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(dirty) != 1 || dirty[0] != "web/index.html" {
		t.Fatalf("initial dirty paths = %v, want starter source only and no development .faros files", dirty)
	}
}

func TestCreateExplicitGitOpsDevelopmentUsesReferenceAndTrackedInventory(t *testing.T) {
	client := newProjectCreationTestClient(
		codeConnectionObjectWithValidated("github", metav1.ConditionTrue),
		applicationTemplateObject(),
	)
	files := workspace.NewFileStore(t.TempDir())
	id := identity{user: "alice", orgUUID: "org-a", workspaceUUID: "ws-1", tenantPath: "root:org-a:ws-1"}
	created, err := (&Server{workspaces: files}).createProjectFromRequestWithPreflight(
		context.Background(), client, id,
		CreateProjectRequest{
			DisplayName: "GitOps Demo", ConnectionRef: "github", TemplateName: "application",
			Delivery: testProjectDelivery(aiv1alpha1.ProjectDeliveryModeGitOps, aiv1alpha1.ProjectDeliveryModeGitOps),
		}, nil, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	binding := created.Spec.Environments[0].Bindings[0]
	if binding.Kind != aiv1alpha1.ProjectBindingKindProviderReference || binding.ResourceRef == nil || binding.ResourceRef.Name != created.Name+"-dev" {
		t.Fatalf("development binding = %+v, want Git-owned reference", binding)
	}
	listed, err := files.ListFiles(context.Background(), projectWorkspaceScope(id, created), workspace.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		".faros/environments/development/instance.yaml": true,
	}
	for _, file := range listed.Files {
		delete(want, file.Path)
	}
	if len(want) != 0 {
		t.Fatalf("explicit GitOps development is missing inventory %v; files = %+v", want, listed.Files)
	}
}

func TestCreateDefaultHybridToleratesUnavailableDevelopmentScaffold(t *testing.T) {
	scaffoldServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer scaffoldServer.Close()
	template := applicationTemplateObject()
	if err := unstructured.SetNestedMap(template.Object, map[string]any{
		"repository": scaffoldServer.URL + "/team/starter",
		"ref":        "main",
	}, "spec", "development", "scaffold"); err != nil {
		t.Fatal(err)
	}
	client := newProjectCreationTestClient(
		codeConnectionObjectWithValidated("github", metav1.ConditionTrue),
		template,
	)
	created, err := (&Server{workspaces: workspace.NewFileStore(t.TempDir())}).createProjectFromRequestWithPreflight(
		context.Background(), client,
		identity{user: "alice", orgUUID: "org-a", workspaceUUID: "ws-1", tenantPath: "root:org-a:ws-1"},
		CreateProjectRequest{DisplayName: "Demo", ConnectionRef: "github", TemplateName: "application"}, nil, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if projectDevelopmentIsGitManaged(created) || !projectProductionIsGitManaged(created) {
		t.Fatalf("delivery = %+v, want Direct development and GitOps production", created.Spec.Delivery)
	}
	projects, listErr := client.Projects().List(context.Background(), metav1.ListOptions{})
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(projects.Items) != 1 {
		t.Fatalf("projects = %+v, want hybrid project to survive optional development scaffold failure", projects.Items)
	}
}

func TestCreateExplicitGitOpsDevelopmentFailsIfEnvironmentScaffoldCannotBePersisted(t *testing.T) {
	client := newProjectCreationTestClient(
		codeConnectionObjectWithValidated("github", metav1.ConditionTrue),
		applicationTemplateObject(),
	)
	_, err := (&Server{}).createProjectFromRequestWithPreflight(
		context.Background(), client,
		identity{user: "alice", orgUUID: "org-a", workspaceUUID: "ws-1", tenantPath: "root:org-a:ws-1"},
		CreateProjectRequest{DisplayName: "Demo", ConnectionRef: "github", TemplateName: "application", Delivery: testProjectDelivery(aiv1alpha1.ProjectDeliveryModeGitOps, aiv1alpha1.ProjectDeliveryModeGitOps)}, nil, nil, nil,
	)
	if err == nil || !strings.Contains(err.Error(), "workspace store is required") {
		t.Fatalf("create error = %v, want truthful GitOps scaffold failure", err)
	}
	projects, listErr := client.Projects().List(context.Background(), metav1.ListOptions{})
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(projects.Items) != 0 {
		t.Fatalf("projects = %+v, want cleanup after GitOps scaffold failure", projects.Items)
	}
}

func TestCreateProjectLivePathListsCatalogCallsPreflightOnceAndCreatesInstance(t *testing.T) {
	dynamicClient := newProjectCreationTestDynamicClient(
		codeConnectionObjectWithValidated("github", metav1.ConditionTrue),
		applicationTemplateObject(),
	)
	dynamicClient.PrependReactor("create", "projects", func(action k8stesting.Action) (bool, runtime.Object, error) {
		object := action.(k8stesting.CreateAction).GetObject()
		object.(metav1.Object).SetUID("project-uid-customer-portal")
		return false, nil, nil
	})
	client := asclient.NewFromDynamic(dynamicClient)
	calls := 0
	server := &Server{
		actionsExternalURL: "https://actions.example.test",
		store:              store.NewMemoryStore(),
		workspaces:         workspace.NewFileStore(t.TempDir()),
		projectCreatePreflight: func(_ context.Context, _ *asclient.Client, prompt string, templates []projectDevelopmentTemplateView) (projectCreatePreflight, error) {
			calls++
			if prompt != "Build a frontend and backend customer portal." {
				t.Fatalf("preflight prompt = %q", prompt)
			}
			if len(templates) != 1 || templates[0].Name != "application" {
				t.Fatalf("preflight templates = %+v, want live application catalog entry", templates)
			}
			return projectCreatePreflight{
				Naming:       projectNamingResult{DisplayName: "Customer Portal", RepositoryName: "customer-portal"},
				TemplateName: "application",
			}, nil
		},
	}
	created, err := server.createProjectFromRequest(
		context.Background(),
		client,
		identity{user: "alice", orgUUID: "org-a", workspaceUUID: "ws-1", tenantPath: "root:org-a:ws-1"},
		CreateProjectRequest{
			Prompt:                   "Build a frontend and backend customer portal.",
			ConnectionRef:            "github",
			InferDevelopmentTemplate: true,
		},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("createProjectFromRequest: %v", err)
	}
	if calls != 1 {
		t.Fatalf("preflight calls = %d, want exactly one", calls)
	}
	if created.Spec.Template == nil || created.Spec.Template.Name != "application" {
		t.Fatalf("created template = %+v, want application", created.Spec.Template)
	}
	// Instances are materialized by the Project reconciler, not the handler:
	// assert the spec-only contract (a self-contained development binding).
	if len(created.Spec.Environments) != 2 || len(created.Spec.Environments[0].Bindings) != 1 {
		t.Fatalf("created environments = %+v, want development plus GitOps configuration", created.Spec.Environments)
	}
	binding := created.Spec.Environments[0].Bindings[0]
	if binding.Kind != aiv1alpha1.ProjectBindingKindProviderResource || binding.ResourceRef == nil || binding.ResourceRef.Name != created.Name+"-dev" {
		t.Fatalf("development binding = %+v, want direct provider resource %s-dev", binding, created.Name)
	}
	want, gvr, err := bindings.Desired(created, created.Spec.Environments[0].Bindings[0])
	if err != nil {
		t.Fatalf("binding is not self-contained: %v", err)
	}
	if gvr.Resource != "instances" || want.GetName() != created.Name+"-dev" {
		t.Fatalf("desired instance = %s/%s, want instances/%s-dev", gvr.Resource, want.GetName(), created.Name)
	}
}

func TestCreateProjectLivePathSurfacesCatalogListErrorBeforePreflight(t *testing.T) {
	dynamicClient := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			asclient.ProjectGVR:     "ProjectList",
			templatesGVR:            "TemplateList",
			apiBindingsResource.GVR: "APIBindingList",
		},
		apiBindingObject("deployments", deploymentsAPIExportName, "Bound", deploymentsGitOpsClaims...),
		apiBindingObject("app-studio", appStudioAPIExportName, "Bound", appStudioGitOpsClaims...),
	)
	forbidden := apierrors.NewForbidden(
		schema.GroupResource{Group: templatesGVR.Group, Resource: templatesGVR.Resource},
		"",
		nil,
	)
	dynamicClient.PrependReactor("list", "templates", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, forbidden
	})
	calls := 0
	server := &Server{
		projectCreatePreflight: func(context.Context, *asclient.Client, string, []projectDevelopmentTemplateView) (projectCreatePreflight, error) {
			calls++
			return projectCreatePreflight{}, nil
		},
	}
	_, err := server.createProjectFromRequest(
		context.Background(),
		asclient.NewFromDynamic(dynamicClient),
		identity{user: "alice", orgUUID: "org-a", workspaceUUID: "ws-1", tenantPath: "root:org-a:ws-1"},
		CreateProjectRequest{
			Prompt:                   "Build a customer portal.",
			InferDevelopmentTemplate: true,
		},
		nil,
		nil,
	)
	if !apierrors.IsForbidden(err) {
		t.Fatalf("catalog list error = %v, want forbidden surfaced", err)
	}
	if calls != 0 {
		t.Fatalf("preflight calls = %d, want none when catalog listing fails", calls)
	}
	projects, listErr := asclient.NewFromDynamic(dynamicClient).Projects().List(context.Background(), metav1.ListOptions{})
	if listErr != nil {
		t.Fatalf("list projects: %v", listErr)
	}
	if len(projects.Items) != 0 {
		t.Fatalf("projects = %+v, want none after catalog failure", projects.Items)
	}
}

func TestCreateProjectInvalidInferredTemplateFallsBackUnbound(t *testing.T) {
	client := newProjectCreationTestClient(
		codeConnectionObjectWithValidated("github", metav1.ConditionTrue),
		applicationTemplateObject(),
	)
	preflight := &projectCreatePreflight{
		Naming:       projectNamingResult{DisplayName: "Customer Portal", RepositoryName: "customer-portal"},
		TemplateName: "invented-template",
	}
	created, err := (&Server{actionsExternalURL: "https://actions.example.test", workspaces: workspace.NewFileStore(t.TempDir())}).createProjectFromRequestWithPreflight(
		context.Background(),
		client,
		identity{user: "alice", orgUUID: "org-a", workspaceUUID: "ws-1", tenantPath: "root:org-a:ws-1"},
		CreateProjectRequest{ConnectionRef: "github", InferDevelopmentTemplate: true},
		nil,
		nil,
		preflight,
	)
	if err != nil {
		t.Fatalf("createProjectFromRequestWithPreflight: %v", err)
	}
	if created.Spec.Template != nil || len(created.Spec.Environments[0].Bindings) != 0 {
		t.Fatalf("created project = %+v, want safe unbound fallback", created.Spec)
	}
}

func TestCreateProjectPreflightTemplateRequiresExplicitInferenceAuthorization(t *testing.T) {
	client := newProjectCreationTestClient(
		codeConnectionObjectWithValidated("github", metav1.ConditionTrue),
		applicationTemplateObject(),
	)
	preflight := &projectCreatePreflight{
		Naming:       projectNamingResult{DisplayName: "Customer Portal", RepositoryName: "customer-portal"},
		TemplateName: "application",
	}
	created, err := (&Server{actionsExternalURL: "https://actions.example.test", workspaces: workspace.NewFileStore(t.TempDir())}).createProjectFromRequestWithPreflight(
		context.Background(),
		client,
		identity{user: "alice", orgUUID: "org-a", workspaceUUID: "ws-1", tenantPath: "root:org-a:ws-1"},
		CreateProjectRequest{ConnectionRef: "github"},
		nil,
		nil,
		preflight,
	)
	if err != nil {
		t.Fatalf("createProjectFromRequestWithPreflight: %v", err)
	}
	if created.Spec.Template != nil || len(created.Spec.Environments[0].Bindings) != 0 {
		t.Fatalf("created project = %+v, want no inferred template without explicit request authorization", created.Spec)
	}
}

func TestCreateProjectExplicitTemplateTakesPrecedenceOverPreflight(t *testing.T) {
	client := newProjectCreationTestClient(
		codeConnectionObjectWithValidated("github", metav1.ConditionTrue),
		applicationTemplateObject(),
	)
	preflight := &projectCreatePreflight{
		Naming:       projectNamingResult{DisplayName: "Customer Portal", RepositoryName: "customer-portal"},
		TemplateName: "invented-template",
	}
	created, err := (&Server{actionsExternalURL: "https://actions.example.test", workspaces: workspace.NewFileStore(t.TempDir())}).createProjectFromRequestWithPreflight(
		context.Background(),
		client,
		identity{user: "alice", orgUUID: "org-a", workspaceUUID: "ws-1", tenantPath: "root:org-a:ws-1"},
		CreateProjectRequest{
			ConnectionRef:            "github",
			TemplateName:             "application",
			InferDevelopmentTemplate: true,
		},
		nil,
		nil,
		preflight,
	)
	if err != nil {
		t.Fatalf("createProjectFromRequestWithPreflight: %v", err)
	}
	if created.Spec.Template == nil || created.Spec.Template.Name != "application" {
		t.Fatalf("created template = %+v, want explicit application template", created.Spec.Template)
	}
}

func TestCreateProjectExplicitTemplateFailsClosedBeforeCreation(t *testing.T) {
	client := newProjectCreationTestClient(
		codeConnectionObjectWithValidated("github", metav1.ConditionTrue),
		applicationTemplateObject(),
	)
	_, err := (&Server{}).createProjectFromRequest(
		context.Background(),
		client,
		identity{user: "alice", orgUUID: "org-a", workspaceUUID: "ws-1", tenantPath: "root:org-a:ws-1"},
		CreateProjectRequest{
			DisplayName:   "Customer Portal",
			TemplateName:  "invented-template",
			ConnectionRef: "github",
		},
		nil,
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), `development template "invented-template" was not found`) {
		t.Fatalf("error = %v, want explicit missing-template validation", err)
	}
	projects, listErr := client.Projects().List(context.Background(), metav1.ListOptions{})
	if listErr != nil {
		t.Fatalf("list projects: %v", listErr)
	}
	if len(projects.Items) != 0 {
		t.Fatalf("projects = %+v, want none after explicit validation failure", projects.Items)
	}
	repositories, listErr := client.Resource(codeRepositoryResource, "").List(context.Background(), metav1.ListOptions{})
	if listErr != nil {
		t.Fatalf("list repositories: %v", listErr)
	}
	if len(repositories.Items) != 0 {
		t.Fatalf("repositories = %+v, want none after explicit validation failure", repositories.Items)
	}
}

func TestResolveProjectCreateTemplateRequiresDevelopmentComponents(t *testing.T) {
	productionOnly := applicationTemplateObject()
	productionOnly.SetName("database")
	delete(productionOnly.Object["spec"].(map[string]any), "development")
	client := newProjectCreationTestClient(productionOnly)

	if _, err := resolveProjectCreateTemplate(context.Background(), client, "database", false); err == nil ||
		!strings.Contains(err.Error(), "declares no development components") {
		t.Fatalf("explicit production-only template error = %v, want development-component validation", err)
	}
	info, err := resolveProjectCreateTemplate(context.Background(), client, "database", true)
	if err != nil {
		t.Fatalf("inferred production-only template returned error: %v", err)
	}
	if info != nil {
		t.Fatalf("inferred production-only template = %+v, want safe unbound fallback", info)
	}
}

func TestResolveProjectCreateTemplateSurfacesOperationalErrors(t *testing.T) {
	dynamicClient := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{templatesGVR: "TemplateList"},
	)
	forbidden := apierrors.NewForbidden(
		schema.GroupResource{Group: templatesGVR.Group, Resource: templatesGVR.Resource},
		"application",
		nil,
	)
	dynamicClient.PrependReactor("get", "templates", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, forbidden
	})
	_, err := resolveProjectCreateTemplate(
		context.Background(),
		asclient.NewFromDynamic(dynamicClient),
		"application",
		true,
	)
	if !apierrors.IsForbidden(err) {
		t.Fatalf("inferred forbidden error = %v, want the operational error surfaced", err)
	}
}

func newProjectCreationTestClient(objects ...runtime.Object) *asclient.Client {
	return asclient.NewFromDynamic(newProjectCreationTestDynamicClient(objects...))
}

func newProjectCreationTestDynamicClient(objects ...runtime.Object) *fake.FakeDynamicClient {
	// Most project-creation tests exercise the Project lifecycle rather than
	// provider enablement. Seed the authoritative GitOps capability so those
	// tests model a tenant that has accepted the optional Deployments claims;
	// readiness-specific tests use their own client fixture to cover missing
	// and partially applied bindings.
	objects = append(objects,
		apiBindingObject("deployments", deploymentsAPIExportName, "Bound", deploymentsGitOpsClaims...),
		apiBindingObject("app-studio", appStudioAPIExportName, "Bound", appStudioGitOpsClaims...),
	)
	client := fake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			asclient.ProjectGVR: "ProjectList",
			templatesGVR:        "TemplateList",
			codeConnectionsGVR:  "ConnectionList",
			codeRepositoriesGVR: "RepositoryList",
			{
				Group: "infrastructure.faros.sh", Version: "v1alpha1", Resource: "instances",
			}: "InstanceList",
			apiBindingsResource.GVR: "APIBindingList",
		},
		objects...,
	)
	client.PrependReactor("create", "projects", func(action k8stesting.Action) (bool, runtime.Object, error) {
		object := action.(k8stesting.CreateAction).GetObject()
		if object.(metav1.Object).GetUID() == "" {
			object.(metav1.Object).SetUID("project-uid-test")
		}
		return false, nil, nil
	})
	return client
}

func TestGenerateProjectAssistantStreamWithStartUsesInitialCreationGrant(t *testing.T) {
	messages := store.NewMemoryStore()
	workspaces := workspace.NewFileStore(t.TempDir())
	server := NewWithWorkspace(nil, messages, workspaces, "", false)
	id := identity{orgUUID: "org-a", workspaceUUID: "ws-1", tenantPath: "root:org-a:ws-1"}
	project := &aiv1alpha1.Project{}
	project.Name = "demo"
	project.UID = "test-project-uid-demo"
	if err := appendProjectUserMessage(context.Background(), messages, testProjectMessageScope(id.orgUUID, id.workspaceUUID, project.Name), "Build a todo app"); err != nil {
		t.Fatalf("append user message: %v", err)
	}
	engine := &capturingProjectAssistantEngine{}
	server.assistantEngine = engine
	settings := projectLLMSettings{Provider: defaultProjectLLMProvider, BaseURL: defaultProjectLLMBaseURL, Model: "test-model", APIKey: "test-key"}
	client := asclient.NewFromDynamic(projectSettingsDynamicClient{secret: projectLLMSettingsSecret(settings)})
	start := &projectAssistantStreamStart{InitialApprovedPlan: ptrProjectAssistantApprovedPlan(projectAssistantInitialCreationPlan())}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	request = request.WithContext(context.WithValue(request.Context(), projectAssistantSupervisorRunContextKey{}, store.AssistantRun{
		ID: "run-initial", Mode: store.AssistantRunModeDefault, Status: store.AssistantRunStatusRunning,
	}))
	_, err := server.generateProjectAssistantStreamWithStart(request, id, client, project, projectAssistantStreamCallbacks{}, start)
	if err != nil {
		t.Fatalf("generateProjectAssistantStreamWithStart returned error: %v", err)
	}
	if engine.req.TurnPolicy.profile != projectAssistantTurnProfileImplementation {
		t.Fatalf("turn policy = %#v, want implementation", engine.req.TurnPolicy)
	}
	if engine.req.InitialApprovedPlan == nil {
		t.Fatal("initial stream request omitted the run-local creation grant")
	}
}

func TestReserveProjectExternalOperationRejectsActiveAssistant(t *testing.T) {
	messages := store.NewMemoryStore()
	server := NewWithWorkspace(nil, messages, workspace.NewFileStore(t.TempDir()), "", false)
	id := identity{orgUUID: "org-a", workspaceUUID: "ws-1", user: "alice"}
	project := &aiv1alpha1.Project{}
	project.Name = "demo"
	project.UID = "project-uid-demo"
	scope := projectMessageScope(id.orgUUID, id.workspaceUUID, project)
	started, err := server.startProjectAssistantRunDurablyWithMode(
		context.Background(),
		scope,
		id.user,
		"fix the app",
		"external-operation-gate",
		store.AssistantRunModeDefault,
		func(run store.AssistantRun, assistant store.Message, _ bool) error {
			_, attachErr := server.projectAssistantSupervisor().Attach(scope, run, assistant)
			return attachErr
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.projectAssistantSupervisor().Abort(scope, started.Run.ID) })

	recorder := httptest.NewRecorder()
	release, ok := server.reserveProjectExternalOperation(
		recorder,
		context.Background(),
		id,
		project,
		"loading the workspace from git",
	)
	if release != nil {
		release()
	}
	if ok || recorder.Code != http.StatusConflict {
		t.Fatalf("operation reservation = (%t, %d, %q), want conflict", ok, recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "active assistant run") {
		t.Fatalf("conflict body = %q, want active assistant run guidance", recorder.Body.String())
	}
}

type capturingProjectAssistantEngine struct {
	req projectAssistantRunRequest
}

func (e *capturingProjectAssistantEngine) StreamProjectAssistant(_ context.Context, req projectAssistantRunRequest) (projectAssistantRunResult, error) {
	e.req = req
	return projectAssistantRunResult{Content: "done"}, nil
}

func (*capturingProjectAssistantEngine) ResumeProjectAssistant(context.Context, projectAssistantRunRequest, projectAssistantResumeRequest, projectAssistantCheckpointState) (projectAssistantRunResult, error) {
	return projectAssistantRunResult{}, nil
}

func TestAppendUniqueProjectMemoryEntries(t *testing.T) {
	got := appendUniqueProjectMemoryEntries(
		[]string{"Keep the existing goal", "  Preserve spacing after trim  ", "Keep the existing goal"},
		[]string{"Preserve spacing after trim", "Add a verified preview", "", " Add a verified preview "},
	)
	want := []string{"Keep the existing goal", "Preserve spacing after trim", "Add a verified preview"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("appendUniqueProjectMemoryEntries() = %#v, want %#v", got, want)
	}
}
