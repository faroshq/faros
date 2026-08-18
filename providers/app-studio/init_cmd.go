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
	"strings"

	sdkinstall "github.com/faroshq/provider-sdk/install"
)

const (
	apiExportName        = "ai.faros.sh"
	defaultWorkspacePath = "root:faros:providers:app-studio"
)

// instanceClaimResources are the infrastructure instance resources the
// Project reconciler lifecycles — the templates' instanceCRD plurals a
// project's live bindings can reference. Extend as the template vocabulary
// grows, and keep manifest.yaml + deploy/chart/templates/catalogentry.yaml in
// sync: the hub writes the tenant APIBinding claims from those at Enable time,
// and a claim missing there is silently denied at reconcile.
// searxngs backs the Studio's shared web-search instance; browsers backs the
// Studio's shared headless browser (development-preview inspection).
var instanceClaimResources = []string{"instances"}

// runInitCmd applies the App Studio provider's in-workspace objects
// (APIResourceSchemas, APIExport, APIExportEndpointSlice, bind grant) using the
// workspace-admin kubeconfig the admin onboarded. Idempotent.
func runInitCmd(ctx context.Context) error {
	config, err := loadProviderConfig()
	if err != nil {
		return fmt.Errorf("init needs a kubeconfig (set FAROS_PROVIDER_KUBECONFIG): %w", err)
	}
	workspacePath := os.Getenv("APP_STUDIO_WORKSPACE_PATH")
	if workspacePath == "" {
		workspacePath = defaultWorkspacePath
	}
	schemasDir := os.Getenv("FAROS_SCHEMAS_DIR")
	if schemasDir == "" {
		schemasDir = "/etc/faros/schemas"
	}
	catalogEntryFile := os.Getenv("FAROS_CATALOGENTRY_FILE")

	// The Project reconciler creates/deletes infrastructure instances in
	// tenant workspaces. Those are first-party (*.faros.sh) types, so kcp
	// requires the identityHash of the APIExport serving them
	// (infrastructure.providers.faros.sh) — Helm value in prod, the dev
	// Makefile auto-discovers it.
	infraHash := os.Getenv("APP_STUDIO_INFRA_IDENTITY_HASH")
	if infraHash == "" {
		log.Printf("WARNING: APP_STUDIO_INFRA_IDENTITY_HASH is empty — the instance permission claims will have no identityHash and tenant Enable will not engage instance lifecycling")
	}
	claims := make([]sdkinstall.PermissionClaim, 0, len(instanceClaimResources)+8)
	for _, r := range instanceClaimResources {
		claims = append(claims, sdkinstall.PermissionClaim{
			Group:        "infrastructure.faros.sh",
			Resource:     r,
			Verbs:        []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			IdentityHash: infraHash,
		})
	}
	// Repository creation (git backing) — the Project reconciler creates
	// Repository CRs; commits go through the code provider's MCP as the
	// project's ServiceAccount (commit bundles are code-provider-local).
	codeHash := strings.TrimSpace(os.Getenv("APP_STUDIO_CODE_IDENTITY_HASH"))
	if codeHash == "" {
		return fmt.Errorf("APP_STUDIO_CODE_IDENTITY_HASH is required")
	}
	claims = append(claims, codePermissionClaims(codeHash)...)
	// Reviewed delivery uses Deployments only for RepositorySync. The target
	// directory contains concrete provider objects, so App Studio never needs
	// Release or Deployment API access.
	deploymentClaims := deploymentPermissionClaims(os.Getenv("APP_STUDIO_DEPLOYMENTS_IDENTITY_HASH"))
	if len(deploymentClaims) == 0 {
		log.Printf("WARNING: APP_STUDIO_DEPLOYMENTS_IDENTITY_HASH is empty — reviewed-production claims will be omitted; tenants can still use Direct delivery")
	}
	claims = append(claims, deploymentClaims...)
	// Per-project ServiceAccount identity: repository commits run in the
	// reconciler long after the request that caused the edits, so they act as
	// an identity of their own rather than borrowing the user's bearer. Also:
	// per-project LLM credentials ride Secrets. Built-in types — no
	// identityHash needed.
	claims = append(claims,
		sdkinstall.PermissionClaim{Resource: "serviceaccounts", Verbs: []string{"get", "list", "watch", "create", "delete"}},
		sdkinstall.PermissionClaim{Resource: "secrets", Verbs: []string{"get", "list", "watch", "create", "update", "delete"}},
		sdkinstall.PermissionClaim{
			Group:    "rbac.authorization.k8s.io",
			Resource: "clusterroles",
			Verbs:    []string{"get", "list", "watch", "create", "update", "delete"},
		},
		sdkinstall.PermissionClaim{
			Group:    "rbac.authorization.k8s.io",
			Resource: "clusterrolebindings",
			Verbs:    []string{"get", "list", "watch", "create", "update", "delete"},
		},
	)

	if err := sdkinstall.Bootstrap(ctx, sdkinstall.Options{
		Config:           config,
		ExportName:       apiExportName,
		WorkspacePath:    workspacePath,
		SchemasDir:       schemasDir,
		Claims:           claims,
		CatalogEntryFile: catalogEntryFile,
	}); err != nil {
		return fmt.Errorf("provider workspace bootstrap: %w", err)
	}
	log.Printf("app-studio init: workspace bootstrapped (export=%s path=%s schemas=%s catalogEntry=%s claims=%d)", apiExportName, workspacePath, schemasDir, catalogEntryFile, len(claims))
	return nil
}

func codePermissionClaims(identityHash string) []sdkinstall.PermissionClaim {
	return []sdkinstall.PermissionClaim{
		{
			Group:        "code.faros.sh",
			Resource:     "repositories",
			Verbs:        []string{"get", "list", "watch", "create", "update", "patch"},
			IdentityHash: identityHash,
		},
		{
			Group:        "code.faros.sh",
			Resource:     "changerequests",
			Verbs:        []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			IdentityHash: identityHash,
		},
	}
}

func deploymentPermissionClaims(identityHash string) []sdkinstall.PermissionClaim {
	hash := strings.TrimSpace(identityHash)
	if hash == "" {
		return nil
	}
	return []sdkinstall.PermissionClaim{
		{
			Group:        "deployments.faros.sh",
			Resource:     "repositorysyncs",
			Verbs:        []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			IdentityHash: hash,
		},
	}
}
