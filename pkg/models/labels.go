package models

const (
	// LabelOrganization is the label used to identify the organization
	LabelOrganization = "faros.sh/organization"
	// LabelWorkspace is the label used to identify the workspace
	LabelWorkspace = "faros.sh/workspace"
)

func LabelSelectorForOrganization(organization string) string {
	return LabelOrganization + "=" + organization
}
