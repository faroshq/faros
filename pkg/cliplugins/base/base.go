package base

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kcp-dev/kcp/pkg/cliplugins/base"
	"github.com/spf13/cobra"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	farosclient "github.com/faroshq/faros/pkg/client/clientset/versioned"
	"github.com/faroshq/faros/pkg/models"
	utilhttp "github.com/faroshq/faros/pkg/util/http"
	utilprint "github.com/faroshq/faros/pkg/util/print"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
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
	// APIEndpoint is the endpoint of the API server
	APIEndpoint string
	// OrganizationName is the name of the organization
	OrganizationName string

	ModifyConfig func(configAccess clientcmd.ConfigAccess, newConfig *clientcmdapi.Config) error
}

// NewOptions provides an instance of Options with default values.
func NewOptions(streams genericclioptions.IOStreams) *Options {
	return &Options{
		Options: base.NewOptions(streams),
		ModifyConfig: func(configAccess clientcmd.ConfigAccess, newConfig *clientcmdapi.Config) error {
			return clientcmd.ModifyConfig(configAccess, *newConfig, true)
		},
	}
}

// BindFlags binds options fields to cmd's flagset.
func (o *Options) BindFlags(cmd *cobra.Command) {
	o.Options.BindFlags(cmd)
	cmd.Flags().StringVarP(&o.Output, "output", "o", o.Output, "output format [table,json,yaml]")
	cmd.Flags().StringVarP(&o.APIEndpoint, "endpoint", "e", "https://api.faros.sh", "Faros API endpoint")
	cmd.Flags().StringVarP(&o.OrganizationName, "organization", "", "", "Name of the organization to which the workspace belongs to first")
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

	// Set organization
	if o.OrganizationName == "" {
		raw, err := o.ClientConfig.RawConfig()
		if err != nil {
			panic(err)
		}
		if raw.Clusters[tenancyv1alpha1.KubeConfigAuthKey] != nil &&
			raw.Clusters[tenancyv1alpha1.KubeConfigAuthKey].Extensions[tenancyv1alpha1.MetadataKey] != nil {
			unknownObj := raw.Clusters[tenancyv1alpha1.KubeConfigAuthKey].Extensions[tenancyv1alpha1.MetadataKey]
			obj, ok := unknownObj.(*runtime.Unknown)
			if !ok {
				return fmt.Errorf("failed to convert object to runtime.Unknown")
			}

			metadata := &tenancyv1alpha1.Metadata{}
			err = json.Unmarshal(obj.Raw, metadata)
			if err != nil {
				return err
			}

			o.OrganizationName = metadata.Spec.CurrentOrganization
		}
	}

	switch o.Output {
	case utilprint.FormatJSON, utilprint.FormatYAML, utilprint.FormatTable, utilprint.FormatJSONStream:
		return nil
	default:
		return fmt.Errorf("invalid output format: %s", o.Output)
	}
}

// Validate validates the configured options.
func (o *Options) Validate() error {
	return nil
}

func (o *Options) GetFarosClient() (*farosclient.Clientset, error) {
	// check if we need to refresh the token, else tell to login
	err := o.RefreshToken()
	if err != nil {
		return nil, err
	}

	config, err := o.ClientConfig.ClientConfig()
	if err != nil {
		return nil, err
	}

	raw, err := o.ClientConfig.RawConfig()
	if err != nil {
		return nil, err
	}

	cluster := raw.Clusters[v1alpha1.KubeConfigAuthKey]

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

func (o *Options) RefreshToken() error {
	config, err := o.ClientConfig.RawConfig()
	if err != nil {
		return err
	}

	cluster := config.Clusters[v1alpha1.KubeConfigAuthKey]
	unknownObj := config.Clusters[v1alpha1.KubeConfigAuthKey].Extensions[v1alpha1.MetadataKey]
	obj, ok := unknownObj.(*runtime.Unknown)
	if !ok {
		return fmt.Errorf("failed to convert object to runtime.Unknown")
	}

	metadata := &tenancyv1alpha1.Metadata{}
	err = json.Unmarshal(obj.Raw, metadata)
	if err != nil {
		return err
	}

	// Auto refresh token each 6 hours
	if !time.Now().Add(+time.Hour * 6).After(time.Unix(int64(metadata.Spec.ExpiresAt), 0)) {
		return nil
	}

	if time.Now().After(time.Unix(int64(metadata.Spec.ExpiresAt), 0)) {
		return fmt.Errorf("token expired, please login again")
	}

	u, err := url.Parse(cluster.Server)
	if err != nil {
		return err
	}

	var client *http.Client
	if strings.Contains(u.Host, "dev") { // TODO: remove this
		client = utilhttp.GetInsecureClient()
	} else {
		client = &http.Client{}
	}

	response, err := client.Post(fmt.Sprintf("https://%s/faros.sh/api/v1alpha1/oidc/callback?refresh_token=%s", u.Host, metadata.Spec.RefreshToken), "application/json", nil)
	if err != nil {
		return err
	}

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to refresh token: %s", response.Status)
	}

	var results models.LoginResponse
	err = json.NewDecoder(response.Body).Decode(&results)
	if err != nil {
		return err
	}

	config.Clusters[v1alpha1.KubeConfigAuthKey] = &clientcmdapi.Cluster{
		Server:                   results.ServerBaseURL,
		InsecureSkipTLSVerify:    cluster.InsecureSkipTLSVerify,
		CertificateAuthority:     cluster.CertificateAuthority,
		CertificateAuthorityData: cluster.CertificateAuthorityData,
		Extensions: map[string]runtime.Object{
			v1alpha1.MetadataKey: &v1alpha1.Metadata{
				TypeMeta: metav1.TypeMeta{
					Kind: v1alpha1.MetadataKind,
				},
				Spec: v1alpha1.MetadataSpec{
					AccessToken:         results.AccessToken,
					RefreshToken:        results.RefreshToken,
					ExpiresAt:           results.ExpiresAt,
					CurrentOrganization: metadata.Spec.CurrentOrganization,
				},
			},
		},
	}

	return o.ModifyConfig(o.ClientConfig.ConfigAccess(), &config)
}
