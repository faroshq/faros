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
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	"github.com/faroshq/provider-app-studio/tenant"
)

func TestGitManagedPromotionCreatesChangeRequestWithoutDirectDeploymentWrites(t *testing.T) {
	base := metav1.Now().Time
	p := projectForPromoteWithRepository("shop", "repo-a")
	p.Spec.DisplayName = "Shop"
	if err := enableProjectGitOps(p); err != nil {
		t.Fatal(err)
	}
	client := newProjectBuildProvenanceClient(p,
		[]*unstructured.Unstructured{repositoryCommitForBuildTest("current", "repo-a", "repo-a", "Succeeded", "commit-current", base)},
		[]*unstructured.Unstructured{
			projectBuildPackageForTest("repo-a", "frontend", "ghcr.io/acme/shop/frontend", map[string]any{"digest": "sha256:front", "tags": []any{"sha-commit-current"}}),
			projectBuildPackageForTest("repo-a", "backend", "ghcr.io/acme/shop/backend", map[string]any{"digest": "sha256:back", "tags": []any{"sha-commit-current"}}),
		},
	)
	var commitArguments map[string]any
	mcp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var envelope struct {
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Params.Name != projectToolCodeCommitFiles {
			t.Fatalf("MCP tool = %q", envelope.Params.Name)
		}
		commitArguments = envelope.Params.Arguments
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"committed"}]}}`))
	}))
	defer mcp.Close()

	persisted, err := client.Projects().Get(context.Background(), p.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/", nil)
	promoted, response, err := (&Server{hubBase: mcp.URL}).promoteProject(
		context.Background(), client, identity{clusterID: "cluster-a", tenantPath: "root:tenant-a"}, persisted, request,
		map[string]any{"frontendPort": float64(8080), "backendPort": float64(3000)},
	)
	if err != nil {
		t.Fatalf("promoteProject: %v", err)
	}
	if response.GitOps == nil || response.GitOps.Phase != "PendingApproval" || response.GitOps.ChangeRequest == "" {
		t.Fatalf("GitOps response = %+v", response.GitOps)
	}
	if commitArguments["baseRef"] != projectGitOpsDefaultBranch || !strings.HasPrefix(commitArguments["branch"].(string), "faros/promote-") {
		t.Fatalf("commit arguments = %#v", commitArguments)
	}
	binding := findProjectProductionBinding(promoted)
	if binding == nil || binding.Kind != aiv1alpha1.ProjectBindingKindProviderReference || !projectIsDeploymentBinding(binding) {
		t.Fatalf("production binding = %+v, want read-only Deployment reference", binding)
	}
	changeRequest, err := client.Resource(tenant.Resource{GVR: projectChangeRequestGVR, Kind: projectChangeRequestKind, Plural: "ChangeRequests"}, "").Get(context.Background(), response.GitOps.ChangeRequest, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get ChangeRequest: %v", err)
	}
	if policy, _, _ := unstructured.NestedString(changeRequest.Object, "spec", "mergePolicy"); policy != "AfterApproval" {
		t.Fatalf("merge policy = %q", policy)
	}
	if _, err := client.Resource(tenant.Resource{GVR: projectReleaseGVR, Kind: projectReleaseKind, Plural: "Releases"}, "").Get(context.Background(), projectBindingValues(binding)["releaseRef"].(string), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("direct Release read error = %v, want NotFound", err)
	}
	deploymentGVR := schema.GroupVersionResource{Group: "deployments.faros.sh", Version: "v1alpha1", Resource: "deployments"}
	if _, err := client.Resource(tenant.Resource{GVR: deploymentGVR, Kind: projectDeploymentKind, Plural: "Deployments"}, "").Get(context.Background(), projectTemplateProdInstanceName(p), metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("direct Deployment read error = %v, want NotFound", err)
	}
}

func applicationTemplateForPromote() projectTemplateInfo {
	info := applicationTemplateInfo()
	info.APIVersion = "infrastructure.faros.sh/v1alpha1"
	info.Kind = "Instance"
	info.Resource = "instances"
	return info
}

func projectForPromote(name string) *aiv1alpha1.Project {
	return &aiv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       aiv1alpha1.ProjectSpec{Template: &aiv1alpha1.ProjectTemplateSpec{Name: "application"}},
	}
}

func projectForPromoteWithRepository(name, repositoryRef string) *aiv1alpha1.Project {
	p := projectForPromote(name)
	p.Spec.Repository = &aiv1alpha1.ProjectRepositoryBinding{RepositoryRef: repositoryRef}
	return p
}

func TestProjectTemplateProdBindingFillsImagesAndForcesMode(t *testing.T) {
	p := projectForPromote("shop")
	images := map[string]string{
		"frontendImage": "ghcr.io/acme/shop/frontend@sha256:aaa",
		"backendImage":  "ghcr.io/acme/shop/backend@sha256:bbb",
	}
	// User form values: production knobs, plus an attempt to override
	// platform-owned fields that must be ignored.
	values := map[string]any{
		"frontendPort":               float64(8080),
		"backendPort":                float64(3000),
		"name":                       "attacker-name",
		"farosMode":                  "development",
		"frontendImage":              "ghcr.io/evil/x@sha256:ccc",
		projectRedeployRevisionField: "attacker-revision",
	}
	binding, err := projectTemplateProdBinding(p, applicationTemplateForPromote(), images, values)
	if err != nil {
		t.Fatalf("projectTemplateProdBinding: %v", err)
	}
	if binding.Name != projectProductionBindingName || binding.Provider != projectDevelopmentProviderAppStudio {
		t.Fatalf("binding meta = %+v", binding)
	}
	if binding.ResourceRef == nil || binding.ResourceRef.Name != "shop-prod" || binding.ResourceRef.Resource != "instances" {
		t.Fatalf("resourceRef = %+v", binding.ResourceRef)
	}

	var vals map[string]any
	if err := json.Unmarshal(binding.Values.Raw, &vals); err != nil {
		t.Fatalf("decode values: %v", err)
	}
	if vals["name"] != "shop-prod" {
		t.Fatalf("name = %v, want shop-prod (platform-owned, user override ignored)", vals["name"])
	}
	if vals["farosMode"] != "production" {
		t.Fatalf("farosMode = %v, want production", vals["farosMode"])
	}
	if vals["frontendImage"] != "ghcr.io/acme/shop/frontend@sha256:aaa" {
		t.Fatalf("frontendImage = %v, want the built digest (user override ignored)", vals["frontendImage"])
	}
	if vals["backendImage"] != "ghcr.io/acme/shop/backend@sha256:bbb" {
		t.Fatalf("backendImage = %v", vals["backendImage"])
	}
	if revision, _ := vals[projectRedeployRevisionField].(string); revision == "" || revision == "attacker-revision" {
		t.Fatalf("%s = %q, want a non-empty platform revision that ignores the user value", projectRedeployRevisionField, revision)
	}
	// Non-reserved production knobs pass through.
	if vals["frontendPort"] != float64(8080) || vals["backendPort"] != float64(3000) {
		t.Fatalf("ports not preserved: %v / %v", vals["frontendPort"], vals["backendPort"])
	}
}

func TestProjectProductionInputValuesExcludePlatformAndImageOwnedFields(t *testing.T) {
	info := applicationTemplateForPromote()
	info.ProductionSchema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"access":       map[string]any{"type": "string"},
			"webImage":     map[string]any{"type": "string"},
			"farosCluster": map[string]any{"type": "string", "description": "Computed by the platform — do NOT set."},
			"expose": map[string]any{"type": "object", "properties": map[string]any{
				"hostnamePrefix": map[string]any{"type": "string"},
				// Keep the schema description neutral: fqdn is reserved by the
				// explicit nested platform-ownership map, not by prose matching.
				"fqdn": map[string]any{"type": "string", "description": "Public hostname"},
			}},
		},
	}
	values := projectProductionInputValues(info, map[string]string{"webImage": "web@sha256:built"}, map[string]any{
		"access":       "private",
		"webImage":     "web@sha256:attacker",
		"farosCluster": "attacker-cluster",
		"name":         "attacker-name",
		"expose":       map[string]any{"hostnamePrefix": "shop", "fqdn": "attacker.example"},
	})
	want := map[string]any{"access": "private", "expose": map[string]any{"hostnamePrefix": "shop"}}
	if !reflect.DeepEqual(values, want) {
		t.Fatalf("filtered production values = %#v, want %#v", values, want)
	}
}

func TestProjectTemplateProdBindingLocksHostnamePrefixAfterFirstDeploy(t *testing.T) {
	p := projectForPromote("shop")
	info := applicationTemplateForPromote()
	info.ProductionSchema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":          map[string]any{"type": "string"},
			"farosMode":     map[string]any{"type": "string"},
			"frontendImage": map[string]any{"type": "string"},
			"expose": map[string]any{"type": "object", "properties": map[string]any{
				"hostnamePrefix": map[string]any{"type": "string"},
				"fqdn":           map[string]any{"type": "string"},
			}},
		},
		"required": []any{"name"},
	}
	images := map[string]string{"frontendImage": "frontend@sha256:built"}

	first, err := projectTemplateProdBinding(p, info, images, map[string]any{
		"expose": map[string]any{"hostnamePrefix": "shop-live"},
	})
	if err != nil {
		t.Fatalf("initial hostname prefix: %v", err)
	}
	firstValues, err := aiv1alpha1BindingValues(first)
	if err != nil {
		t.Fatalf("decode initial binding: %v", err)
	}
	firstExpose, _ := firstValues["expose"].(map[string]any)
	if firstExpose["hostnamePrefix"] != "shop-live" {
		t.Fatalf("initial hostnamePrefix = %#v, want shop-live", firstExpose["hostnamePrefix"])
	}
	upsertProjectProductionBinding(p, first)

	unchanged, err := projectTemplateProdBinding(p, info, images, map[string]any{
		"expose": map[string]any{"hostnamePrefix": "shop-live"},
	})
	if err != nil {
		t.Fatalf("unchanged hostname prefix on re-promote: %v", err)
	}
	unchangedValues, err := aiv1alpha1BindingValues(unchanged)
	if err != nil {
		t.Fatalf("decode unchanged binding: %v", err)
	}
	unchangedExpose, _ := unchangedValues["expose"].(map[string]any)
	if unchangedExpose["hostnamePrefix"] != "shop-live" {
		t.Fatalf("unchanged hostnamePrefix = %#v, want shop-live", unchangedExpose["hostnamePrefix"])
	}

	_, err = projectTemplateProdBinding(p, info, images, map[string]any{
		"expose": map[string]any{"hostnamePrefix": "shop-new"},
	})
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || !strings.Contains(err.Error(), projectProductionHostnamePrefixPath) {
		t.Fatalf("mutated hostname prefix error = %v, want immutable validation naming %s", err, projectProductionHostnamePrefixPath)
	}
}

func TestProjectTemplateProdBindingDoesNotInjectUndeclaredHostnamePrefix(t *testing.T) {
	p := projectForPromote("worker")
	info := applicationTemplateForPromote()
	info.ProductionSchema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":                  map[string]any{"type": "string"},
			"farosMode":             map[string]any{"type": "string"},
			"farosRedeployRevision": map[string]any{"type": "string"},
			"frontendImage":         map[string]any{"type": "string"},
		},
		"required":             []any{"name"},
		"additionalProperties": false,
	}
	images := map[string]string{"frontendImage": "frontend@sha256:built"}

	first, err := projectTemplateProdBinding(p, info, images, nil)
	if err != nil {
		t.Fatalf("initial production binding without exposure: %v", err)
	}
	upsertProjectProductionBinding(p, first)

	repromoted, err := projectTemplateProdBinding(p, info, images, nil)
	if err != nil {
		t.Fatalf("re-promote without exposure: %v", err)
	}
	values, err := aiv1alpha1BindingValues(repromoted)
	if err != nil {
		t.Fatalf("decode re-promoted binding: %v", err)
	}
	if _, found := values["expose"]; found {
		t.Fatalf("re-promote injected undeclared expose object: %#v", values["expose"])
	}
}

func TestProjectProductionImmutableInputPathsAdvertiseDeclaredHostnamePrefixOnly(t *testing.T) {
	base := projectTemplateInfo{ImmutableProductionInputs: []string{"database.size"}}
	withoutExposure := projectProductionImmutableInputPaths(base)
	if !reflect.DeepEqual(withoutExposure, []string{"database.size"}) {
		t.Fatalf("immutable inputs without exposure = %#v, want only declared inputs", withoutExposure)
	}

	withExposure := base
	withExposure.ProductionSchema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"expose": map[string]any{"type": "object", "properties": map[string]any{
				"hostnamePrefix": map[string]any{"type": "string"},
			}},
		},
	}
	got := projectProductionImmutableInputPaths(withExposure)
	want := []string{"database.size", projectProductionHostnamePrefixPath}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("immutable inputs with exposure = %#v, want %#v", got, want)
	}
}

func TestProjectProductionInputValuesSanitizeObjectsInsideArrays(t *testing.T) {
	info := applicationTemplateForPromote()
	info.ProductionSchema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"routes": map[string]any{"type": "array", "items": map[string]any{
				"type": "object", "properties": map[string]any{
					"path": map[string]any{"type": "string"},
					"fqdn": map[string]any{"type": "string", "description": "Computed by the platform — do NOT set."},
				},
			}},
		},
	}
	values := projectProductionInputValues(info, nil, map[string]any{
		"routes": []any{map[string]any{"path": "/", "fqdn": "attacker.example"}},
	})
	want := map[string]any{"routes": []any{map[string]any{"path": "/"}}}
	if !reflect.DeepEqual(values, want) {
		t.Fatalf("filtered array values = %#v, want %#v", values, want)
	}
}

func TestProjectTemplateProdBindingRejectsInvalidSchemaValues(t *testing.T) {
	info := applicationTemplateForPromote()
	info.ProductionSchema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":          map[string]any{"type": "string"},
			"farosMode":     map[string]any{"type": "string"},
			"frontendImage": map[string]any{"type": "string"},
			"replicas":      map[string]any{"type": "integer", "minimum": float64(1)},
		},
		"required": []any{"name"},
	}
	_, err := projectTemplateProdBinding(projectForPromote("shop"), info, map[string]string{"frontendImage": "image@sha256:built"}, map[string]any{"replicas": 1.5})
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || !strings.Contains(err.Error(), "replicas") {
		t.Fatalf("invalid replicas error = %v, want ValidationError naming replicas", err)
	}
}

func TestProjectTemplateProdBindingPreservesAndEnforcesImmutableInputs(t *testing.T) {
	p := projectForPromote("shop")
	info := applicationTemplateForPromote()
	info.ImmutableProductionInputs = []string{"database.size"}
	info.ProductionSchema = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":          map[string]any{"type": "string"},
			"farosMode":     map[string]any{"type": "string"},
			"frontendImage": map[string]any{"type": "string"},
			"database": map[string]any{"type": "object", "properties": map[string]any{
				"size": map[string]any{"type": "string", "enum": []any{"small", "medium", "large"}, "default": "small"},
			}},
		},
		"required": []any{"name"},
	}
	upsertProjectProductionBinding(p, aiv1alpha1.ProjectProviderBindingSpec{
		Name:   projectProductionBindingName,
		Values: runtime.RawExtension{Raw: []byte(`{"database":{"size":"large"},"name":"shop-prod","farosMode":"production"}`)},
	})

	_, err := projectTemplateProdBinding(p, info, map[string]string{"frontendImage": "image@sha256:new"}, map[string]any{"database": map[string]any{"size": "medium"}})
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || !strings.Contains(err.Error(), "database.size") {
		t.Fatalf("immutable size error = %v", err)
	}

	binding, err := projectTemplateProdBinding(p, info, map[string]string{"frontendImage": "image@sha256:new"}, nil)
	if err != nil {
		t.Fatalf("omitted immutable size: %v", err)
	}
	values, err := aiv1alpha1BindingValues(binding)
	if err != nil {
		t.Fatal(err)
	}
	database, _ := values["database"].(map[string]any)
	if database["size"] != "large" {
		t.Fatalf("preserved database size = %#v, want large", database["size"])
	}
}

func TestProjectTemplateInfoCarriesProductionSchema(t *testing.T) {
	obj := applicationTemplateObject()
	obj.SetAnnotations(map[string]string{projectTemplateImmutableInputsAnnotation: " database.version, database.size "})
	obj.Object["spec"].(map[string]any)["schema"] = map[string]any{
		"type":       "object",
		"properties": map[string]any{"access": map[string]any{"type": "string", "default": "public"}},
	}
	info, err := projectTemplateInfoFromUnstructured(obj)
	if err != nil {
		t.Fatalf("projectTemplateInfoFromUnstructured: %v", err)
	}
	if got := info.ProductionSchema["type"]; got != "object" {
		t.Fatalf("production schema type = %#v, want object", got)
	}
	if want := []string{"database.size", "database.version"}; !reflect.DeepEqual(info.ImmutableProductionInputs, want) {
		t.Fatalf("immutable production inputs = %#v, want %#v", info.ImmutableProductionInputs, want)
	}
}

func TestProjectRequestedRedeployRevisionReadsPersistedProductionValues(t *testing.T) {
	binding := &aiv1alpha1.ProjectProviderBindingSpec{
		Values: runtime.RawExtension{Raw: []byte(`{"farosRedeployRevision":" rollout-42 "}`)},
	}
	if got := projectRequestedRedeployRevision(binding); got != "rollout-42" {
		t.Fatalf("requested revision = %q, want rollout-42", got)
	}
}

func TestProjectDeploymentBindingUsesNestedConfigurationAndRolloutID(t *testing.T) {
	p := projectForPromote("shop")
	info := applicationTemplateForPromote()
	images := map[string]string{
		"frontendImage": "ghcr.io/acme/shop/frontend@sha256:aaa",
		"backendImage":  "ghcr.io/acme/shop/backend@sha256:bbb",
	}
	binding, err := projectDeploymentProdBinding(p, info, "shop-release-abc", images, map[string]any{
		"frontendPort": 8080,
		"name":         "attacker",
		"farosMode":    "development",
	}, "rollout-42")
	if err != nil {
		t.Fatalf("projectDeploymentProdBinding: %v", err)
	}
	if binding.Provider != projectDeploymentProvider || binding.ResourceRef == nil ||
		binding.ResourceRef.APIVersion != projectDeploymentAPIVersion ||
		binding.ResourceRef.Kind != projectDeploymentKind ||
		binding.ResourceRef.Resource != projectDeploymentResource ||
		binding.ResourceRef.Name != "shop-prod" {
		t.Fatalf("deployment binding = %+v", binding)
	}
	values, err := aiv1alpha1BindingValues(binding)
	if err != nil {
		t.Fatal(err)
	}
	if values["releaseRef"] != "shop-release-abc" || values["className"] != projectDeploymentClassName || values["rolloutID"] != "rollout-42" {
		t.Fatalf("deployment values = %#v", values)
	}
	configuration, _ := values["configuration"].(map[string]any)
	if configuration["frontendPort"] != float64(8080) {
		t.Fatalf("configuration = %#v, want tenant production input", configuration)
	}
	for _, reserved := range []string{"name", "farosMode", "frontendImage", "backendImage", projectRedeployRevisionField} {
		if _, found := configuration[reserved]; found {
			t.Fatalf("configuration retained platform-owned %q: %#v", reserved, configuration)
		}
	}
	if got := projectProductionConfiguration(&binding); !reflect.DeepEqual(got, configuration) {
		t.Fatalf("production configuration = %#v, want %#v", got, configuration)
	}
	if got := projectRequestedRedeployRevision(&binding); got != "rollout-42" {
		t.Fatalf("requested rollout = %q, want rollout-42", got)
	}
}

func TestProjectReleaseForPromotionIsDeterministicAndArtifactOrdered(t *testing.T) {
	p := projectForPromoteWithRepository("shop", "repo-a")
	info := applicationTemplateForPromote()
	firstName, first, err := projectReleaseForPromotion(p, info, "commit-abc", map[string]string{
		"frontend": "frontend@sha256:aaa",
		"backend":  "backend@sha256:bbb",
	})
	if err != nil {
		t.Fatal(err)
	}
	secondName, second, err := projectReleaseForPromotion(p, info, "commit-abc", map[string]string{
		"backend":  "backend@sha256:bbb",
		"frontend": "frontend@sha256:aaa",
	})
	if err != nil {
		t.Fatal(err)
	}
	if firstName != secondName || !reflect.DeepEqual(first, second) {
		t.Fatalf("release is not deterministic: %q %#v / %q %#v", firstName, first, secondName, second)
	}
	if first.Source.RepositoryRef != "repo-a" || first.Source.Revision != "commit-abc" || first.BlueprintRef.Name != "application" {
		t.Fatalf("release source/blueprint = %#v", first)
	}
	if len(first.Artifacts) != 2 || first.Artifacts[0].Name != "backend" || first.Artifacts[1].Name != "frontend" {
		t.Fatalf("release artifacts = %#v, want stable component-name ordering", first.Artifacts)
	}
	changedName, _, err := projectReleaseForPromotion(p, info, "commit-def", map[string]string{"frontend": "frontend@sha256:aaa"})
	if err != nil {
		t.Fatal(err)
	}
	if changedName == firstName {
		t.Fatalf("different release evidence reused name %q", firstName)
	}
}

func TestProjectTemplateProdBindingMintsDistinctRevisionsAndHonorsExplicitRevision(t *testing.T) {
	p := projectForPromote("shop")
	images := map[string]string{"frontendImage": "frontend@sha256:aaa"}

	first, err := projectTemplateProdBinding(p, applicationTemplateForPromote(), images, nil)
	if err != nil {
		t.Fatalf("first projectTemplateProdBinding: %v", err)
	}
	second, err := projectTemplateProdBinding(p, applicationTemplateForPromote(), images, nil)
	if err != nil {
		t.Fatalf("second projectTemplateProdBinding: %v", err)
	}
	firstValues, err := aiv1alpha1BindingValues(first)
	if err != nil {
		t.Fatal(err)
	}
	secondValues, err := aiv1alpha1BindingValues(second)
	if err != nil {
		t.Fatal(err)
	}
	firstRevision, _ := firstValues[projectRedeployRevisionField].(string)
	secondRevision, _ := secondValues[projectRedeployRevisionField].(string)
	if firstRevision == "" || secondRevision == "" {
		t.Fatalf("generated revisions = %q / %q, want both non-empty", firstRevision, secondRevision)
	}
	if firstRevision == secondRevision {
		t.Fatalf("generated revisions = %q / %q, want distinct revisions", firstRevision, secondRevision)
	}

	const explicitRevision = "rollout-explicit-42"
	explicit, err := projectTemplateProdBinding(p, applicationTemplateForPromote(), images,
		map[string]any{projectRedeployRevisionField: "user-value"}, explicitRevision)
	if err != nil {
		t.Fatalf("explicit projectTemplateProdBinding: %v", err)
	}
	explicitValues, err := aiv1alpha1BindingValues(explicit)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := explicitValues[projectRedeployRevisionField].(string); got != explicitRevision {
		t.Fatalf("explicit %s = %q, want %q", projectRedeployRevisionField, got, explicitRevision)
	}
}

func TestProjectPromoteResponseIncludesRolloutRevision(t *testing.T) {
	const revision = "rollout-response-42"
	raw, err := json.Marshal(projectPromoteResponse{
		Environment:     projectProductionEnvironmentName,
		Instance:        "shop-prod",
		RolloutRevision: revision,
	})
	if err != nil {
		t.Fatalf("marshal projectPromoteResponse: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode projectPromoteResponse: %v", err)
	}
	if got, _ := decoded["rolloutRevision"].(string); got != revision {
		t.Fatalf("rolloutRevision = %q, want %q", got, revision)
	}
}

func TestProjectObservedRedeployRevisionReadsProviderInstanceSpec(t *testing.T) {
	instance := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{"values": map[string]any{projectRedeployRevisionField: " rollout-observed-42 "}},
	}}
	if got := projectObservedRedeployRevision(instance); got != "rollout-observed-42" {
		t.Fatalf("observed rollout revision = %q, want rollout-observed-42", got)
	}
	if got := projectObservedRedeployRevision(nil); got != "" {
		t.Fatalf("nil instance revision = %q, want empty", got)
	}
	deployment := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": projectDeploymentAPIVersion,
		"kind":       projectDeploymentKind,
		"status":     map[string]any{"observedRolloutID": " deployment-rollout-42 "},
	}}
	if got := projectObservedRedeployRevision(deployment); got != "deployment-rollout-42" {
		t.Fatalf("deployment observed rollout = %q, want deployment-rollout-42", got)
	}
}

func TestGitOpsProductionReadUsesLiveDeploymentInsteadOfProjectSnapshot(t *testing.T) {
	instance := &unstructured.Unstructured{Object: map[string]any{"spec": map[string]any{
		"configuration": map[string]any{"replicas": int64(4)},
		"rolloutID":     "merged-config-sha",
	}}}
	configuration, rolloutID := projectGitOpsObservedProduction(instance)
	if !reflect.DeepEqual(configuration, map[string]any{"replicas": int64(4)}) || rolloutID != "merged-config-sha" {
		t.Fatalf("observed configuration = %#v rollout = %q", configuration, rolloutID)
	}
}

func TestProjectBuildAndPromotionRequireExactReviewedCommitImages(t *testing.T) {
	base := metav1.Now().Time
	tests := []struct {
		name          string
		commits       []*unstructured.Unstructured
		packages      []*unstructured.Unstructured
		wantBuild     string
		wantPromotErr bool
	}{
		{
			name: "no successful commit",
			commits: []*unstructured.Unstructured{
				repositoryCommitForBuildTest("failed", "repo-a", "repo-a", "Failed", "commit-failed", base),
			},
			wantBuild:     "none",
			wantPromotErr: true,
		},
		{
			name: "newest successful empty SHA",
			commits: []*unstructured.Unstructured{
				repositoryCommitForBuildTest("older", "repo-a", "repo-a", "Succeeded", "commit-old", base.Add(-time.Hour)),
				repositoryCommitForBuildTest("newest-empty", "repo-a", "repo-a", "Succeeded", "", base),
			},
			packages: []*unstructured.Unstructured{
				projectBuildPackageForTest("repo-a", "frontend", "ghcr.io/acme/shop/frontend", map[string]any{"digest": "sha256:old-front", "tags": []any{"sha-commit-old"}}),
				projectBuildPackageForTest("repo-a", "backend", "ghcr.io/acme/shop/backend", map[string]any{"digest": "sha256:old-back", "tags": []any{"sha-commit-old"}}),
			},
			wantBuild:     "none",
			wantPromotErr: true,
		},
		{
			name: "missing exact component tag",
			commits: []*unstructured.Unstructured{
				repositoryCommitForBuildTest("current", "repo-a", "repo-a", "Succeeded", "commit-current", base),
			},
			packages: []*unstructured.Unstructured{
				projectBuildPackageForTest("repo-a", "frontend", "ghcr.io/acme/shop/frontend", map[string]any{"digest": "sha256:front", "tags": []any{"latest", "sha-commit-current"}}),
				projectBuildPackageForTest("repo-a", "backend", "ghcr.io/acme/shop/backend", map[string]any{"digest": "sha256:other", "tags": []any{"latest", "sha-other"}}),
			},
			wantBuild:     "incomplete",
			wantPromotErr: true,
		},
		{
			name: "complete exact component tags",
			commits: []*unstructured.Unstructured{
				repositoryCommitForBuildTest("current", "repo-a", "repo-a", "Succeeded", "commit-current", base),
			},
			packages: []*unstructured.Unstructured{
				projectBuildPackageForTest("repo-a", "frontend", "ghcr.io/acme/shop/frontend", map[string]any{"digest": "sha256:front", "tags": []any{"latest", "sha-commit-current"}}),
				projectBuildPackageForTest("repo-a", "backend", "ghcr.io/acme/shop/backend", map[string]any{"digest": "sha256:back", "tags": []any{"latest", "sha-commit-current"}}),
			},
			wantBuild:     "built",
			wantPromotErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			project := projectForPromoteWithRepository("shop", "repo-a")
			client := newProjectBuildProvenanceClient(project, tc.commits, tc.packages)
			persisted, err := client.Projects().Get(context.Background(), project.Name, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("get test project: %v", err)
			}
			check, err := (&Server{}).checkProjectBuild(context.Background(), client, identity{}, persisted)
			if err != nil {
				t.Fatalf("checkProjectBuild: %v", err)
			}
			if check.Status != tc.wantBuild {
				t.Fatalf("build status = %q, want %q (check=%+v)", check.Status, tc.wantBuild, check)
			}

			promoted, response, promoteErr := (&Server{}).promoteProject(context.Background(), client, identity{}, persisted, nil, nil)
			if tc.wantPromotErr {
				if promoteErr == nil || !strings.Contains(promoteErr.Error(), "not ready to promote") {
					t.Fatalf("promoteProject error = %v, want not-ready validation error", promoteErr)
				}
			} else if promoteErr != nil {
				t.Fatalf("promoteProject returned error for complete exact tags: %v", promoteErr)
			} else {
				binding := findProjectProductionBinding(promoted)
				if !projectIsDeploymentBinding(binding) {
					t.Fatalf("promotion binding = %+v, want deployment provider", binding)
				}
				values := projectBindingValues(binding)
				releaseName, _ := values["releaseRef"].(string)
				if releaseName == "" || values["rolloutID"] != response.RolloutRevision {
					t.Fatalf("promotion values = %#v, response = %+v", values, response)
				}
				release, getErr := client.Resource(tenant.Resource{GVR: projectReleaseGVR, Kind: projectReleaseKind, Plural: "Releases"}, "").Get(context.Background(), releaseName, metav1.GetOptions{})
				if getErr != nil {
					t.Fatalf("get immutable release: %v", getErr)
				}
				if revision, _, _ := unstructured.NestedString(release.Object, "spec", "source", "revision"); revision != "commit-current" {
					t.Fatalf("release revision = %q, want commit-current", revision)
				}
				artifacts, _, _ := unstructured.NestedSlice(release.Object, "spec", "artifacts")
				if len(artifacts) != 2 {
					t.Fatalf("release artifacts = %#v, want exact two-component evidence", artifacts)
				}

				// Re-promoting the same exact evidence reuses the immutable Release
				// while minting a fresh rollout identity.
				again, secondResponse, secondErr := (&Server{}).promoteProject(context.Background(), client, identity{}, promoted, nil, nil)
				if secondErr != nil {
					t.Fatalf("re-promote exact release: %v", secondErr)
				}
				secondValues := projectBindingValues(findProjectProductionBinding(again))
				if secondValues["releaseRef"] != releaseName {
					t.Fatalf("re-promote releaseRef = %#v, want %q", secondValues["releaseRef"], releaseName)
				}
				if secondResponse.RolloutRevision == response.RolloutRevision {
					t.Fatalf("re-promote rolloutID reused %q", response.RolloutRevision)
				}
			}
		})
	}
}

func TestUpsertProjectProductionBindingAddsThenReplaces(t *testing.T) {
	p := projectForPromote("shop")
	// A pre-existing development environment must be left untouched.
	p.Spec.Environments = []aiv1alpha1.ProjectEnvironmentSpec{{
		Name:     projectDevelopmentEnvironmentName,
		Mode:     aiv1alpha1.ProjectEnvironmentModeLive,
		Bindings: []aiv1alpha1.ProjectProviderBindingSpec{{Name: projectDevelopmentBindingName}},
	}}

	first := aiv1alpha1.ProjectProviderBindingSpec{Name: projectProductionBindingName, Values: rawJSON(t, map[string]any{"v": 1})}
	upsertProjectProductionBinding(p, first)
	if len(p.Spec.Environments) != 2 {
		t.Fatalf("environments = %d, want 2 (dev + prod)", len(p.Spec.Environments))
	}
	prod := findEnv(t, p, projectProductionEnvironmentName)
	if prod.Mode != aiv1alpha1.ProjectEnvironmentModeArtifact {
		t.Fatalf("prod mode = %q, want artifact", prod.Mode)
	}
	if len(prod.Bindings) != 1 {
		t.Fatalf("prod bindings = %d, want 1", len(prod.Bindings))
	}

	// Re-promote replaces the binding rather than appending a duplicate.
	second := aiv1alpha1.ProjectProviderBindingSpec{Name: projectProductionBindingName, Values: rawJSON(t, map[string]any{"v": 2})}
	upsertProjectProductionBinding(p, second)
	prod = findEnv(t, p, projectProductionEnvironmentName)
	if len(prod.Bindings) != 1 {
		t.Fatalf("prod bindings after re-promote = %d, want 1 (replaced)", len(prod.Bindings))
	}
	if string(prod.Bindings[0].Values.Raw) != `{"v":2}` {
		t.Fatalf("prod binding not replaced: %s", prod.Bindings[0].Values.Raw)
	}
	// Dev environment survived.
	dev := findEnv(t, p, projectDevelopmentEnvironmentName)
	if len(dev.Bindings) != 1 || dev.Bindings[0].Name != projectDevelopmentBindingName {
		t.Fatalf("dev environment disturbed: %+v", dev)
	}
}

func TestUpsertProjectProductionBindingReplacesGitManagedReference(t *testing.T) {
	p := projectForPromoteWithRepository("shop", "repo-a")
	if err := enableProjectGitOps(p); err != nil {
		t.Fatal(err)
	}
	first, err := projectGitOpsProductionBinding(p, map[string]any{"releaseRef": "release-a", "configuration": map[string]any{"replicas": 1}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := projectGitOpsProductionBinding(p, map[string]any{"releaseRef": "release-b", "configuration": map[string]any{"replicas": 2}})
	if err != nil {
		t.Fatal(err)
	}
	upsertProjectProductionBinding(p, first)
	upsertProjectProductionBinding(p, second)
	production := findEnv(t, p, projectProductionEnvironmentName)
	if len(production.Bindings) != 1 {
		t.Fatalf("production bindings = %+v, want one current Git reference", production.Bindings)
	}
	if values := projectBindingValues(&production.Bindings[0]); values["releaseRef"] != "release-b" {
		t.Fatalf("production snapshot = %#v, want release-b", values)
	}
}

func rawJSON(t *testing.T, v any) runtime.RawExtension {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return runtime.RawExtension{Raw: b}
}

func aiv1alpha1BindingValues(binding aiv1alpha1.ProjectProviderBindingSpec) (map[string]any, error) {
	values := map[string]any{}
	if err := json.Unmarshal(binding.Values.Raw, &values); err != nil {
		return nil, err
	}
	return values, nil
}

func findEnv(t *testing.T, p *aiv1alpha1.Project, name string) aiv1alpha1.ProjectEnvironmentSpec {
	t.Helper()
	for _, e := range p.Spec.Environments {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("environment %q not found", name)
	return aiv1alpha1.ProjectEnvironmentSpec{}
}
