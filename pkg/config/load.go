package config

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	utilfile "github.com/faroshq/faros/pkg/util/file"
)

// LoadController loads the configuration from the environment and flags
// Loading order:
// 1. Load .env file
// 2. Load envconfig from ENV variables and defaults
func Load() (*Config, error) {
	c := &Config{}
	godotenv.Load()

	err := envconfig.Process("", c)
	if err != nil {
		return c, err
	}

	if c.APIConfig.OIDCAuthSessionKey == "" {
		fmt.Println("FAROS_OIDC_AUTH_SESSION_KEY not supplied, generating random one")
		c.APIConfig.OIDCAuthSessionKey = uuid.Must(uuid.NewUUID()).String()
	}

	rest, err := loadKubeConfig(c.FarosKCPConfig.HostingClusterKubeConfigPath)
	if err != nil {
		return nil, err
	}
	c.FarosKCPConfig.HostingClusterRestConfig = rest

	rest, err = loadKubeConfig(c.FarosKCPConfig.KCPClusterKubeConfigPath)
	if err != nil {
		return nil, err
	}
	c.FarosKCPConfig.KCPClusterRestConfig = rest

	return c, err
}

// loadKubeConfig loads a kubeconfig from disk or from the environment
func loadKubeConfig(kubeconfigPath string) (*rest.Config, error) {
	exists, _ := utilfile.Exist(kubeconfigPath)
	if !exists {
		config, err := clientcmd.BuildConfigFromFlags("", "")
		if err != nil {
			return nil, err
		}
		return config, nil
	} else {
		rawConfig, err := clientcmd.LoadFromFile(kubeconfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load admin kubeconfig: %w", err)
		}

		return clientcmd.NewNonInteractiveClientConfig(*rawConfig, rawConfig.CurrentContext, nil, nil).ClientConfig()
	}

}
