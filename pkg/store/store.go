package store

import (
	"context"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
)

type Store interface {
	GetUser(context.Context, tenancyv1alpha1.User) (*tenancyv1alpha1.User, error)
	ListUsers(context.Context, tenancyv1alpha1.User) (*tenancyv1alpha1.UserList, error)
	DeleteUser(context.Context, tenancyv1alpha1.User) error
	CreateUser(context.Context, tenancyv1alpha1.User) (*tenancyv1alpha1.User, error)
	UpdateUser(context.Context, tenancyv1alpha1.User) (*tenancyv1alpha1.User, error)

	GetWorkspace(context.Context, tenancyv1alpha1.Workspace) (*tenancyv1alpha1.Workspace, error)
	ListWorkspaces(context.Context, string, tenancyv1alpha1.Workspace) (*tenancyv1alpha1.WorkspaceList, error)
	DeleteWorkspace(context.Context, tenancyv1alpha1.Workspace) error
	CreateWorkspace(context.Context, tenancyv1alpha1.Workspace) (*tenancyv1alpha1.Workspace, error)
	UpdateWorkspace(context.Context, tenancyv1alpha1.Workspace) (*tenancyv1alpha1.Workspace, error)

	GetOrganization(context.Context, tenancyv1alpha1.Organization) (*tenancyv1alpha1.Organization, error)
	ListOrganizations(context.Context, tenancyv1alpha1.Organization) (*tenancyv1alpha1.OrganizationList, error)
	DeleteOrganization(context.Context, tenancyv1alpha1.Organization) error
	CreateOrganization(context.Context, tenancyv1alpha1.Organization) (*tenancyv1alpha1.Organization, error)
	UpdateOrganization(context.Context, tenancyv1alpha1.Organization) (*tenancyv1alpha1.Organization, error)
}
