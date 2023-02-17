package plugin

import (
	"context"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	farosclient "github.com/faroshq/faros/pkg/client/clientset/versioned"
	"github.com/faroshq/faros/pkg/cliplugins/base"
)

// DeleteOptions contains options for configuring faros
type DeleteOptions struct {
	*base.Options

	Name             string
	OrganizationName string
}

// NewGetOptions returns a new GetOptions.
func NewDeleteOptions(streams genericclioptions.IOStreams) *DeleteOptions {
	return &DeleteOptions{
		Options: base.NewOptions(streams),
	}
}

// BindFlags binds fields GenerateOptions as command line flags to cmd's flagset.
func (o *DeleteOptions) BindFlags(cmd *cobra.Command) {
	o.Options.BindFlags(cmd)

	cmd.Flags().StringVarP(&o.OrganizationName, "organization", "", o.OrganizationName, "Name of the organization to which the workspace belongs to first.")
}

// Complete ensures all dynamically populated fields are initialized.
func (o *DeleteOptions) Complete(args []string) error {
	if err := o.Options.Complete(); err != nil {
		return err
	}

	if o.Name == "" && len(args) > 0 {
		o.Name = args[0]
	}

	return nil
}

// Validate validates the SyncOptions are complete and usable.
func (o *DeleteOptions) Validate() error {
	var errs []error

	if err := o.Options.Validate(); err != nil {
		errs = append(errs, err)
	}

	return utilerrors.NewAggregate(errs)
}

// Run gets workspaces from tenant workspace api
func (o *DeleteOptions) Run(ctx context.Context) error {
	config, err := o.ClientConfig.ClientConfig()
	if err != nil {
		return err
	}

	u, err := url.Parse(config.Host)
	if err != nil {
		return err
	}
	config.Host = u.Host

	farosClient, err := farosclient.NewForConfig(config)
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

	path, err := url.JoinPath(fmt.Sprintf(o.TenantWorkspacesAPIfmt, o.OrganizationName), o.Name)
	if err != nil {
		return err
	}

	err = farosClient.RESTClient().Delete().AbsPath(path).Do(ctx).Error()
	if err != nil {
		return err
	}

	fmt.Println("Workspace deleted successfully")
	return nil
}
