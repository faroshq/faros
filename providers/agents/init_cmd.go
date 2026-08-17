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

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	sdkinstall "github.com/faroshq/provider-sdk/install"
)

const (
	apiExportName        = "agents.faros.sh"
	defaultWorkspacePath = "root:faros:providers:agents"
)

// runInitCmd applies the provider's in-workspace objects (APIResourceSchemas,
// APIExport, APIExportEndpointSlice, bind grant, and optionally the
// CatalogEntry) using the workspace-admin kubeconfig. Idempotent.
func runInitCmd(ctx context.Context) error {
	config, err := loadInitConfig()
	if err != nil {
		return fmt.Errorf("init needs a kubeconfig (set FAROS_PROVIDER_KUBECONFIG): %w", err)
	}
	workspacePath := os.Getenv("AGENTS_WORKSPACE_PATH")
	if workspacePath == "" {
		workspacePath = defaultWorkspacePath
	}
	schemasDir := os.Getenv("FAROS_SCHEMAS_DIR")
	if schemasDir == "" {
		schemasDir = "/etc/faros/schemas"
	}
	catalogEntryFile := os.Getenv("FAROS_CATALOGENTRY_FILE")

	if err := sdkinstall.Bootstrap(ctx, sdkinstall.Options{
		Config:        config,
		ExportName:    apiExportName,
		WorkspacePath: workspacePath,
		SchemasDir:    schemasDir,
		// The provider stores model credentials and per-connection secrets in
		// the tenant workspace and acts as the calling user; the claim lets it
		// read/write those Secrets. Tenant scoping is expressed in the
		// CatalogEntry's permissionClaims (manifest.yaml).
		//
		// MUST stay in sync with manifest.yaml and the chart's
		// catalogentry.yaml: the CatalogEntry drives what tenants accept on
		// their APIBinding, but the virtual workspace authorizes against the
		// claims on the APIExport written here — a claim missing on either
		// side is denied.
		Claims:           agentPermissionClaims(),
		CatalogEntryFile: catalogEntryFile,
	}); err != nil {
		return fmt.Errorf("provider workspace bootstrap: %w", err)
	}
	log.Printf("agents-provider init: workspace bootstrapped (export=%s path=%s schemas=%s catalogEntry=%s)", apiExportName, workspacePath, schemasDir, catalogEntryFile)
	return nil
}

func agentPermissionClaims() []sdkinstall.PermissionClaim {
	return []sdkinstall.PermissionClaim{
		{Resource: "secrets", Verbs: []string{"get", "list", "watch", "create", "update", "delete"}},
		// Per-agent identity for background runs (api/agentidentity.go):
		// the agent's ServiceAccount, its read-instances ClusterRole and
		// binding. Create-only keeps the provider from mutating arbitrary
		// tenant RBAC; legacy wildcard roles require admin migration.
		{Resource: "serviceaccounts", Verbs: []string{"get", "create"}},
		{Group: "rbac.authorization.k8s.io", Resource: "clusterroles", Verbs: []string{"get", "create"}},
		{Group: "rbac.authorization.k8s.io", Resource: "clusterrolebindings", Verbs: []string{"get", "create"}},
		// Service-to-service callers authenticate and authorize against the
		// target tenant workspace through the APIExport virtual workspace.
		// These built-in review APIs do not carry an identityHash.
		{Group: "authentication.k8s.io", Resource: "tokenreviews", Verbs: []string{"create"}},
		{Group: "authorization.k8s.io", Resource: "subjectaccessreviews", Verbs: []string{"create"}},
	}
}

// loadInitConfig resolves the workspace-admin kubeconfig for init.
func loadInitConfig() (*rest.Config, error) {
	if p := os.Getenv("FAROS_PROVIDER_KUBECONFIG"); p != "" {
		return clientcmd.BuildConfigFromFlags("", p)
	}
	if p := os.Getenv("KUBECONFIG"); p != "" {
		return clientcmd.BuildConfigFromFlags("", p)
	}
	return rest.InClusterConfig()
}
