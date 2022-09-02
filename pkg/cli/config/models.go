package config

import (
	"github.com/sirupsen/logrus"

	"github.com/faroshq/faros/pkg/client"
)

// GlobalConfig is the global configuration for CLI
// IMPORTANT: Flags names in AppendGlobalFlags must match yaml keys in config.yaml!!!
// Otherwise viper looses those values
type GlobalConfig struct {
	LogLevel                  string `yaml:"loglevel,omitempty"`
	Output                    string `yaml:"output,omitempty"`
	WorkDir                   string `yaml:"work-dir,omitempty"`
	DefaultKubeConfigLocation string `yaml:"default-kubeconfig,omitempty"`
	KubeConfigMode            string `yaml:"kubeconfig-mint-mode,omitempty"`
	APIEndpoint               string `yaml:"api-endpoint,omitempty"`
	Namespace                 string `yaml:"namespace,omitempty"`
	InsecureSkipTLSVerify     bool   `yaml:"insecure-skip-tls-verify,omitempty"`
	Username                  string `yaml:"username,omitempty"`
	Password                  string `yaml:"password,omitempty"`

	APIClient *client.Client `yaml:"-"`
	Log       *logrus.Entry  `yaml:"-"`
}
