package plugin

import (
	"context"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/faroshq/faros/pkg/cliplugins/base"
)

// DeleteOptions contains options for configuring faros
type DeleteOptions struct {
	*base.Options

	Name string
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
	farosClient, err := o.GetFarosClient()
	if err != nil {
		return err
	}

	path, err := url.JoinPath(o.TenantOrganizationsAPI, o.Name)
	if err != nil {
		return err
	}
	err = farosClient.RESTClient().Delete().AbsPath(path).Do(ctx).Error()
	if err != nil {
		return err
	}

	fmt.Println("Organization deleted successfully")
	return nil
}
