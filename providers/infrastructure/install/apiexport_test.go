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
	"reflect"
	"testing"

	apisv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha2"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"

	infrav1alpha1 "github.com/faroshq/provider-infrastructure/apis/v1alpha1"
)

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

func TestReconcileOwnedResourcesPrunesLegacyInfrastructureEntries(t *testing.T) {
	otherGroup := apisv1alpha2.ResourceSchema{
		Name:   "widgets",
		Group:  "other.example.io",
		Schema: "v1.widgets.other.example.io",
		Storage: apisv1alpha2.ResourceSchemaStorage{
			CRD: &apisv1alpha2.ResourceSchemaStorageCRD{},
		},
	}
	legacyApplication := apisv1alpha2.ResourceSchema{
		Name:   "applications",
		Group:  infrav1alpha1.GroupName,
		Schema: "legacy.applications.infrastructure.faros.sh",
		Storage: apisv1alpha2.ResourceSchemaStorage{
			CRD: &apisv1alpha2.ResourceSchemaStorageCRD{},
		},
	}
	desired := []apisv1alpha2.ResourceSchema{
		{
			Name:   "instances",
			Group:  infrav1alpha1.GroupName,
			Schema: "stable.instances.infrastructure.faros.sh",
			Storage: apisv1alpha2.ResourceSchemaStorage{
				CRD: &apisv1alpha2.ResourceSchemaStorageCRD{},
			},
		},
		{
			Name:   "templates",
			Group:  infrav1alpha1.GroupName,
			Schema: "stable.templates.infrastructure.faros.sh",
			Storage: apisv1alpha2.ResourceSchemaStorage{
				Virtual: &apisv1alpha2.ResourceSchemaStorageVirtual{
					Reference: corev1.TypedLocalObjectReference{
						APIGroup: ptrTo("cache.kcp.io"),
						Kind:     "CachedResourceEndpointSlice",
						Name:     EndpointSliceTemplatesName,
					},
					IdentityHash: "templates-identity",
				},
			},
		},
	}

	got := reconcileOwnedResources([]apisv1alpha2.ResourceSchema{
		legacyApplication,
		otherGroup,
		{
			Name:   "templates",
			Group:  infrav1alpha1.GroupName,
			Schema: "stale.templates.infrastructure.faros.sh",
			Storage: apisv1alpha2.ResourceSchemaStorage{
				CRD: &apisv1alpha2.ResourceSchemaStorageCRD{},
			},
		},
	}, desired, infrav1alpha1.GroupName)

	want := append([]apisv1alpha2.ResourceSchema{otherGroup}, desired...)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("reconciled resources = %#v, want %#v", got, want)
	}
	for _, resource := range got {
		if resource.Group == infrav1alpha1.GroupName && resource.Name == legacyApplication.Name {
			t.Fatalf("legacy per-template resource %q was not pruned", resource.Name)
		}
	}
	if got[1].Storage.CRD == nil {
		t.Fatal("stable instances resource lost CRD storage")
	}
	if got[2].Storage.Virtual == nil || got[2].Storage.Virtual.IdentityHash != "templates-identity" {
		t.Fatalf("stable templates resource lost virtual storage: %#v", got[2].Storage)
	}
	if again := reconcileOwnedResources(got, desired, infrav1alpha1.GroupName); !reflect.DeepEqual(again, got) {
		t.Fatalf("reconciliation is not idempotent: %#v", again)
	}
}

func TestResourcesEqualIncludesVirtualReferenceAPIGroup(t *testing.T) {
	resource := func(apiGroup string) apisv1alpha2.ResourceSchema {
		return apisv1alpha2.ResourceSchema{
			Name: "templates", Group: infrav1alpha1.GroupName, Schema: "stable.templates.infrastructure.faros.sh",
			Storage: apisv1alpha2.ResourceSchemaStorage{Virtual: &apisv1alpha2.ResourceSchemaStorageVirtual{
				Reference: corev1.TypedLocalObjectReference{
					APIGroup: ptrTo(apiGroup), Kind: "CachedResourceEndpointSlice", Name: EndpointSliceTemplatesName,
				},
				IdentityHash: "templates-identity",
			}},
		}
	}
	if resourcesEqual([]apisv1alpha2.ResourceSchema{resource("cache.kcp.io")}, []apisv1alpha2.ResourceSchema{resource("wrong.example.io")}) {
		t.Fatal("resourcesEqual ignored storage.virtual.reference.apiGroup")
	}
}

