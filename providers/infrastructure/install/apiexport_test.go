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

package install

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"reflect"
	"testing"

	apisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
	apisv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha2"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"
)

const testInfrastructureGroup = "infrastructure.faros.sh"

func testResource(name, group, schema string, storage apisv1alpha2.ResourceSchemaStorage) apisv1alpha2.ResourceSchema {
	return apisv1alpha2.ResourceSchema{
		Name:    name,
		Group:   group,
		Schema:  schema,
		Storage: storage,
	}
}

func TestStorageEqualComparesVirtualAPIGroupValues(t *testing.T) {
	firstGroup := "cache.kcp.io"
	secondGroup := "cache.kcp.io"
	first := storageForResource(testInfrastructureGroup, "templates", "identity-hash")
	second := storageForResource(testInfrastructureGroup, "templates", "identity-hash")
	first.Virtual.Reference.APIGroup = &firstGroup
	second.Virtual.Reference.APIGroup = &secondGroup
	if first.Virtual.Reference.APIGroup == second.Virtual.Reference.APIGroup {
		t.Fatal("test requires separately allocated APIGroup pointers")
	}
	if !storageEqual(first, second) {
		t.Fatalf("storageEqual(%#v, %#v) = false for equal APIGroup values", first, second)
	}
	if !resourcesEqual(
		[]apisv1alpha2.ResourceSchema{testResource("templates", testInfrastructureGroup, "current", first)},
		[]apisv1alpha2.ResourceSchema{testResource("templates", testInfrastructureGroup, "current", second)},
	) {
		t.Fatal("resourcesEqual returned false for separately allocated equal APIGroup values")
	}

	second.Virtual.Reference.APIGroup = nil
	if storageEqual(first, second) {
		t.Fatal("storageEqual returned true for one nil APIGroup and one non-nil APIGroup")
	}
	first.Virtual.Reference.APIGroup = nil
	if !storageEqual(first, second) {
		t.Fatal("storageEqual returned false for two nil APIGroup values")
	}
}

func testAPIExport(t *testing.T, resources []apisv1alpha2.ResourceSchema) *unstructured.Unstructured {
	t.Helper()
	data, err := json.Marshal(resources)
	if err != nil {
		t.Fatalf("marshal APIExport resources: %v", err)
	}
	var raw []any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal APIExport resources: %v", err)
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apisv1alpha2.SchemeGroupVersion.String(),
		"kind":       "APIExport",
		"metadata": map[string]any{
			"name":            APIExportName,
			"resourceVersion": "1",
		},
		"spec": map[string]any{
			"resources": raw,
		},
	}}
}

func testAPIExportResources(t *testing.T, export *unstructured.Unstructured) []apisv1alpha2.ResourceSchema {
	t.Helper()
	raw, found, err := unstructured.NestedFieldNoCopy(export.Object, "spec", "resources")
	if err != nil {
		t.Fatalf("read APIExport resources: %v", err)
	}
	if !found || raw == nil {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal APIExport resources: %v", err)
	}
	var resources []apisv1alpha2.ResourceSchema
	if err := json.Unmarshal(data, &resources); err != nil {
		t.Fatalf("decode APIExport resources: %v", err)
	}
	return resources
}

