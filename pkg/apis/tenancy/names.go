package tenancy

import (
	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
)

func GetWorkspaceName(org tenancyv1alpha1.Organization, workspace tenancyv1alpha1.Workspace) string {
	return org.Name + "-" + workspace.Name
}
