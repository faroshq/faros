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
	claims, err := deploymentPermissionClaims(" deployments-hash ")
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 2 {
		t.Fatalf("deployment claims = %d, want Release and Deployment", len(claims))
	}
	if claims[0].Group != "deployments.faros.sh" || claims[0].Resource != "releases" ||
		!slices.Equal(claims[0].Verbs, []string{"get", "list", "watch", "create"}) ||
		claims[0].IdentityHash != "deployments-hash" {
		t.Fatalf("Release claim = %+v", claims[0])
	}
	if claims[1].Group != "deployments.faros.sh" || claims[1].Resource != "deployments" ||
		!slices.Equal(claims[1].Verbs, []string{"get", "list", "watch", "create", "update", "patch", "delete"}) ||
		claims[1].IdentityHash != "deployments-hash" {
		t.Fatalf("Deployment claim = %+v", claims[1])
	}
}

func TestCodePermissionClaimsIncludeGitOpsSourceAndApproval(t *testing.T) {
	claims := codePermissionClaims("code-hash")
	if len(claims) != 3 {
		t.Fatalf("code claims = %d, want Repository, RepositorySync, ChangeRequest", len(claims))
	}
	for i, resource := range []string{"repositories", "repositorysyncs", "changerequests"} {
		if claims[i].Group != "code.faros.sh" || claims[i].Resource != resource || claims[i].IdentityHash != "code-hash" {
			t.Fatalf("claim %d = %+v", i, claims[i])
		}
	}
}

func TestDeploymentPermissionClaimsRequireIdentityHash(t *testing.T) {
	for _, value := range []string{"", "   "} {
		claims, err := deploymentPermissionClaims(value)
		if err == nil || !strings.Contains(err.Error(), "APP_STUDIO_DEPLOYMENTS_IDENTITY_HASH is required") {
			t.Fatalf("deploymentPermissionClaims(%q) = %#v, %v; want required error", value, claims, err)
		}
		if claims != nil {
			t.Fatalf("deploymentPermissionClaims(%q) returned hash-less claims: %#v", value, claims)
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
			"- name: deployments",
			"- resource: releases\n        group: deployments.faros.sh\n        verbs: [\"get\", \"list\", \"watch\", \"create\"]",
			"- resource: deployments\n        group: deployments.faros.sh\n        verbs: [\"get\", \"list\", \"watch\", \"create\", \"update\", \"patch\", \"delete\"]",
			"- resource: repositorysyncs\n        group: code.faros.sh\n        verbs: [\"get\", \"list\", \"watch\", \"create\", \"update\", \"patch\", \"delete\"]",
			"- resource: changerequests\n        group: code.faros.sh\n        verbs: [\"get\", \"list\", \"watch\", \"create\", \"update\", \"patch\", \"delete\"]",
		} {
			if !strings.Contains(source.body, required) {
				t.Errorf("%s is missing synchronized deployment contract %q", source.name, required)
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
		!strings.Contains(string(deployment), "required \"apiExport.deploymentsIdentityHash is required") ||
		!strings.Contains(string(deployment), "required \"apiExport.codeIdentityHash is required") ||
		!strings.Contains(string(values), "deploymentsIdentityHash:") {
		t.Fatal("chart does not fail closed on the Code and Deployments APIExport identity hashes")
	}
}
