package config

import "time"

const (
	ConfigFileName = "config.yaml"
)

type Config struct {
	API        API        `yaml:"api,omitempty"`
	Database   Database   `yaml:"database,omitempty"`
	Controller Controller `yaml:"controller,omitempty"`
}

type Controller struct {
	// SessionExpireInterval is the interval at which sessions are checked for expiration
	SessionExpireInterval time.Duration `envconfig:"FAROS_SESSION_EXPIRE_INTERVAL" default:"30s"`
	// SessionPurgeTTL is the time after which sessions are purged after expiration
	SessionPurgeTTL time.Duration `envconfig:"FAROS_SESSION_PURGE_TTL" default:"1h"`
}

type API struct {
	URI            string   `envconfig:"FAROS_API_URI" default:"localhost:8443"`
	AllowedOrigins []string `envconfig:"FAROS_API_ALLOWED_ORIGIN" default:""`
	TLSKeyPath     string   `envconfig:"FAROS_API_TLS_KEY" default:""`
	TLSCertPath    string   `envconfig:"FAROS_API_TLS_CERT" default:""`

	// loaded config after parsing
	TLSEnabled bool
	TLSKey     []byte `yaml:"-"`
	TLSCert    []byte `yaml:"-"`
}

type Database struct {
	SqliteURI string `envconfig:"FAROS_DATABASE_SQLITE_URI" default:"file::memory:?cache=shared"`
	Type      string `envconfig:"FAROS_DATABASE_TYPE" default:"sqlite" `
}
