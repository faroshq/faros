package config

import (
	"fmt"
	"net/url"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

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

	rest, err := loadKubeConfig(c.FarosKCPConfig.HostingClusterKubeConfigPath, false)
	if err != nil {
		return nil, err
	}
	c.FarosKCPConfig.HostingClusterRestConfig = rest

	rest, err = loadKubeConfig(c.FarosKCPConfig.KCPClusterKubeConfigPath, true)
	if err != nil {
		return nil, err
	}
	c.FarosKCPConfig.KCPClusterRestConfig = rest

	// best effort
	rest, err = loadKubeConfig(c.FarosKCPConfig.ControllersKubeConfigPath, false)
	if err != nil {
		klog.Infof("Failed to load controllers kubeconfig: %v", err)
		err = nil
	}
	c.FarosKCPConfig.ControllersRestConfig = rest

	return c, err
}

// loadKubeConfig loads a kubeconfig from disk or from the environment
func loadKubeConfig(kubeconfigPath string, dropPath bool) (*rest.Config, error) {
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

		rest, err := clientcmd.NewNonInteractiveClientConfig(*rawConfig, rawConfig.CurrentContext, nil, nil).ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to create client config: %w", err)
		}
		if dropPath {
			u, err := url.Parse(rest.Host)
			if err != nil {
				return nil, fmt.Errorf("failed to parse host: %w", err)
			}
			u.Path = ""
			rest.Host = u.String()
		}
		return rest, nil
	}

}
