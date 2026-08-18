// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"

	sdkinstall "github.com/faroshq/provider-sdk/install"
)

func TestCatalogEntryContractStaysInSync(t *testing.T) {
	manifest := catalogParityReadEntry(t, "manifest.yaml")
	chart := catalogParityRenderEntry(t)
	initialized := catalogParityInitContract(t, codeClaims(), nil)

	catalogParityCompare(t, "dependencies", manifest.Dependencies, chart.Dependencies)
	catalogParityCompare(t, "requiredResources (manifest/chart)", manifest.RequiredResources, chart.RequiredResources)
	catalogParityCompare(t, "requiredResources (manifest/init schemas)", manifest.RequiredResources, initialized.RequiredResources)
	catalogParityCompare(t, "permissionClaims (manifest/chart)", manifest.PermissionClaims, chart.PermissionClaims)
	catalogParityCompare(t, "permissionClaims (manifest/init)", manifest.PermissionClaims, initialized.PermissionClaims)
}

type catalogParityContract struct {
	Dependencies      []catalogParityDependency
	RequiredResources []catalogParityResource
	PermissionClaims  []catalogParityClaim
}

type catalogParityDependency struct {
	Name string `json:"name"`
}

type catalogParityResource struct {
	Group string `json:"group"`
	Name  string `json:"name"`
}

type catalogParityClaim struct {
	Group          string   `json:"group"`
	Resource       string   `json:"resource"`
	Verbs          []string `json:"verbs"`
	IdentitySource string
}

type catalogParityEntry struct {
	Kind string            `json:"kind"`
	Data map[string]string `json:"data"`
	Spec struct {
		Dependencies []catalogParityDependency `json:"dependencies"`
		APIExport    struct {
			RequiredResources []catalogParityResource `json:"requiredResources"`
			PermissionClaims  []struct {
				Group          string   `json:"group"`
				Resource       string   `json:"resource"`
				Verbs          []string `json:"verbs"`
				TenantScoped   bool     `json:"tenantScoped"`
				IdentitySource *struct {
					Kind     string `json:"kind"`
					Provider string `json:"provider"`
				} `json:"identitySource"`
			} `json:"permissionClaims"`
		} `json:"apiExport"`
	} `json:"spec"`
}

func catalogParityReadEntry(t *testing.T, path string) catalogParityContract {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return catalogParityDecodeEntry(t, path, raw)
}

func catalogParityRenderEntry(t *testing.T) catalogParityContract {
	t.Helper()
	cmd := exec.Command("helm", "template", "catalog-parity", "deploy/chart",
		"--set", "catalogEntry.enabled=true", "--set", "catalogEntry.renderAsConfigMap=true")
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("render Helm chart: %v\n%s", err, raw)
	}
	decoder := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(raw), 4096)
	for {
		var doc catalogParityEntry
		if err := decoder.Decode(&doc); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("decode rendered Helm chart: %v", err)
		}
		if doc.Kind == "ConfigMap" && doc.Data["catalogentry.yaml"] != "" {
			return catalogParityDecodeEntry(t, "rendered Helm CatalogEntry", []byte(doc.Data["catalogentry.yaml"]))
		}
	}
	t.Fatal("rendered Helm chart has no CatalogEntry ConfigMap")
	return catalogParityContract{}
}

func catalogParityDecodeEntry(t *testing.T, source string, raw []byte) catalogParityContract {
	t.Helper()
	var entry catalogParityEntry
	if err := yaml.Unmarshal(raw, &entry); err != nil {
		t.Fatalf("decode %s: %v", source, err)
	}
	contract := catalogParityContract{
		Dependencies:      append([]catalogParityDependency(nil), entry.Spec.Dependencies...),
		RequiredResources: append([]catalogParityResource(nil), entry.Spec.APIExport.RequiredResources...),
	}
	for _, claim := range entry.Spec.APIExport.PermissionClaims {
		if !claim.TenantScoped {
			t.Fatalf("%s claim %s/%s is not tenant-scoped", source, claim.Group, claim.Resource)
		}
		identitySource := ""
		if claim.IdentitySource != nil {
			identitySource = claim.IdentitySource.Kind + "/" + claim.IdentitySource.Provider
		}
		contract.PermissionClaims = append(contract.PermissionClaims, catalogParityClaim{
			Group: claim.Group, Resource: claim.Resource, Verbs: append([]string(nil), claim.Verbs...), IdentitySource: identitySource,
		})
	}
	catalogParityNormalize(&contract)
	return contract
}

func catalogParityInitContract(t *testing.T, claims []sdkinstall.PermissionClaim, identitySources map[string]string) catalogParityContract {
	t.Helper()
	paths, err := filepath.Glob("deploy/chart/files/schemas/*.yaml")
	if err != nil || len(paths) == 0 {
		t.Fatalf("find init schemas: paths=%v err=%v", paths, err)
	}
	contract := catalogParityContract{}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Spec struct {
				Group string `json:"group"`
				Names struct {
					Plural string `json:"plural"`
				} `json:"names"`
			} `json:"spec"`
		}
		if err := yaml.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("decode schema %s: %v", path, err)
		}
		contract.RequiredResources = append(contract.RequiredResources, catalogParityResource{Group: schema.Spec.Group, Name: schema.Spec.Names.Plural})
	}
	for _, claim := range claims {
		identitySource := ""
		if claim.IdentityHash != "" {
			var ok bool
			identitySource, ok = identitySources[claim.IdentityHash]
			if !ok {
				t.Fatalf("init claim %s/%s has unexpected identity hash %q", claim.Group, claim.Resource, claim.IdentityHash)
			}
		}
		contract.PermissionClaims = append(contract.PermissionClaims, catalogParityClaim{
			Group: claim.Group, Resource: claim.Resource, Verbs: append([]string(nil), claim.Verbs...), IdentitySource: identitySource,
		})
	}
	catalogParityNormalize(&contract)
	return contract
}

func catalogParityNormalize(contract *catalogParityContract) {
	sort.Slice(contract.Dependencies, func(i, j int) bool { return contract.Dependencies[i].Name < contract.Dependencies[j].Name })
	sort.Slice(contract.RequiredResources, func(i, j int) bool {
		return contract.RequiredResources[i].Group+"/"+contract.RequiredResources[i].Name < contract.RequiredResources[j].Group+"/"+contract.RequiredResources[j].Name
	})
	for i := range contract.PermissionClaims {
		sort.Strings(contract.PermissionClaims[i].Verbs)
	}
	sort.Slice(contract.PermissionClaims, func(i, j int) bool {
		return contract.PermissionClaims[i].Group+"/"+contract.PermissionClaims[i].Resource < contract.PermissionClaims[j].Group+"/"+contract.PermissionClaims[j].Resource
	})
}

func catalogParityCompare(t *testing.T, field string, want, got any) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("%s drifted:\nmanifest: %#v\nother:    %#v", field, want, got)
	}
}
