package plugin

import (
	"context"
	"net/url"

	"github.com/spf13/cobra"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	"github.com/faroshq/faros/pkg/cliplugins/base"
	utilprint "github.com/faroshq/faros/pkg/util/print"
)

// GetOptions contains options for configuring faros
type GetOptions struct {
	*base.Options
	Name string
}

// NewGetOptions returns a new GetOptions.
func NewGetOptions(streams genericclioptions.IOStreams) *GetOptions {
	return &GetOptions{
		Options: base.NewOptions(streams),
	}
}

// BindFlags binds fields GenerateOptions as command line flags to cmd's flagset.
func (o *GetOptions) BindFlags(cmd *cobra.Command) {
	o.Options.BindFlags(cmd)
}

// Complete ensures all dynamically populated fields are initialized.
func (o *GetOptions) Complete(args []string) error {
	if err := o.Options.Complete(); err != nil {
		return err
	}

	if o.Name == "" && len(args) > 0 {
		o.Name = args[0]
	}

	return nil
}

// Validate validates the SyncOptions are complete and usable.
func (o *GetOptions) Validate() error {
	var errs []error

	if err := o.Options.Validate(); err != nil {
		errs = append(errs, err)
	}

	return utilerrors.NewAggregate(errs)
}

// Run gets workspaces from tenant workspace api
func (o *GetOptions) Run(ctx context.Context) error {
	farosClient, err := o.GetFarosClient()
	if err != nil {
		return err
	}

	// TODO: Add o.Name check to get a single organization over list

	organizations := &tenancyv1alpha1.OrganizationList{}

	path, err := url.JoinPath(o.TenantOrganizationsAPI)
	if err != nil {
		return err
	}
	err = farosClient.RESTClient().Get().AbsPath(path).Do(ctx).Into(organizations)
	if err != nil {
		return err
	}

	if o.Output == utilprint.FormatTable {
		table := utilprint.DefaultTable()
		table.SetHeader([]string{"NAME", "DESCRIPTION", "STATUS", "AGE"})
		for _, organization := range organizations.Items {
			{
				status := "Unknown"
				if len(organization.Status.Conditions) > 0 {
					status = string(organization.Status.Conditions[0].Type)
				}

				table.Append([]string{
					organization.Name,
					organization.Spec.Description,
					status,
					utilprint.Since(organization.CreationTimestamp.Time).String()},
				)
			}
		}
		table.Render()
		return nil
	}

	return utilprint.PrintWithFormat(organizations, o.Output)
}
