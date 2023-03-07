package plugin

import (
	"context"
	"fmt"
	"net/url"

	"github.com/spf13/cobra"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	"github.com/faroshq/faros/pkg/cliplugins/base"
	utilprint "github.com/faroshq/faros/pkg/util/print"
	"github.com/faroshq/faros/pkg/util/rest"
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

	// Check organization exists
	organizations := tenancyv1alpha1.OrganizationList{}
	err = rest.ContentTypeJSON(farosClient.RESTClient().Get()).AbsPath(o.TenantOrganizationsAPI).Do(ctx).Into(&organizations)
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

	path, err := url.JoinPath(fmt.Sprintf(o.TenantWorkspacesAPIfmt, o.OrganizationName))
	if err != nil {
		return err
	}
	workspaces := &tenancyv1alpha1.WorkspaceList{}
	err = rest.ContentTypeJSON(farosClient.RESTClient().Get()).AbsPath(path).Do(ctx).Into(workspaces)
	if err != nil {
		return err
	}

	if o.Output == utilprint.FormatTable {
		table := utilprint.DefaultTable()
		table.SetHeader([]string{"NAME", "ORGANIZATION", "DESCRIPTION", "STATUS", "AGE"})
		for _, workspace := range workspaces.Items {
			{
				status := "Unknown"
				if len(workspace.Status.Conditions) > 0 {
					status = string(workspace.Status.Conditions[0].Type)
				}

				table.Append([]string{
					workspace.Name,
					workspace.Spec.OrganizationRef.Name,
					workspace.Spec.Description,
					status,
					utilprint.Since(workspace.CreationTimestamp.Time).String()},
				)
			}
		}
		table.Render()
		return nil
	}

	return utilprint.PrintWithFormat(workspaces, o.Output)
}
