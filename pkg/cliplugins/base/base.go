package base

import (
	"fmt"
	"net/url"

	"github.com/kcp-dev/kcp/pkg/cliplugins/base"
	"github.com/spf13/cobra"

	"k8s.io/cli-runtime/pkg/genericclioptions"

	farosclient "github.com/faroshq/faros/pkg/client/clientset/versioned"
	utilprint "github.com/faroshq/faros/pkg/util/print"
)

// Options contains options common to most CLI plugins, including settings for connecting to faros
type Options struct {
	*base.Options
	// Output specifies output format
	Output string

	// TenantOrganizationsAPI is the API path for organizations
	TenantOrganizationsAPI string

	// TenantWorkspacesAPIfmt is the API path for workspaces
	TenantWorkspacesAPIfmt string
}

// NewOptions provides an instance of Options with default values.
func NewOptions(streams genericclioptions.IOStreams) *Options {
	return &Options{
		Options: base.NewOptions(streams),
	}
}

// BindFlags binds options fields to cmd's flagset.
func (o *Options) BindFlags(cmd *cobra.Command) {
	o.Options.BindFlags(cmd)
	cmd.Flags().StringVarP(&o.Output, "output", "o", o.Output, "output format [table,json,yaml]")
}

// Complete initializes ClientConfig based on Kubeconfig and KubectlOverrides.
func (o *Options) Complete() error {
	if err := o.Options.Complete(); err != nil {
		return err
	}
	if o.Output == "" {
		o.Output = utilprint.FormatTable
	}

	o.TenantOrganizationsAPI = "/faros.sh/api/v1alpha1/organizations"
	o.TenantWorkspacesAPIfmt = "/faros.sh/api/v1alpha1/organizations/%s/workspaces"

	switch o.Output {
	case utilprint.FormatJSON, utilprint.FormatYAML, utilprint.FormatTable, utilprint.FormatJSONStream:
		return nil
	default:
		return fmt.Errorf("invalid output format: %s", o.Output)
	}
}

var kubeConfigAuthKey = "faros"

// Validate validates the configured options.
func (o *Options) Validate() error {
	return nil
}

func (o *Options) GetFarosClient() (*farosclient.Clientset, error) {
	config, err := o.ClientConfig.ClientConfig()
	if err != nil {
		return nil, err
	}

	raw, err := o.ClientConfig.RawConfig()
	if err != nil {
		return nil, err
	}

	cluster := raw.Clusters[kubeConfigAuthKey]

	u, err := url.Parse(cluster.Server)
	if err != nil {
		return nil, err
	}
	config.Host = u.Host

	farosClient, err := farosclient.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return farosClient, nil
}