func TestReconcileAPIExportResourcesRetriesConflict(t *testing.T) {
	foreign := testResource(
		"templates",
		"widgets.example.com",
		"v1alpha1.templates.widgets.example.com",
		storageForResource("widgets.example.com", "templates", ""),
	)
	desired := []apisv1alpha2.ResourceSchema{
		testResource(
			"templates",
			testInfrastructureGroup,
			"v1alpha1.templates.infrastructure.faros.sh",
			storageForResource(testInfrastructureGroup, "templates", "identity-hash"),
		),
		testResource(
			"instances",
			testInfrastructureGroup,
			"v1alpha1.instances.infrastructure.faros.sh",
			storageForResource(testInfrastructureGroup, "instances", "identity-hash"),
		),
	}
	client := fake.NewSimpleDynamicClient(runtime.NewScheme(), testAPIExport(t, []apisv1alpha2.ResourceSchema{
		testResource(
			"legacy",
			testInfrastructureGroup,
			"v1alpha1.legacy.infrastructure.faros.sh",
			storageForResource(testInfrastructureGroup, "legacy", ""),
		),
	}))

	var gets, updates int
	client.PrependReactor("get", "apiexports", func(clienttesting.Action) (bool, runtime.Object, error) {
		gets++
		return false, nil, nil
	})
	client.PrependReactor("update", "apiexports", func(_ clienttesting.Action) (bool, runtime.Object, error) {
		updates++
		if updates != 1 {
			return false, nil, nil
		}
		current, err := client.Tracker().Get(apiExportGVR, "", APIExportName)
		if err != nil {
			return true, nil, err
		}
		currentExport := current.(*unstructured.Unstructured).DeepCopy()
		foreignData, err := json.Marshal([]apisv1alpha2.ResourceSchema{foreign})
		if err != nil {
			return true, nil, err
		}
		var foreignRaw []any
		if err := json.Unmarshal(foreignData, &foreignRaw); err != nil {
			return true, nil, err
		}
		if err := unstructured.SetNestedField(currentExport.Object, foreignRaw, "spec", "resources"); err != nil {
			return true, nil, err
		}
		currentExport.SetResourceVersion("2")
		if err := client.Tracker().Update(apiExportGVR, currentExport, "", metav1.UpdateOptions{}); err != nil {
			return true, nil, err
		}
		return true, nil, apierrors.NewConflict(apiExportGVR.GroupResource(), APIExportName, errors.New("simulated conflict"))
	})

	if err := reconcileAPIExportResources(context.Background(), client, desired); err != nil {
		t.Fatalf("reconcile APIExport resources: %v", err)
	}
	if gets != 2 {
		t.Fatalf("APIExport GET count = %d, want 2 after one conflict", gets)
	}
	if updates != 2 {
		t.Fatalf("APIExport update count = %d, want 2 after one conflict", updates)
	}

	current, err := client.Resource(apiExportGVR).Get(context.Background(), APIExportName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get reconciled APIExport: %v", err)
	}
	got := testAPIExportResources(t, current)
	want := []apisv1alpha2.ResourceSchema{foreign, desired[0], desired[1]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reconciled resources after conflict = %#v, want %#v", got, want)
	}
}

func TestReconcileResourceEntriesPrunesLegacyAndPreservesForeign(t *testing.T) {
	foreign := testResource(
		"templates",
		"widgets.example.com",
		"v1alpha1.templates.widgets.example.com",
		storageForResource("widgets.example.com", "templates", ""),
	)
	desiredTemplates := testResource(
		"templates",
		testInfrastructureGroup,
		"v1alpha1.templates.infrastructure.faros.sh",
		storageForResource(testInfrastructureGroup, "templates", "cached-resource-hash"),
	)
	desiredInstances := testResource(
		"instances",
		testInfrastructureGroup,
		"v1alpha1.instances.infrastructure.faros.sh",
		storageForResource(testInfrastructureGroup, "instances", "cached-resource-hash"),
	)
	existing := []apisv1alpha2.ResourceSchema{
		testResource(
			"redis",
			testInfrastructureGroup,
			"v1alpha1.redis.infrastructure.faros.sh",
			storageForResource(testInfrastructureGroup, "redis", ""),
		),
		foreign,
		testResource(
			"templates",
			testInfrastructureGroup,
			"v1alpha1.templates.infrastructure.faros.sh-legacy",
			storageForResource(testInfrastructureGroup, "templates", ""),
		),
	}

	got := reconcileResourceEntries(existing, []apisv1alpha2.ResourceSchema{desiredTemplates, desiredInstances}, testInfrastructureGroup)
	want := []apisv1alpha2.ResourceSchema{foreign, desiredTemplates, desiredInstances}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reconciled APIExport resources = %#v, want %#v", got, want)
	}
}

