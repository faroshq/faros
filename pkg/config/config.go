package config

import (
	"k8s.io/client-go/rest"
)

const (
	ConfigFileName = "config.yaml"
)

type Config struct {
	// APIConfig is the configuration for the API server.
	APIConfig APIConfig `yaml:"api"`
	// FarosKCPConfig is the configuration for the Faros KCP integration.
	FarosKCPConfig FarosKCPConfig `yaml:"farosKCP"`
}

type APIConfig struct {
	// Addr is the address to bind the controller to.
	Addr string `envconfig:"FAROS_API_ADDR" required:"true" default:":8443"`
	// ControllerExternalURL is the URL that the controller is externally reachable at.
	ControllerExternalURL string `envconfig:"FAROS_API_EXTERNAL_URL" required:"true" default:"https://api.faros.sh"`

	// AllowedCORSOrigins is a list of allowed CORS origins.
	AllowedCORSOrigins []string `envconfig:"FAROS_API_ALLOWED_CORS_ORIGINS" yaml:"allowedCORSOrigins,omitempty" default:"*"`

	// In prod we use auto-certs so this is not an issue.
	// TODO: Add support for auto-certs
	TLSKeyFile  string `envconfig:"FAROS_TLS_KEY_FILE" default:""`
	TLSCertFile string `envconfig:"FAROS_TLS_CERT_FILE" default:""`

	// OIDC provider configuration
	OIDCIssuerURL         string `envconfig:"FAROS_OIDC_ISSUER_URL" yaml:"oidcIssuerURL,omitempty" default:"https://dex.faros.sh"`
	OIDCClientID          string `envconfig:"FAROS_OIDC_CLIENT_ID" yaml:"oidcClientID,omitempty" default:"faros"`
	OIDCClientSecret      string `envconfig:"FAROS_OIDC_CLIENT_SECRET" yaml:"oidcClientSecret,omitempty" default:"faros"`
	OIDCCASecretNamespace string `envconfig:"FAROS_OIDC_CA_SECRET_NAMESPACE" yaml:"oidcCASecretNamespace,omitempty" default:"dex"`
	OIDCCASecretName      string `envconfig:"FAROS_OIDC_CA_SECRET_NAME" yaml:"oidcCASecretName,omitempty" default:"dex-pki-ca"`
	OIDCUsernameClaim     string `envconfig:"FAROS_OIDC_USERNAME_CLAIM" yaml:"oidcFarosUsernameClaim,omitempty" default:"email"`
	OIDCUserPrefix        string `envconfig:"FAROS_OIDC_USER_PREFIX" yaml:"oidcUserPrefix,omitempty" default:"faros-sso-"`
	OIDCGroupsPrefix      string `envconfig:"FAROS_OIDC_GROUPS_PREFIX" yaml:"oidcGroupsPrefix,omitempty" default:"faros-sso-"`
	OIDCAuthSessionKey    string `envconfig:"FAROS_OIDC_AUTH_SESSION_KEY" yaml:"oidcAuthSessionKey,omitempty" default:""`
}

type FarosKCPConfig struct {
	// Important: HostingClusterKubeConfigPath is used to dynamically read secrets for trust. For now single secrets we
	// require in API server context is OIDC CA bundle from Dex. If removed this dependency, this can be
	// removed.
	// HostingClusterKubeConfig is the path to the kubeconfig file for the hosting cluster.
	HostingClusterKubeConfigPath string `envconfig:"FAROS_HOSTING_CLUSTER_KUBECONFIG" default:"cluster.kubeconfig"`
	// HostingClusterNamespace is the namespace in the hosting cluster where the controller will run.
	HostingClusterNamespace string `envconfig:"FAROS_HOSTING_CLUSTER_NAMESPACE" required:"true" default:"kcp"`
	// HostingClusterRestConfig is the rest config for the hosting cluster.
	// Loaded from HostingClusterKubeConfig.
	HostingClusterRestConfig *rest.Config `envconfig:"-"`

	// KCPClusterKubeConfigPath is the path to the kubeconfig file for the kcp cluster
	KCPClusterKubeConfigPath string `envconfig:"FAROS_KCP_CLUSTER_KUBECONFIG" required:"true" default:"kcp.kubeconfig"`
	// KCPClusterRestConfig is the rest config for the KCP cluster.
	// Used to manage users, workspaces, etc
	KCPClusterRestConfig *rest.Config `envconfig:"-"`

	// ControllersTenantWorkspace is name of workspace for global tenant management. Used in service management
	// Must match one in Controllers config
	ControllersTenantWorkspace string `envconfig:"FAROS_TENANT_WORKSPACE" yaml:"controllersTenantWorkspace,omitempty" default:"root:faros:service:tenants"`

	// ControllersWorkspace is name of workspace controllers are operating in
	ControllersWorkspace string `envconfig:"FAROS_CONTROLLER_WORKSPACE" yaml:"controllersWorkspace,omitempty" default:"root:faros:service:controllers"`
}

func (c *APIConfig) AutoCertEnabled() bool {
	if c.TLSCertFile == "" && c.TLSKeyFile == "" {
		return true
	}
	return false
}
