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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gorilla/mux"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	asclient "github.com/faroshq/provider-app-studio/client"
)

func productionTemplateTestObject(name string) *unstructured.Unstructured {
	obj := applicationTemplateObject()
	obj.SetName(name)
	properties, _, _ := unstructured.NestedMap(obj.Object, "spec", "schema", "properties")
	properties["frontendImage"] = map[string]any{"type": "string", "description": "Computed by the platform from immutable package evidence."}
	properties["backendImage"] = map[string]any{"type": "string", "description": "Computed by the platform from immutable package evidence."}
	_ = unstructured.SetNestedMap(obj.Object, properties, "spec", "schema", "properties")
	return obj
}

func TestGetProjectPromotionUsesExactSelectedProductionTemplate(t *testing.T) {
	project := developmentServicesTestProject("shop", "project-uid")
	selected := productionTemplateTestObject("selected-production")
	decoy := productionTemplateTestObject("decoy-production")
	client := developmentServicesTestClient(project, selected, decoy)
	server := &Server{projectClientFor: func(identity) (*asclient.Client, error) { return client, nil }}
	request := httptest.NewRequest(http.MethodGet, "/api/projects/shop/promotion?templateName=selected-production", nil)
	request = mux.SetURLVars(request, map[string]string{"project": "shop"})
	setDevelopmentServicesTestIdentity(request)
	response := httptest.NewRecorder()
	server.getProjectPromotion(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var readiness projectPromotionReadinessResponse
	if err := json.Unmarshal(response.Body.Bytes(), &readiness); err != nil {
		t.Fatal(err)
	}
	if readiness.Template != "selected-production" || len(readiness.TargetComponents) != 2 {
		t.Fatalf("readiness = %+v, want exact selected target", readiness)
	}
	if readiness.Promotable {
		t.Fatal("project without build evidence reported promotable")
	}

	request = httptest.NewRequest(http.MethodGet, "/api/projects/shop/promotion", nil)
	request = mux.SetURLVars(request, map[string]string{"project": "shop"})
	setDevelopmentServicesTestIdentity(request)
	response = httptest.NewRecorder()
	server.getProjectPromotion(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("no-target status=%d body=%s", response.Code, response.Body.String())
	}
	readiness = projectPromotionReadinessResponse{}
	if err := json.Unmarshal(response.Body.Bytes(), &readiness); err != nil {
		t.Fatal(err)
	}
	if readiness.Template != "" || readiness.ProductionSchema != nil || readiness.Promotable {
		t.Fatalf("universal no-target readiness = %+v, want no inferred Template", readiness)
	}
}

func TestProductionTemplateViewsRequireDeclaredImageInputsAndExcludePlatformOwned(t *testing.T) {
	application := productionTemplateTestObject("application")
	_ = unstructured.SetNestedField(application.Object, "Application", "spec", "displayName")
	missingInput := applicationTemplateObject()
	missingInput.SetName("missing-image-schema")
	database := applicationTemplateObject()
	database.SetName("database")
	unstructured.RemoveNestedField(database.Object, "spec", "development")
	platformUniversal := productionTemplateTestObject("universal-coding-sandbox")
	platformUniversal.SetLabels(map[string]string{projectTemplatePlatformOwnedLabel: projectTemplatePlatformOwnedValue})
	worker := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "infrastructure.faros.sh/v1alpha1",
		"kind":       "Template",
		"metadata":   map[string]any{"name": "worker"},
		"spec": map[string]any{
			"schema": map[string]any{"type": "object", "properties": map[string]any{"image": map[string]any{"type": "string"}}},
		},
	}}

	views := productionTemplateViews([]unstructured.Unstructured{*platformUniversal, *database, *missingInput, *worker, *application})
	if len(views) != 2 || views[0].Name != "application" || views[1].Name != "worker" {
		t.Fatalf("production templates = %+v, want application and worker only", views)
	}
	wantApplication := []projectProductionTemplateComponentView{{Name: "backend", ImageInput: "backendImage"}, {Name: "frontend", ImageInput: "frontendImage"}}
	if !reflect.DeepEqual(views[0].Components, wantApplication) {
		t.Fatalf("application targets = %+v, want %+v", views[0].Components, wantApplication)
	}
	if !reflect.DeepEqual(views[1].Components, []projectProductionTemplateComponentView{{Name: "default", ImageInput: "image"}}) {
		t.Fatalf("worker targets = %+v", views[1].Components)
	}
}

func TestProjectPromotionTemplateNameNeverInfersDevelopmentTemplate(t *testing.T) {
	project := &aiv1alpha1.Project{Spec: aiv1alpha1.ProjectSpec{
		Template: &aiv1alpha1.ProjectTemplateSpec{Name: "development-only"},
	}}
	if got := projectPromotionTemplateName(project, ""); got != "" {
		t.Fatalf("unpromoted target = %q, want empty rather than development Template", got)
	}
	if got := projectPromotionTemplateName(project, " selected-production "); got != "selected-production" {
		t.Fatalf("explicit target = %q", got)
	}
	project.Spec.Environments = []aiv1alpha1.ProjectEnvironmentSpec{{
		Name: projectProductionEnvironmentName,
		Bindings: []aiv1alpha1.ProjectProviderBindingSpec{{
			Name:        projectProductionBindingName,
			Kind:        aiv1alpha1.ProjectBindingKindProviderResource,
			TemplateRef: &aiv1alpha1.ProjectTemplateSpec{Name: "existing-production"},
		}},
	}}
	if got := projectPromotionTemplateName(project, ""); got != "existing-production" {
		t.Fatalf("existing production target = %q", got)
	}
	if got := projectPromotionTemplateName(project, "new-production"); got != "new-production" {
		t.Fatalf("explicit target did not override existing binding: %q", got)
	}
}
