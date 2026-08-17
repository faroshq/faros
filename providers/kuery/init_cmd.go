// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"context"
	"fmt"
	"log"
	"os"

	sdkinstall "github.com/faroshq/provider-sdk/install"
)

const (
	apiExportName        = "kuery.providers.faros.sh"
	defaultWorkspacePath = "root:faros:providers:kuery"
	edgesClaimGroup      = "edges.faros.sh"
	edgesClaimResource   = "kubernetesclusters"
)

// runInitCmd applies kuery's in-workspace objects (APIResourceSchemas,
// APIExport, APIExportEndpointSlice, bind grant) using the workspace-admin
// kubeconfig the admin onboarded. Idempotent.
//
// kuery's KubernetesCluster permission claim is a FIRST-PARTY type
// (edges.faros.sh), so its APIExport claim must carry the identityHash of the
// edges provider APIExport. The workspace-scoped kubeconfig cannot read that
// sibling workspace, so the platform admin supplies it via
// KUERY_EDGES_IDENTITY_HASH. The hub independently resolves the current edges
// export identity before it marks kuery ready.
func runInitCmd(ctx context.Context) error {
	config, err := loadProviderConfig()
	if err != nil {
		return fmt.Errorf("init needs a kubeconfig (set FAROS_PROVIDER_KUBECONFIG): %w", err)
	}
	workspacePath := os.Getenv("KUERY_WORKSPACE_PATH")
	if workspacePath == "" {
		workspacePath = defaultWorkspacePath
	}
	schemasDir := os.Getenv("FAROS_SCHEMAS_DIR")
	if schemasDir == "" {
		schemasDir = "/etc/faros/schemas"
	}

	edgesHash := os.Getenv("KUERY_EDGES_IDENTITY_HASH")
	if edgesHash == "" {
		log.Printf("WARNING KUERY_EDGES_IDENTITY_HASH is empty; the KubernetesCluster permission claim will have no identityHash and tenant Enable will not engage edges. Query status.identityHash from APIExport edges.providers.faros.sh in root:faros:providers:edges and set the chart value.")
	}
	catalogEntryFile := os.Getenv("FAROS_CATALOGENTRY_FILE")

	if err := sdkinstall.Bootstrap(ctx, sdkinstall.Options{
		Config:           config,
		ExportName:       apiExportName,
		WorkspacePath:    workspacePath,
		SchemasDir:       schemasDir,
		Claims:           kueryPermissionClaims(edgesHash),
		CatalogEntryFile: catalogEntryFile,
	}); err != nil {
		return fmt.Errorf("provider workspace bootstrap: %w", err)
	}
	log.Printf("kuery init: workspace bootstrapped (export=%s path=%s schemas=%s edgesHash=%t catalogEntry=%s)", apiExportName, workspacePath, schemasDir, edgesHash != "", catalogEntryFile)
	return nil
}

func kueryPermissionClaims(edgesHash string) []sdkinstall.PermissionClaim {
	return []sdkinstall.PermissionClaim{{
		Group:        edgesClaimGroup,
		Resource:     edgesClaimResource,
		Verbs:        []string{"get", "list", "watch"},
		IdentityHash: edgesHash,
	}}
}
