package config

import (
	"fmt"
	"os"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"

	"k8s.io/client-go/tools/clientcmd"

	utilkubernetes "github.com/faroshq/faros/pkg/util/kubernetes"
)

// LoadController loads the configuration from the environment and flags
// Loading order:
// 1. Load .env file
// 2. Load envconfig from ENV variables and defaults
func LoadAPI() (*Config, error) {
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

	hostingKubeConfig, err := loadKubeConfig(c.FarosKCPConfig.HostingClusterKubeConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load hosting cluster kubeconfig: %w", err)
	}

	c.FarosKCPConfig.HostingClusterRestConfig, err = hostingKubeConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load hosting cluster rest config: %w", err)
	}

	kcpKubeConfig, err := loadKubeConfig(c.FarosKCPConfig.KCPClusterKubeConfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load kcp cluster kubeconfig: %w", err)
	}

	kcpRest, err := kcpKubeConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to load kcp cluster rest config: %w", err)
	}

	// Sanitize as we don't know what user did in the kubeconfig
	// TODO: Clean duplicate code
	cf, err := utilkubernetes.NewClientFactory(kcpRest)
	if err != nil {
		return nil, err
	}

	rest, err := cf.GetRootRestConfig()
	if err != nil {
		return nil, err
	}

	c.FarosKCPConfig.KCPClusterRestConfig = rest

	return c, err
}

// loadKubeConfig loads a kubeconfig from disk. This method is
// intended to be common between fixture for servers whose lifecycle
// is test-managed and fixture for servers whose lifecycle is managed
// separately from a test run.
func loadKubeConfig(kubeconfigPath string) (clientcmd.ClientConfig, error) {
	fs, err := os.Stat(kubeconfigPath)
	if err != nil {
		return nil, err
	}
	if fs.Size() == 0 {
		return nil, fmt.Errorf("%s points to an empty file", kubeconfigPath)
	}

	rawConfig, err := clientcmd.LoadFromFile(kubeconfigPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load admin kubeconfig: %w", err)
	}

	return clientcmd.NewNonInteractiveClientConfig(*rawConfig, rawConfig.CurrentContext, nil, nil), nil
}