func TestReconcileResourceEntriesIsIdempotent(t *testing.T) {
	desired := []apisv1alpha2.ResourceSchema{
		testResource(
			"templates",
			testInfrastructureGroup,
			"v1alpha1.templates.infrastructure.faros.sh",
			storageForResource(testInfrastructureGroup, "templates", "cached-resource-hash"),
		),
		testResource(
			"instances",
			testInfrastructureGroup,
			"v1alpha1.instances.infrastructure.faros.sh",
			storageForResource(testInfrastructureGroup, "instances", "cached-resource-hash"),
		),
	}
	existing := []apisv1alpha2.ResourceSchema{
		testResource("templates", testInfrastructureGroup, "old-templates", storageForResource(testInfrastructureGroup, "templates", "")),
		testResource("legacy", testInfrastructureGroup, "old-legacy", storageForResource(testInfrastructureGroup, "legacy", "")),
	}

	first := reconcileResourceEntries(existing, desired, testInfrastructureGroup)
	second := reconcileResourceEntries(first, desired, testInfrastructureGroup)
	if !reflect.DeepEqual(second, first) {
		t.Fatalf("second reconcile changed resources: first=%#v second=%#v", first, second)
	}
}

func TestReconcileResourceEntriesUsesCurrentSchemaMapping(t *testing.T) {
	currentTemplates := testResource(
		"templates",
		testInfrastructureGroup,
		"v1alpha1.templates.infrastructure.faros.sh-current",
		storageForResource(testInfrastructureGroup, "templates", "identity-hash"),
	)
	currentInstances := testResource(
		"instances",
		testInfrastructureGroup,
		"v1alpha1.instances.infrastructure.faros.sh-current",
		storageForResource(testInfrastructureGroup, "instances", "identity-hash"),
	)
	existing := []apisv1alpha2.ResourceSchema{
		testResource("templates", testInfrastructureGroup, "v1alpha1.templates.infrastructure.faros.sh-stale", storageForResource(testInfrastructureGroup, "templates", "old-hash")),
		testResource("instances", testInfrastructureGroup, "v1alpha1.instances.infrastructure.faros.sh-stale", storageForResource(testInfrastructureGroup, "instances", "")),
	}

	got := reconcileResourceEntries(existing, []apisv1alpha2.ResourceSchema{currentTemplates, currentInstances}, testInfrastructureGroup)
	if len(got) != 2 {
		t.Fatalf("reconciled resource count = %d, want 2", len(got))
	}
	if !reflect.DeepEqual(got[0], currentTemplates) || !reflect.DeepEqual(got[1], currentInstances) {
		t.Fatalf("reconciled schema mapping = %#v, want templates=%#v instances=%#v", got, currentTemplates, currentInstances)
	}
}

