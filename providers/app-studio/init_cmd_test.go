// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestDeploymentPermissionClaimsUseDeploymentIdentity(t *testing.T) {
	claims := deploymentPermissionClaims(" deployments-hash ")
	if len(claims) != 1 {
		t.Fatalf("deployment claims = %d, want RepositorySync only", len(claims))
	}
	if claims[0].Group != "deployments.faros.sh" || claims[0].Resource != "repositorysyncs" ||
		!slices.Equal(claims[0].Verbs, []string{"get", "list", "watch", "create", "update", "patch", "delete"}) ||
		claims[0].IdentityHash != "deployments-hash" {
		t.Fatalf("RepositorySync claim = %+v", claims[0])
	}
}

func TestCodePermissionClaimsIncludeGitOpsSourceAndApproval(t *testing.T) {
	claims := codePermissionClaims("code-hash")
	if len(claims) != 2 {
		t.Fatalf("code claims = %d, want Repository and ChangeRequest", len(claims))
	}
	for i, resource := range []string{"repositories", "changerequests"} {
		if claims[i].Group != "code.faros.sh" || claims[i].Resource != resource || claims[i].IdentityHash != "code-hash" {
			t.Fatalf("claim %d = %+v", i, claims[i])
		}
	}
}

func TestDeploymentPermissionClaimsAreOptionalWithoutIdentityHash(t *testing.T) {
	for _, value := range []string{"", "   "} {
		if claims := deploymentPermissionClaims(value); claims != nil {
			t.Fatalf("deploymentPermissionClaims(%q) = %#v, want no optional claims", value, claims)
		}
	}
}

func TestCatalogAndChartKeepDeploymentContractInSync(t *testing.T) {
	manifest, err := os.ReadFile("manifest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	chart, err := os.ReadFile("deploy/chart/templates/catalogentry.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []struct {
		name string
		body string
	}{
		{name: "manifest", body: string(manifest)},
		{name: "chart", body: string(chart)},
	} {
		for _, required := range []string{
			"- resource: repositorysyncs\n        group: deployments.faros.sh\n        verbs: [\"get\", \"list\", \"watch\", \"create\", \"update\", \"patch\", \"delete\"]",
			"- resource: repositorysyncs\n        group: deployments.faros.sh\n        verbs: [\"get\", \"list\", \"watch\", \"create\", \"update\", \"patch\", \"delete\"]\n        tenantScoped: true\n        optional: true",
			"- resource: changerequests\n        group: code.faros.sh\n        verbs: [\"get\", \"list\", \"watch\", \"create\", \"update\", \"patch\", \"delete\"]",
		} {
			if !strings.Contains(source.body, required) {
				t.Errorf("%s is missing synchronized deployment contract %q", source.name, required)
			}
		}
		if strings.Contains(source.body, "- resource: repositorysyncs\n        group: code.faros.sh") {
			t.Errorf("%s still advertises RepositorySync under the Code APIExport", source.name)
		}
		if strings.Contains(source.body, "- name: deployments") {
			t.Errorf("%s still declares Deployments as a hard CatalogEntry dependency", source.name)
		}
		for _, removed := range []string{"- resource: releases\n        group: deployments.faros.sh", "- resource: deployments\n        group: deployments.faros.sh"} {
			if strings.Contains(source.body, removed) {
				t.Errorf("%s still advertises target-specific Deployments API %q", source.name, removed)
			}
		}
	}

	deployment, err := os.ReadFile("deploy/chart/templates/deployment.yaml")
	if err != nil {
		t.Fatal(err)
	}
	values, err := os.ReadFile("deploy/chart/values.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(deployment), "APP_STUDIO_DEPLOYMENTS_IDENTITY_HASH") ||
		!strings.Contains(string(deployment), ".Values.apiExport.deploymentsIdentityHash") ||
		strings.Contains(string(deployment), "required \"apiExport.deploymentsIdentityHash is required") ||
		!strings.Contains(string(deployment), "required \"apiExport.codeIdentityHash is required") ||
		!strings.Contains(string(values), "deploymentsIdentityHash:") {
		t.Fatal("chart does not keep Code identity required while making Deployments identity optional")
	}
}
