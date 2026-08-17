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

var instanceResources = []string{"applications", "simplewebapps", "workers"}

func deploymentClaims(identityHash string) ([]sdkinstall.PermissionClaim, error) {
	hash := strings.TrimSpace(identityHash)
	if hash == "" {
		return nil, fmt.Errorf("DEPLOYMENTS_INFRA_IDENTITY_HASH is required")
	}
	claims := []sdkinstall.PermissionClaim{{Group: "infrastructure.faros.sh", Resource: "templates", Verbs: []string{"get", "list", "watch"}, IdentityHash: hash}}
	for _, resource := range instanceResources {
		claims = append(claims, sdkinstall.PermissionClaim{Group: "infrastructure.faros.sh", Resource: resource, Verbs: []string{"get", "list", "watch", "create", "update", "patch", "delete"}, IdentityHash: hash})
	}
	return claims, nil
}

func runInitCmd(ctx context.Context) error {
	config, err := loadControllerConfig()
	if err != nil {
		return err
	}
	claims, err := deploymentClaims(os.Getenv("DEPLOYMENTS_INFRA_IDENTITY_HASH"))
	if err != nil {
		return err
	}
	workspacePath := os.Getenv("DEPLOYMENTS_WORKSPACE_PATH")
	if workspacePath == "" {
		workspacePath = defaultWorkspacePath
	}
	schemasDir := os.Getenv("FAROS_SCHEMAS_DIR")
	if schemasDir == "" {
		schemasDir = "/etc/faros/schemas"
	}
	return sdkinstall.Bootstrap(ctx, sdkinstall.Options{Config: config, ExportName: apiExportName, WorkspacePath: workspacePath, SchemasDir: schemasDir, Claims: claims, CatalogEntryFile: os.Getenv("FAROS_CATALOGENTRY_FILE")})
}
