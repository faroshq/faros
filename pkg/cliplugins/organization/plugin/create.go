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

var kubeConfigAuthKey = "faros"

// GetOptions contains options for configuring faros workspaces
type CreateOptions struct {
	*base.Options
	Name        string
	Description string
	Members     []string
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

	cmd.Flags().StringVarP(&o.Description, "description", "d", o.Description, "Description of the organization")

}

// Complete ensures all dynamically populated fields are initialized.
func (o *CreateOptions) Complete(args []string) error {
	if err := o.Options.Complete(); err != nil {
		return err
	}

	if o.Name == "" && len(args) > 0 {
		o.Name = args[0]
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
		errs = append(errs, fmt.Errorf("organization name is required"))
	}

	return utilerrors.NewAggregate(errs)
}

// Run gets  from tenant workspace api
func (o *CreateOptions) Run(ctx context.Context) error {
	farosClient, err := o.GetFarosClient()
	if err != nil {
		return err
	}

	organization := tenancyv1alpha1.Organization{
		TypeMeta: metav1.TypeMeta{
			Kind:       tenancyv1alpha1.OrganizationKind,
			APIVersion: tenancyv1alpha1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: o.Name,
		},
		Spec: tenancyv1alpha1.OrganizationSpec{
			Description: o.Description,
		},
	}

	patch, err := json.Marshal(organization)
	if err != nil {
		return fmt.Errorf("error creating patch: %v", err)
	}

	err = farosClient.RESTClient().Post().Body(patch).AbsPath(o.TenantOrganizationsAPI).Do(ctx).Into(&organization)
	if err != nil {
		return err
	}

	return nil
}