func TestEmbeddedCRDsMapToCurrentTenantResources(t *testing.T) {
	entries, err := fs.ReadDir(crdsFS, "crds")
	if err != nil {
		t.Fatalf("read embedded CRDs: %v", err)
	}

	got := make(map[string]apisv1alpha2.ResourceSchema, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		raw, err := fs.ReadFile(crdsFS, "crds/"+entry.Name())
		if err != nil {
			t.Fatalf("read embedded CRD %q: %v", entry.Name(), err)
		}
		var crd apiextensionsv1.CustomResourceDefinition
		if err := utilyaml.Unmarshal(raw, &crd); err != nil {
			t.Fatalf("parse embedded CRD %q: %v", entry.Name(), err)
		}
		schemaObj, err := apisv1alpha1.CRDToAPIResourceSchema(&crd, schemaPrefix(&crd))
		if err != nil {
			t.Fatalf("convert embedded CRD %q: %v", entry.Name(), err)
		}
		resource := resourceSchemaForCRD(&crd, schemaObj.Name, "identity-hash")
		if resource.Group != testInfrastructureGroup {
			t.Fatalf("embedded CRD %q has API group %q, want %q", entry.Name(), resource.Group, testInfrastructureGroup)
		}
		if resource.Schema != schemaObj.Name {
			t.Fatalf("resource %q maps to schema %q, want %q", resource.Name, resource.Schema, schemaObj.Name)
		}
		if _, exists := got[resource.Name]; exists {
			t.Fatalf("duplicate embedded resource %q", resource.Name)
		}
		got[resource.Name] = resource
	}

	if len(got) != 2 {
		t.Fatalf("embedded tenant resource count = %d, want 2 (templates and instances)", len(got))
	}
	for _, name := range []string{"templates", "instances"} {
		if _, exists := got[name]; !exists {
			t.Errorf("embedded tenant resources missing %q: %#v", name, got)
		}
	}
	if got["templates"].Storage.Virtual == nil || got["templates"].Storage.Virtual.IdentityHash != "identity-hash" {
		t.Fatalf("templates storage = %#v, want virtual identity-hash", got["templates"].Storage)
	}
	if got["instances"].Storage.CRD == nil {
		t.Fatalf("instances storage = %#v, want CRD", got["instances"].Storage)
	}
}

// schemaWithPointers builds a CRD whose OpenAPIV3Schema populates the
// pointer-typed fields (Default, *bool flags) that fmt %v renders as
// memory addresses. Each call allocates fresh, so two structurally
// identical results live at different addresses — the precise condition
// that made the old %v-based hash non-deterministic and leaked a new
// immutable APIResourceSchema on every reconcile (eventually OOM-ing etcd).
func schemaWithPointers() *apiextensionsv1.CustomResourceDefinition {
	preserve := true
	return &apiextensionsv1.CustomResourceDefinition{
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "infrastructure.faros.sh",
			Names: apiextensionsv1.CustomResourceDefinitionNames{Kind: "Template", Plural: "templates"},
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name: "v1alpha1",
				Schema: &apiextensionsv1.CustomResourceValidation{
					OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
						Type: "object",
						Properties: map[string]apiextensionsv1.JSONSchemaProps{
							"image": {
								Type:    "string",
								Default: &apiextensionsv1.JSON{Raw: []byte(`"registry.example/img:v1"`)},
							},
							"replicas": {
								Type:    "integer",
								Default: &apiextensionsv1.JSON{Raw: []byte(`1`)},
							},
						},
						XPreserveUnknownFields: &preserve,
					},
				},
			}},
		},
	}
}

// TestSchemaPrefixDeterministic locks the fix: identical schema content
// must hash to the same name regardless of allocation, even when the
// schema carries pointer fields. With the old fmt %v hash this failed
// because %v printed pointer addresses.
func TestSchemaPrefixDeterministic(t *testing.T) {
	a := schemaPrefix(schemaWithPointers())
	b := schemaPrefix(schemaWithPointers())
	if a != b {
		t.Fatalf("schemaPrefix must be deterministic for identical content; got %q vs %q", a, b)
	}
}

// TestSchemaPrefixSensitiveToContent ensures a real content change still
// produces a different name (so genuine schema updates get a new schema).
func TestSchemaPrefixSensitiveToContent(t *testing.T) {
	base := schemaWithPointers()
	changed := schemaWithPointers()
	changed.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["replicas"] =
		apiextensionsv1.JSONSchemaProps{
			Type:    "integer",
			Default: &apiextensionsv1.JSON{Raw: []byte(`3`)}, // 1 -> 3
		}
	if schemaPrefix(base) == schemaPrefix(changed) {
		t.Fatal("schemaPrefix must change when schema content changes")
	}
}
