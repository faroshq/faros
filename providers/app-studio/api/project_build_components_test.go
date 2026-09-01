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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
)

func applicationTemplateInfo() projectTemplateInfo {
	return projectTemplateInfo{
		Name:              "application",
		BuildWorkflowPath: ".github/workflows/build.yaml",
		Components: map[string]projectTemplateComponent{
			"frontend": {WorkspacePath: "web", ImageInput: "frontendImage"},
			"backend":  {WorkspacePath: "api", ImageInput: "backendImage"},
		},
	}
}

func TestProjectBuildComponentsSortedWithImageInput(t *testing.T) {
	got := projectBuildComponents(applicationTemplateInfo())
	if len(got) != 2 {
		t.Fatalf("components = %d, want 2", len(got))
	}
	if got[0].Name != "backend" || got[1].Name != "frontend" {
		t.Fatalf("component order = %q,%q, want backend,frontend", got[0].Name, got[1].Name)
	}
	if got[0].Context != "api" || got[0].ImageInput != "backendImage" {
		t.Fatalf("backend = %+v, want context api / backendImage", got[0])
	}
	if got[1].Context != "web" || got[1].ImageInput != "frontendImage" {
		t.Fatalf("frontend = %+v, want context web / frontendImage", got[1])
	}
}

func TestProjectBuildComponentsSkipsComponentsWithoutImageInput(t *testing.T) {
	info := projectTemplateInfo{
		Name: "worker-only",
		Components: map[string]projectTemplateComponent{
			"worker": {WorkspacePath: "."},
			"web":    {WorkspacePath: "web", ImageInput: "image"},
		},
	}
	got := projectBuildComponents(info)
	if len(got) != 1 || got[0].Name != "web" {
		t.Fatalf("components = %+v, want only web", got)
	}
}

func TestProjectBuildComponentsForUniversalProjectUsesStableSourceContract(t *testing.T) {
	p := &aiv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "shop"},
		Spec: aiv1alpha1.ProjectSpec{Components: []aiv1alpha1.ProjectComponentSpec{
			{
				Name:       "web",
				Kind:       aiv1alpha1.ProjectComponentKindService,
				SourcePath: "web",
				Build: &aiv1alpha1.ProjectComponentBuildSpec{
					ContextPath:    "web",
					DockerfilePath: "web/Dockerfile",
				},
			},
			{
				Name:       "worker",
				Kind:       aiv1alpha1.ProjectComponentKindWorker,
				SourcePath: ".",
			},
		}},
	}

	got := projectBuildComponentsForProject(p, projectTemplateInfo{})
	if len(got) != 2 {
		t.Fatalf("components = %+v, want two universal project components", got)
	}
	if got[0].Name != "web" || got[0].Context != "web" || got[0].ContextPath != "web" || got[0].DockerfilePath != "web/Dockerfile" || got[0].ImageInput != "webImage" || got[0].TemplateComponent != "web" {
		t.Fatalf("web build component = %+v, want stable web source/build paths and webImage", got[0])
	}
	if got[1].Name != "worker" || got[1].Context != "." || got[1].ContextPath != "." || got[1].DockerfilePath != "" || got[1].ImageInput != "workerImage" || got[1].TemplateComponent != "worker" {
		t.Fatalf("worker build component = %+v, want stable worker source and workerImage", got[1])
	}
}

func TestProjectBuildComponentsForProjectUsesDevelopmentMappingAndBuildContext(t *testing.T) {
	p := &aiv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "shop"},
		Spec: aiv1alpha1.ProjectSpec{
			Components: []aiv1alpha1.ProjectComponentSpec{{
				Name:       "api-service",
				Kind:       aiv1alpha1.ProjectComponentKindService,
				SourcePath: "services/api",
				Build: &aiv1alpha1.ProjectComponentBuildSpec{
					ContextPath:    "build/api",
					DockerfilePath: "build/api/Dockerfile",
				},
			}},
			Environments: []aiv1alpha1.ProjectEnvironmentSpec{{
				Name: projectDevelopmentEnvironmentName,
				Bindings: []aiv1alpha1.ProjectProviderBindingSpec{{
					Name: projectDevelopmentBindingName,
					Kind: aiv1alpha1.ProjectBindingKindProviderResource,
					ComponentMappings: []aiv1alpha1.ProjectComponentMappingSpec{{
						ComponentRef:    "api-service",
						TargetComponent: "backend",
					}},
				}},
			}},
		},
	}
	info := projectTemplateInfo{Components: map[string]projectTemplateComponent{
		"backend": {WorkspacePath: "api", ImageInput: "backendImage"},
	}}

	got := projectBuildComponentsForProject(p, info)
	if len(got) != 1 || got[0].Name != "api-service" || got[0].TemplateComponent != "backend" || got[0].Context != "build/api" || got[0].ContextPath != "build/api" || got[0].DockerfilePath != "build/api/Dockerfile" || got[0].ImageInput != "backendImage" {
		t.Fatalf("mapped build component = %+v, want api-service/backend/build/api/build/api/Dockerfile/backendImage", got)
	}
}
