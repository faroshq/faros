// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	sdkinstall "github.com/faroshq/provider-sdk/install"
)

const (
	apiExportName        = "deployments.faros.sh"
	defaultWorkspacePath = "root:faros:providers:deployments"
)

func deploymentClaims(infrastructureIdentityHash, codeIdentityHash string) ([]sdkinstall.PermissionClaim, error) {
	codeHash := strings.TrimSpace(codeIdentityHash)
	if codeHash == "" {
		return nil, fmt.Errorf("DEPLOYMENTS_CODE_IDENTITY_HASH is required")
	}
	claims := []sdkinstall.PermissionClaim{{
		Group:        "code.faros.sh",
		Resource:     "repositorycheckouts",
		Verbs:        []string{"get", "list", "watch", "create", "update", "patch", "delete"},
		IdentityHash: codeHash,
	}}
	// Infrastructure is one optional target capability, not a Deployments
	// runtime dependency. When its APIExport is installed, advertising the
	// claim lets a tenant explicitly authorize Instance apply from the portal.
	if infraHash := strings.TrimSpace(infrastructureIdentityHash); infraHash != "" {
		claims = append(claims, sdkinstall.PermissionClaim{
			Group:        "infrastructure.faros.sh",
			Resource:     "instances",
			Verbs:        []string{"get", "create", "update", "patch", "delete"},
			IdentityHash: infraHash,
		})
	}
	claims = append(claims, sdkinstall.PermissionClaim{
		Resource: "configmaps",
		Verbs:    []string{"get", "list", "watch", "create", "update", "patch", "delete"},
	})
	return claims, nil
}

func runInitCmd(ctx context.Context) error {
	config, err := loadControllerConfig()
	if err != nil {
		return err
	}
	claims, err := deploymentClaims(os.Getenv("DEPLOYMENTS_INFRA_IDENTITY_HASH"), os.Getenv("DEPLOYMENTS_CODE_IDENTITY_HASH"))
	if err != nil {
		return err
	}
	workspacePath := deploymentWorkspacePath()
	schemasDir := os.Getenv("FAROS_SCHEMAS_DIR")
	if schemasDir == "" {
		schemasDir = "/etc/faros/schemas"
	}
	return sdkinstall.Bootstrap(ctx, sdkinstall.Options{Config: config, ExportName: apiExportName, WorkspacePath: workspacePath, SchemasDir: schemasDir, Claims: claims, CatalogEntryFile: os.Getenv("FAROS_CATALOGENTRY_FILE")})
}

func deploymentWorkspacePath() string {
	if workspacePath := strings.TrimSpace(os.Getenv("DEPLOYMENTS_WORKSPACE_PATH")); workspacePath != "" {
		return workspacePath
	}
	return defaultWorkspacePath
}
