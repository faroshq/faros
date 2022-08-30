package config

import (
	"os"

	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
)

// Load loads the configuration from the environment and flags
// Loading order:
// 1. Load .env file
// 2. Load envconfig from ENV variables and defaults
func Load() (*Config, error) {
	c := &Config{}
	// 1. Load .env file
	godotenv.Load()

	// 2. Load ENV and defaults
	err := envconfig.Process("", c)
	if err != nil {
		return c, err
	}

	// Load certs if provided
	if c.API.TLSKeyPath != "" && c.API.TLSCertPath != "" {
		c.API.TLSEnabled = true
		c.API.TLSKey, err = os.ReadFile(c.API.TLSKeyPath)
		if err != nil {
			return c, err
		}
		c.API.TLSCert, err = os.ReadFile(c.API.TLSCertPath)
		if err != nil {
			return c, err
		}
	}

	return c, err
}
