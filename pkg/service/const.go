package service

import "path"

var (
	apiVersion = "v1alpha1"

	pathAPIVersion    = "/faros.sh/api/v1alpha1"
	organizations     = "/organizations"
	organizationArg   = "/{organization}"
	pathOrganizations = path.Join(pathAPIVersion, organizations)
	pathWorkspaces    = "/workspaces"
	workspaceArg      = "/{workspace}"

	pathOIDC     = path.Join(pathAPIVersion, "/oidc")
	oidcLogin    = "/login"
	oidcCallback = "/callback"
	oidcRegister = "/register"
)

var (
	limit int64 = 1024 * 1024 * 10
)
