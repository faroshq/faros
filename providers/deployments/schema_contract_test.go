// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package main

import (
	"bytes"
	"os"
	"reflect"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestRepositorySyncSchemaContract(t *testing.T) {
	kcpSchema, err := os.ReadFile("config/kcp/apiresourceschema-repositorysyncs.deployments.faros.sh.yaml")
	if err != nil {
		t.Fatal(err)
	}
	chartSchema, err := os.ReadFile("deploy/chart/files/schemas/repositorysyncs.deployments.faros.sh.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(kcpSchema, chartSchema) {
		t.Fatal("RepositorySync chart schema is not synchronized with the generated kcp schema")
	}
	for _, required := range [][]byte{
		[]byte("- AwaitingAuthorization"),
		[]byte("- Synced"),
		[]byte("targetRequirements:"),
		[]byte("sourcePath:"),
	} {
		if !bytes.Contains(kcpSchema, required) {
			t.Fatalf("RepositorySync schema does not contain %q", required)
		}
	}
	export, err := os.ReadFile("config/kcp/apiexport-deployments.faros.sh.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(export, []byte("name: deployments\n")) || bytes.Contains(export, []byte("name: releases\n")) {
		t.Fatal("Deployments APIExport still publishes the removed Release/Deployment APIs")
	}
	for _, removed := range []string{
		"config/crds/deployments.faros.sh_deployments.yaml",
		"config/crds/deployments.faros.sh_releases.yaml",
		"config/kcp/apiresourceschema-deployments.deployments.faros.sh.yaml",
		"config/kcp/apiresourceschema-releases.deployments.faros.sh.yaml",
	} {
		if _, err := os.Stat(removed); !os.IsNotExist(err) {
			t.Fatalf("removed schema %q still exists or cannot be inspected: %v", removed, err)
		}
	}
}

func TestCatalogAndChartProviderContract(t *testing.T) {
	manifestRaw, err := os.ReadFile("manifest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	chartRaw, err := os.ReadFile("deploy/chart/templates/catalogentry.yaml")
	if err != nil {
		t.Fatal(err)
	}
	manifest := decodeCatalogEntry(t, manifestRaw)
	chart := decodeCatalogEntry(t, extractChartCatalogEntry(t, chartRaw))
	normalizeCatalogEntry(manifest)
	normalizeCatalogEntry(chart)
	if !reflect.DeepEqual(manifest, chart) {
		t.Fatalf("manifest and chart CatalogEntry specs differ\nmanifest: %#v\nchart:    %#v", manifest["spec"], chart["spec"])
	}
	spec, _ := manifest["spec"].(map[string]any)
	dependencies, _ := spec["dependencies"].([]any)
	if len(dependencies) != 1 || !reflect.DeepEqual(dependencies[0], map[string]any{"name": "code"}) {
		t.Fatalf("Deployments dependencies = %#v, want Code only", dependencies)
	}

	claims, err := deploymentClaims("test-infrastructure-identity", "test-code-identity")
	if err != nil {
		t.Fatal(err)
	}
	manifestClaims := nestedCatalogClaims(t, manifest)
	if len(manifestClaims) != len(claims) {
		t.Fatalf("CatalogEntry claim count = %d, deploymentClaims count = %d", len(manifestClaims), len(claims))
	}
	for i, claim := range claims {
		want := map[string]any{
			"group":        claim.Group,
			"resource":     claim.Resource,
			"verbs":        stringValues(claim.Verbs),
			"tenantScoped": true,
		}
		switch {
		case claim.Group == "code.faros.sh":
			want["purpose"] = "Read bounded repository checkouts for desired-state sync."
		case claim.Group == "infrastructure.faros.sh":
			want["optional"] = true
			want["purpose"] = "Apply Infrastructure instances from repository syncs."
		case claim.Group == "" && claim.Resource == "configmaps":
			want["optional"] = true
			want["purpose"] = "Apply ConfigMaps from repository syncs."
		}
		if !reflect.DeepEqual(manifestClaims[i], want) {
			t.Fatalf("CatalogEntry claim %d differs from deploymentClaims\nmanifest: %#v\nruntime:  %#v", i, manifestClaims[i], want)
		}
	}

	chartDeployment, err := os.ReadFile("deploy/chart/templates/deployment.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, contract := range [][]byte{
		[]byte(`required "codeIdentityHash is required"`),
		[]byte(`required "code.url is required"`),
		[]byte("automountServiceAccountToken: false"),
		[]byte("imagePullPolicy: {{ .Values.image.pullPolicy }}"),
	} {
		if !bytes.Contains(chartDeployment, contract) {
			t.Fatalf("chart Deployment is missing %q", contract)
		}
	}
	if bytes.Contains(chartDeployment, []byte(`required "infrastructureIdentityHash is required"`)) {
		t.Fatal("chart still requires the optional Infrastructure identity hash")
	}
}

func decodeCatalogEntry(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	entry := map[string]any{}
	if err := yaml.Unmarshal(raw, &entry); err != nil {
		t.Fatalf("decode CatalogEntry: %v", err)
	}
	return entry
}

func extractChartCatalogEntry(t *testing.T, raw []byte) []byte {
	t.Helper()
	const marker = "  catalogentry.yaml: |\n"
	content := string(raw)
	start := strings.Index(content, marker)
	if start < 0 {
		t.Fatal("chart template has no catalogentry.yaml data block")
	}
	content = content[start+len(marker):]
	if end := strings.LastIndex(content, "{{- end }}"); end >= 0 {
		content = content[:end]
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		line = strings.TrimPrefix(line, "    ")
		switch {
		case strings.HasPrefix(strings.TrimSpace(line), "version: {{"):
			line = "  version: \"chart-version\""
		case strings.HasPrefix(strings.TrimSpace(line), "url: \"http://{{"):
			line = "    url: \"chart-backend\""
		}
		lines[i] = line
	}
	return []byte(strings.TrimSpace(strings.Join(lines, "\n")) + "\n")
}

func normalizeCatalogEntry(entry map[string]any) {
	spec, _ := entry["spec"].(map[string]any)
	spec["version"] = "normalized"
	ui, _ := spec["ui"].(map[string]any)
	ui["url"] = "normalized"
	backend, _ := spec["backend"].(map[string]any)
	backend["url"] = "normalized"
}

func nestedCatalogClaims(t *testing.T, entry map[string]any) []map[string]any {
	t.Helper()
	spec, ok := entry["spec"].(map[string]any)
	if !ok {
		t.Fatal("CatalogEntry spec is missing")
	}
	export, ok := spec["apiExport"].(map[string]any)
	if !ok {
		t.Fatal("CatalogEntry spec.apiExport is missing")
	}
	raw, ok := export["permissionClaims"].([]any)
	if !ok {
		t.Fatal("CatalogEntry permissionClaims are missing")
	}
	claims := make([]map[string]any, 0, len(raw))
	for _, value := range raw {
		claim, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("CatalogEntry claim is not an object: %#v", value)
		}
		claims = append(claims, claim)
	}
	return claims
}

func stringValues(values []string) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}
