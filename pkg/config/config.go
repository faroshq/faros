package config

const (
	ConfigFileName = "config.yaml"
)

type Config struct {
	API      API      `yaml:"api,omitempty"`
	Database Database `yaml:"database,omitempty"`
}

type Registry struct {
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
