package plugin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	"github.com/faroshq/faros/pkg/cliplugins/base"
)

var (
	kubeConfigContextKeyOrg = "faros-org"
)

// GetOptions contains options for configuring faros workspaces
type CreateOptions struct {
	*base.Options
	Name        string
	Description string
}

// NewCreateOptions returns a new NewCreateOptions.
func NewCreateOptions(streams genericclioptions.IOStreams) *CreateOptions {
	return &CreateOptions{
		Options: base.NewOptions(streams),
	}
}

// BindFlags binds fields GenerateOptions as command line flags to cmd's flagset.
func (o *CreateOptions) BindFlags(cmd *cobra.Command) {
	o.Options.BindFlags(cmd)

	cmd.Flags().StringVarP(&o.Description, "description", "d", o.Description, "Description of the workspace")
}

// Complete ensures all dynamically populated fields are initialized.
func (o *CreateOptions) Complete(args []string) error {
	if err := o.Options.Complete(); err != nil {
		return err
	}

	if o.Name == "" && len(args) > 0 {
		o.Name = args[0]
	}

	raw, err := o.ClientConfig.RawConfig()
	if err != nil {
		panic(err)
	}

	if o.OrganizationName == "" {
		if ns, ok := raw.Contexts[kubeConfigContextKeyOrg]; ok {
			o.OrganizationName = ns.Namespace
		}
	}

	return nil
}

// Validate validates the Options are complete and usable.
func (o *CreateOptions) Validate() error {
	var errs []error

	if err := o.Options.Validate(); err != nil {
		errs = append(errs, err)
	}

	if o.Name == "" {
		errs = append(errs, fmt.Errorf("workspace name is required"))
	}

	return utilerrors.NewAggregate(errs)
}

// Run gets  from tenant workspace api
func (o *CreateOptions) Run(ctx context.Context) error {
	farosClient, err := o.GetFarosClient()
	if err != nil {
		return err
	}

	// Check organization exists
	organizations := tenancyv1alpha1.OrganizationList{}
	err = farosClient.RESTClient().Get().AbsPath(o.TenantOrganizationsAPI).Do(ctx).Into(&organizations)
	if err != nil {
		return err
	}

	if o.OrganizationName == "" {
		if len(organizations.Items) == 0 {
			return fmt.Errorf("no organizations found")
		}
		o.OrganizationName = organizations.Items[0].Name
	} else {
		found := false
		for _, organization := range organizations.Items {
			if organization.Name == o.OrganizationName {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("organization %s not found", o.OrganizationName)
		}
	}

	workspace := tenancyv1alpha1.Workspace{
		TypeMeta: metav1.TypeMeta{
			Kind:       tenancyv1alpha1.WorkspaceKind,
			APIVersion: tenancyv1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: o.Name,
		},
		Spec: tenancyv1alpha1.WorkspaceSpec{
			Description: o.Description,
		},
	}

	patch, err := json.Marshal(workspace)
	if err != nil {
		return fmt.Errorf("error creating patch: %v", err)
	}

	err = farosClient.RESTClient().Post().Body(patch).AbsPath(fmt.Sprintf(o.TenantWorkspacesAPIfmt, o.OrganizationName)).Do(ctx).Into(&workspace)
	if err != nil {
		return err
	}

	fmt.Printf("Workspace %s/%s created successfully \n", o.OrganizationName, o.Name)

	return nil
}
