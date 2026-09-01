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
	"io/fs"
	"strings"

	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
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

// TestEmbeddedPlatformCRDsIncludeDevelopmentService protects the install
// boundary, not just controller-gen output. PlatformSchemaInAPIExport walks
// crdsFS, so a CRD that exists only in config/crds is invisible to a fresh
// provider init and tenants cannot create that resource after APIBind.
func TestEmbeddedPlatformCRDsIncludeDevelopmentService(t *testing.T) {
	entries, err := fs.ReadDir(crdsFS, "crds")
	if err != nil {
		t.Fatalf("read embedded CRDs: %v", err)
	}

	var found bool
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "" {
			continue
		}
		raw, err := fs.ReadFile(crdsFS, "crds/"+entry.Name())
		if err != nil {
			t.Fatalf("read embedded CRD %s: %v", entry.Name(), err)
		}
		var crd apiextensionsv1.CustomResourceDefinition
		if err := utilyaml.Unmarshal(raw, &crd); err != nil {
			t.Fatalf("decode embedded CRD %s: %v", entry.Name(), err)
		}
		if crd.Spec.Group != "infrastructure.faros.sh" || crd.Spec.Names.Plural != "developmentservices" {
			continue
		}
		found = true
		if crd.Name != "developmentservices.infrastructure.faros.sh" {
			t.Fatalf("DevelopmentService CRD name = %q", crd.Name)
		}
		if crd.Spec.Names.Kind != "DevelopmentService" || crd.Spec.Scope != apiextensionsv1.ClusterScoped {
			t.Fatalf("DevelopmentService CRD identity = kind=%q scope=%q", crd.Spec.Names.Kind, crd.Spec.Scope)
		}
		if len(crd.Spec.Versions) != 1 || crd.Spec.Versions[0].Name != "v1alpha1" {
			t.Fatalf("DevelopmentService CRD versions = %#v", crd.Spec.Versions)
		}
		if crd.Spec.Versions[0].Schema == nil || crd.Spec.Versions[0].Schema.OpenAPIV3Schema == nil {
			t.Fatal("DevelopmentService CRD has no OpenAPI schema")
		}
	}
	if !found {
		t.Fatal("embedded platform CRDs do not include developmentservices")
	}
}

func TestEmbeddedPlatformCRDsIncludeConnection(t *testing.T) {
	entries, err := fs.ReadDir(crdsFS, "crds")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		raw, err := fs.ReadFile(crdsFS, "crds/"+entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		var crd apiextensionsv1.CustomResourceDefinition
		if err := utilyaml.Unmarshal(raw, &crd); err != nil {
			t.Fatal(err)
		}
		if crd.Spec.Names.Plural == "connections" {
			if crd.Name != "connections.infrastructure.faros.sh" || crd.Spec.Names.Kind != "Connection" || crd.Spec.Scope != apiextensionsv1.ClusterScoped {
				t.Fatalf("Connection CRD identity = name=%q kind=%q scope=%q", crd.Name, crd.Spec.Names.Kind, crd.Spec.Scope)
			}
			spec := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"]
			if len(spec.XValidations) == 0 || !strings.Contains(spec.XValidations[0].Rule, "oldSelf") {
				t.Fatalf("Connection spec does not carry immutable validation: %#v", spec.XValidations)
			}
			return
		}
	}
	t.Fatal("embedded platform CRDs do not include connections")
}