func TestReconcileAPIExportResourcesRetriesConflictWithFreshRead(t *testing.T) {
	existing := []apisv1alpha2.ResourceSchema{
		{
			Name: "applications", Group: infrav1alpha1.GroupName, Schema: "legacy.applications.infrastructure.faros.sh",
			Storage: apisv1alpha2.ResourceSchemaStorage{CRD: &apisv1alpha2.ResourceSchemaStorageCRD{}},
		},
		{
			Name: "widgets", Group: "other.example.io", Schema: "v1.widgets.other.example.io",
			Storage: apisv1alpha2.ResourceSchemaStorage{CRD: &apisv1alpha2.ResourceSchemaStorageCRD{}},
		},
	}
	desired := []apisv1alpha2.ResourceSchema{{
		Name: "instances", Group: infrav1alpha1.GroupName, Schema: "stable.instances.infrastructure.faros.sh",
		Storage: apisv1alpha2.ResourceSchemaStorage{CRD: &apisv1alpha2.ResourceSchemaStorageCRD{}},
	}}
	concurrentResource := apisv1alpha2.ResourceSchema{
		Name: "gadgets", Group: "concurrent.example.io", Schema: "v1.gadgets.concurrent.example.io",
		Storage: apisv1alpha2.ResourceSchemaStorage{CRD: &apisv1alpha2.ResourceSchemaStorageCRD{}},
	}
	encodeResources := func(in []apisv1alpha2.ResourceSchema) []any {
		t.Helper()
		raw, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		var resources []any
		if err := json.Unmarshal(raw, &resources); err != nil {
			t.Fatal(err)
		}
		return resources
	}
	export := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apisv1alpha2.SchemeGroupVersion.String(),
		"kind":       "APIExport",
		"metadata": map[string]any{
			"name": APIExportName,
		},
		"spec": map[string]any{"resources": encodeResources(existing)},
	}}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), export)
	updates := 0
	client.PrependReactor("update", "apiexports", func(k8stesting.Action) (bool, runtime.Object, error) {
		updates++
		if updates == 1 {
			// Simulate another init replica winning the update with an unrelated
			// API group. A correct retry must GET this new state and preserve it.
			tracked, err := client.Tracker().Get(apiExportGVR, "", APIExportName)
			if err != nil {
				return true, nil, err
			}
			concurrent := tracked.DeepCopyObject().(*unstructured.Unstructured)
			if err := unstructured.SetNestedField(
				concurrent.Object,
				encodeResources(append(append([]apisv1alpha2.ResourceSchema(nil), existing...), concurrentResource)),
				"spec", "resources",
			); err != nil {
				return true, nil, err
			}
			if err := client.Tracker().Update(apiExportGVR, concurrent, ""); err != nil {
				return true, nil, err
			}
			return true, nil, apierrors.NewConflict(
				schema.GroupResource{Group: apisv1alpha2.SchemeGroupVersion.Group, Resource: "apiexports"},
				APIExportName,
				errors.New("concurrent init update"),
			)
		}
		return false, nil, nil
	})

	if err := reconcileAPIExportResources(context.Background(), client, desired); err != nil {
		t.Fatalf("reconcileAPIExportResources: %v", err)
	}
	if updates != 2 {
		t.Fatalf("APIExport update attempts = %d, want 2", updates)
	}
	got, err := client.Resource(apiExportGVR).Get(context.Background(), APIExportName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get reconciled APIExport: %v", err)
	}
	gotRaw, found, err := unstructured.NestedFieldNoCopy(got.Object, "spec", "resources")
	if err != nil || !found {
		t.Fatalf("get spec.resources: found=%t err=%v", found, err)
	}
	gotData, err := json.Marshal(gotRaw)
	if err != nil {
		t.Fatal(err)
	}
	var gotResources []apisv1alpha2.ResourceSchema
	if err := json.Unmarshal(gotData, &gotResources); err != nil {
		t.Fatal(err)
	}
	want := reconcileOwnedResources(
		append(append([]apisv1alpha2.ResourceSchema(nil), existing...), concurrentResource),
		desired,
		infrav1alpha1.GroupName,
	)
	if !reflect.DeepEqual(gotResources, want) {
		t.Fatalf("reconciled resources = %#v, want %#v", gotResources, want)
	}
}
