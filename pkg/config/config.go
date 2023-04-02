package config

import (
	"k8s.io/client-go/rest"
)

type Config struct {
	// APIConfig is the configuration for the API server.
	APIConfig APIConfig `yaml:"api"`
	// FarosKCPConfig is the configuration for the Faros KCP integration.
	FarosKCPConfig FarosKCPConfig `yaml:"farosKCP"`
	// SyncerConfig is the configuration for the syncer.
	SyncerConfig SyncerConfig `yaml:"syncer"`
}

type APIConfig struct {
	// Addr is the address to bind the controller to.
	Addr string `envconfig:"FAROS_API_ADDR" required:"true" default:":8443"`
	// ControllerExternalURL is the URL that the controller is externally reachable at.
	ControllerExternalURL string `envconfig:"FAROS_API_EXTERNAL_URL" required:"true" default:"https://api.faros.sh"`

	// AllowedCORSOrigins is a list of allowed CORS origins.
	AllowedCORSOrigins []string `envconfig:"FAROS_API_ALLOWED_CORS_ORIGINS" yaml:"allowedCORSOrigins,omitempty" default:"*"`

	// SkipTLSVerify disables TLS verification for all components. Used in dev
	SkipTLSVerify bool `envconfig:"FAROS_SKIP_TLS_VERIFY" yaml:"skipTLSVerify,omitempty" default:"false"`

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
	// Important: HostingClusterKubeConfigPath is used to move controllers (and in the future API)
	// kubeconfigs into hosting cluster for those components to run. Secrets are created part of
	// bootstrap process. Later on components dynamically load this kubeconfig and use it to
	// to start process. If this is not acceptable, bootstrap process can be split from runtime
	// and runtime part changed.

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
	ControllersTenantWorkspace string `envconfig:"FAROS_TENANT_WORKSPACE" yaml:"controllersTenantWorkspace,omitempty" default:"root:faros:tenants"`
	// ControllerKubeConfigSecretName is name of the secret for controller kubeconfig
	ControllerFarosConfigSecretName string `envconfig:"FAROS_CONTROLLER_FAROS_CONFIG_SECRET_NAME" yaml:"controllerFarosConfigSecretName,omitempty" default:"faros-controllers-config"`
	// ControllerClusterNameSecretKey is key of the secret for controller cluster name
	ControllerClusterNameSecretKey string `envconfig:"FAROS_CONTROLLER_CLUSTER_NAME_SECRET_KEY" yaml:"controllerClusterNameSecretKey,omitempty" default:"cluster-name"`
	// ControllerKubeConfigSecretKey is key of the secret for controller kubeconfig
	ControllerKubeConfigSecretKey string `envconfig:"FAROS_CONTROLLER_KUBECONFIG_SECRET_KEY" yaml:"controllerKubeConfigSecretKey,omitempty" default:"kubeconfig"`
	// ControllersOrganizationWorkspace is name of workspace for all organizations to be present in
	ControllersOrganizationWorkspace string `envconfig:"FAROS_ORGANIZATIONS_WORKSPACE" yaml:"controllersOrganizationWorkspace,omitempty" default:"root:faros-orgs"`
	// ControllersKubeConfigPath is path to kubeconfig for controllers
	ControllersKubeConfigPath string `envconfig:"FAROS_CONTROLLERS_KUBECONFIG" default:"controllers.kubeconfig"`
	// ControllersRestConfig is the rest config for the controllers cluster
	ControllersRestConfig *rest.Config `envconfig:"-"`
	// ControllersWorkspace is name of workspace controllers are operating in
	ControllersWorkspace string `envconfig:"FAROS_CONTROLLER_WORKSPACE" yaml:"controllersWorkspace,omitempty" default:"root:faros:controllers"`
	// ControllersClusterName is name of the cluster controllers are running in. Resolved from configMap in the kcp namespace which is created by bootstrap process
	// Should be mounted from configMap
	ControllersClusterName string `envconfig:"FAROS_CONTROLLERS_CLUSTER_NAME" yaml:"controllersClusterName,omitempty" default:""`

	// SkipTLSVerify disables TLS verification for all components. Used in dev
	SkipTLSVerify bool `envconfig:"FAROS_SKIP_TLS_VERIFY" yaml:"skipTLSVerify,omitempty" default:"false"`
}

type SyncerConfig struct {
	// Image is the image to use for the syncer.
	Image string `envconfig:"FAROS_SYNCER_IMAGE" required:"true" default:"ghcr.io/kcp-dev/kcp/syncer:v0.11.0"`
	// Replicas is the number of syncer replicas to run.
	Replicas int `envconfig:"FAROS_SYNCER_REPLICAS" default:"1"`
	// "Resources to synchronize with kcp, each resource should be in the format of resourcename.<gvr_of_the_resource>,"
	//	"e.g. to sync routes to physical cluster the resource name should be given as --resource routes.route.openshift.io")
	ResourceToSync []string `envconfig:"FAROS_SYNCER_RESOURCE_TO_SYNC" default:""`
	// QPS is the qps the syncer uses when talking to an apiserver.
	QPS float32 `envconfig:"FAROS_SYNCER_QPS" default:"20"`
	// Burst is the burst the syncer uses when talking to an apiserver.
	Burst int `envconfig:"FAROS_SYNCER_BURST" default:"30"`
	// FeatureGatesString is the set of features gates.
	FeatureGatesString string `envconfig:"FAROS_SYNCER_FEATURE_GATES" default:""`
	// APIImportPollInterval is the time interval to push apiimport.
	APIImportPollIntervalString string `envconfig:"FAROS_SYNCER_API_IMPORT_POLL_INTERVAL" default:"1m"`
	// DownstreamNamespaceCleanDelayString is the delay after which the syncer will delete a namespace in the downstream cluster.
	DownstreamNamespaceCleanDelayString string `envconfig:"FAROS_SYNCER_DOWNSTREAM_NAMESPACE_CLEAN_DELAY" default:"30s"`
}

func (c *APIConfig) AutoCertEnabled() bool {
	if c.TLSCertFile == "" && c.TLSKeyFile == "" {
		return true
	}
	return false
}
