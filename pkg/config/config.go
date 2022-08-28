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
	// EncryptionKeys is the keys used for encrypting and decrypting secret fields
	// in the database. If one key fails to decrypt, second is used. Last key is used to seal the secrets
	EncryptionKeys []string `envconfig:"FAROS_ENCRYPTION_KEYS" default:"tDPRu/wtFeSRnnfU4rNXWKhvjq+H+pL+s6mU5+hH9XZmAxAIy8tUKN6fO4lbmBiSY6zSq0x/Zwf+a3X3DnbNCg=="`
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
