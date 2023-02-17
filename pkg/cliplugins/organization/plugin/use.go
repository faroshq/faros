package plugin

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	"github.com/faroshq/faros/pkg/cliplugins/base"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// UseOptions contains options for configuring faros
type UseOptions struct {
	*base.Options
	Name string

	// for testing
	modifyConfig func(configAccess clientcmd.ConfigAccess, newConfig *clientcmdapi.Config) error
}

// NewUseOptions returns a new GetOptions.
func NewUseOptions(streams genericclioptions.IOStreams) *UseOptions {
	return &UseOptions{
		Options: base.NewOptions(streams),
		modifyConfig: func(configAccess clientcmd.ConfigAccess, newConfig *clientcmdapi.Config) error {
			return clientcmd.ModifyConfig(configAccess, *newConfig, true)
		},
	}
}

// BindFlags binds fields GenerateOptions as command line flags to cmd's flagset.
func (o *UseOptions) BindFlags(cmd *cobra.Command) {
	o.Options.BindFlags(cmd)
}

// Complete ensures all dynamically populated fields are initialized.
func (o *UseOptions) Complete(args []string) error {
	if err := o.Options.Complete(); err != nil {
		return err
	}

	if o.Name == "" && len(args) > 0 {
		o.Name = args[0]
	}

	return nil
}

// Validate validates the SyncOptions are complete and usable.
func (o *UseOptions) Validate() error {
	var errs []error

	if err := o.Options.Validate(); err != nil {
		errs = append(errs, err)
	}

	return utilerrors.NewAggregate(errs)
}

// Run gets workspace from tenant workspace api
func (o *UseOptions) Run(ctx context.Context) error {
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

	if o.Name == "" {
		if len(organizations.Items) == 0 {
			return fmt.Errorf("no organizations found")
		}
		o.Name = organizations.Items[0].Name
	} else {
		found := false
		for _, organization := range organizations.Items {
			if organization.Name == o.Name {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("organization %s not found", o.Name)
		}
	}

	// Get raw config and add new cluster and context to it
	rawConfig, err := o.ClientConfig.RawConfig()
	if err != nil {
		return err
	}

	rawConfig.Contexts[kubeConfigContextKeyOrg] = clientcmdapi.NewContext()
	rawConfig.Contexts[kubeConfigContextKeyOrg].Namespace = o.Name

	fmt.Printf("Using organization: %s \n ", o.Name)
	return o.modifyConfig(o.ClientConfig.ConfigAccess(), &rawConfig)
}
