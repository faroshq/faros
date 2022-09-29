package config

import "time"

const (
	ConfigFileName = "config.yaml"
)

type ServerConfig struct {
	API        API        `yaml:"api,omitempty"`
	Database   Database   `yaml:"database,omitempty"`
	Controller Controller `yaml:"controller,omitempty"`
}

type AgentConfig struct {
	// ServerURI is the URI of the server to connect to
	ServerURI string `envconfig:"FAROS_SERVER_URI" yaml:"serverURI,omitempty"`
	// AccessKey is the access key used to authenticate with the server
	AccessKey string `envconfig:"FAROS_ACCESS_KEY" yaml:"accessKey,omitempty"`
	// ClusterID is the ID of the cluster this agent is running on
	ClusterID string `envconfig:"FAROS_CLUSTER_ID" yaml:"clusterID,omitempty"`
	// AccessID is the ID of the access object
	AccessID string `envconfig:"FAROS_ACCESS_ID" yaml:"accessID,omitempty"`
	// NamespaceID is the ID of the namespace this agent is running in
	NamespaceID string `envconfig:"FAROS_NAMESPACE_ID" yaml:"namespaceID,omitempty"`
}

type Controller struct {
	// SessionExpireInterval is the interval at which sessions are checked for expiration
	SessionExpireInterval time.Duration `envconfig:"FAROS_SESSION_EXPIRE_INTERVAL" default:"30s"`
	// SessionPurgeTTL is the time after which sessions are purged after expiration
	SessionPurgeTTL time.Duration `envconfig:"FAROS_SESSION_PURGE_TTL" default:"1h"`
	// EncryptionKeys is the keys used for encrypting and decrypting secret fields
	// in the database. If one key fails to decrypt, second is used. Last key is used to seal the secrets
	EncryptionKeys []string `envconfig:"FAROS_ENCRYPTION_KEYS" default:"tDPRu/wtFeSRnnfU4rNXWKhvjq+H+pL+s6mU5+hH9XZmAxAIy8tUKN6fO4lbmBiSY6zSq0x/Zwf+a3X3DnbNCg=="`
	// CloudRefreshInterval is the interval at which cloud resources are refreshed
	CloudRefreshInterval time.Duration `envconfig:"FAROS_CLOUD_REFRESH_INTERVAL" default:"30s"`
	// AzureCredentials is the credentials used to authenticate with Azure
	AzureCredentials AzureCredentials `yaml:"azure_credentials,omitempty"`
}

type API struct {
	URI                     string   `envconfig:"FAROS_API_URI" default:"0.0.0.0:8443"`
	AllowedOrigins          []string `envconfig:"FAROS_API_ALLOWED_ORIGIN" default:""`
	TLSKeyPath              string   `envconfig:"FAROS_API_TLS_KEY" default:"/faros-secrets/localhost.key"`
	TLSCertPath             string   `envconfig:"FAROS_API_TLS_CERT" default:"/faros-secrets/localhost.crt"`
	AuthenticationProviders []string `envconfig:"FAROS_API_AUTHENTICATION_PROVIDERS" default:"basicauth"`

	BasicAuthAuthenticationProviderFile string `envconfig:"FAROS_API_BASICAUTH_AUTHENTICATION_PROVIDER_FILE" default:"/faros-secrets/htpasswd"`

	// loaded config after parsing
	TLSEnabled bool
	TLSKey     []byte `yaml:"-"`
	TLSCert    []byte `yaml:"-"`
}

type Database struct {
	SqliteURI string `envconfig:"FAROS_DATABASE_SQLITE_URI" default:"file::memory:?cache=shared"`
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

// AzureCredentials contains the credentials for Azure
type AzureCredentials struct {
	SubscriptionID string `envconfig:"AZURE_SUBSCRIPTION_ID" default:""`
	TenantID       string `envconfig:"AZURE_TENANT_ID" default:""`
	ClientID       string `envconfig:"AZURE_CLIENT_ID" default:""`
	ClientSecret   string `envconfig:"AZURE_CLIENT_SECRET" default:""`
}
