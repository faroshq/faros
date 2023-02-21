package config

import (
	"time"

	"k8s.io/client-go/rest"
)

const (
	ConfigFileName = "config.yaml"
)

type Config struct {
	// APIConfig is the configuration for the API server.
	APIConfig APIConfig `yaml:"api"`
	// DatastoreConfig is the configuration for the datastore.
	DatastoreConfig DatastoreConfig `yaml:"datastore"`
	// FarosKCPConfig is the configuration for the Faros KCP integration.
	FarosKCPConfig FarosKCPConfig `yaml:"farosKCP"`
}

type APIConfig struct {
	// Addr is the address to bind the controller to.
	Addr string `envconfig:"FAROS_API_ADDR" required:"true" default:":8443"`
	// ControllerExternalURL is the URL that the controller is externally reachable at.
	ControllerExternalURL string `envconfig:"FAROS_API_EXTERNAL_URL" required:"true" default:"https://faros.dev.faros.sh"`

	// In prod we use auto-certs so this is not an issue.
	// TODO: Add support for auto-certs
	TLSKeyFile  string `envconfig:"FAROS_TLS_KEY_FILE" default:""`
	TLSCertFile string `envconfig:"FAROS_TLS_CERT_FILE" default:""`

	// OIDC provider configuration
	OIDCIssuerURL      string `envconfig:"FAROS_OIDC_ISSUER_URL" yaml:"oidcIssuerURL,omitempty" default:"https://dex.dev.faros.sh"`
	OIDCClientID       string `envconfig:"FAROS_OIDC_CLIENT_ID" yaml:"oidcClientID,omitempty" default:"faros"`
	OIDCClientSecret   string `envconfig:"FAROS_OIDC_CLIENT_SECRET" yaml:"oidcClientSecret,omitempty" default:"faros"`
	OIDCCASecretName   string `envconfig:"FAROS_OIDC_CA_SECRET_NAME" yaml:"oidcCASecretName,omitempty" default:"dex-pki-ca"`
	OIDCUsernameClaim  string `envconfig:"FAROS_OIDC_USERNAME_CLAIM" yaml:"oidcFarosUsernameClaim,omitempty" default:"email"`
	OIDCUserPrefix     string `envconfig:"FAROS_OIDC_USER_PREFIX" yaml:"oidcUserPrefix,omitempty" default:"faros-sso-"`
	OIDCGroupsPrefix   string `envconfig:"FAROS_OIDC_GROUPS_PREFIX" yaml:"oidcGroupsPrefix,omitempty" default:"faros-sso-"`
	OIDCAuthSessionKey string `envconfig:"FAROS_OIDC_AUTH_SESSION_KEY" yaml:"oidcAuthSessionKey,omitempty" default:""`
}

type FarosKCPConfig struct {
	// Important: HostingClusterKubeConfigPath is used to dynamically read secrets for trust. For now single secrets we
	// require in API server context is OIDC CA bundle from Dex. If removed this dependency, this can be
	// removed.
	// HostingClusterKubeConfig is the path to the kubeconfig file for the hosting cluster.
	HostingClusterKubeConfigPath string `envconfig:"FAROS_API_HOSTING_CLUSTER_KUBECONFIG" required:"true" default:"cluster.kubeconfig"`
	// HostingClusterNamespace is the namespace in the hosting cluster where the controller will run.
	HostingClusterNamespace string `envconfig:"FAROS_API_HOSTING_CLUSTER_NAMESPACE" required:"true" default:"kcp"`
	// HostingClusterRestConfig is the rest config for the hosting cluster.
	// Loaded from HostingClusterKubeConfig.
	HostingClusterRestConfig *rest.Config `envconfig:"-"`

	// KCPClusterKubeConfigPath is the path to the kubeconfig file for the kcp cluster
	KCPClusterKubeConfigPath string `envconfig:"FAROS_API_KCP_CLUSTER_KUBECONFIG" required:"true" default:"kcp.kubeconfig"`
	// KCPClusterRestConfig is the rest config for the KCP cluster.
	// Used to manage users, workspaces, etc
	KCPClusterRestConfig *rest.Config `envconfig:"-"`

	// ControllersTenantWorkspace is name of workspace for global tenant management. Used in service management
	// Must match one in Controllers config
	ControllersTenantWorkspace string `envconfig:"FAROS_TENANT_WORKSPACE" yaml:"controllersTenantWorkspace,omitempty" default:"root:faros:service:tenants"`

	// ControllersWorkspace is name of workspace controllers are operating in
	ControllersWorkspace string `envconfig:"FAROS_CONTROLLER_WORKSPACE" yaml:"controllersWorkspace,omitempty" default:"root:faros:service:controllers"`
}

type DatastoreConfig struct {
	SqliteURI string `envconfig:"FAROS_DATABASE_SQLITE_URI" default:"dev/database.sqlite3"`
	// Name of the database
	Name string `envconfig:"FAROS_DATABASE_NAME" default:"faros"`
	// Type is the type of database to use.
	Type string `envconfig:"FAROS_DATABASE_TYPE" default:"sqlite" `
	// Host is the host of the database
	Host string `envconfig:"FAROS_DATABASE_HOST" default:"localhost"`
	// Port is the port of the database
	Port int `envconfig:"FAROS_DATABASE_PORT" default:"5432"`
	// Password is the password of the database
	Password string `envconfig:"FAROS_DATABASE_PASSWORD" default:""`
	// Username is the username of the database
	Username string `envconfig:"FAROS_DATABASE_USERNAME" default:""`
	// MaxConnIdleTime is the maximum amount of time a database connection can be idle
	MaxConnIdleTime time.Duration `envconfig:"FAROS_DATABASE_MAX_CONN_IDLE_TIME" default:"30s"`
	//MaxConnLifeTime is the maximum amount of time a database connection can be used
	MaxConnLifeTime time.Duration `envconfig:"FAROS_DATABASE_MAX_CONN_LIFE_TIME" default:"1h"`
}
