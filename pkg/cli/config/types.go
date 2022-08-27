package config

import (
	"github.com/sirupsen/logrus"

	"github.com/faroshq/faros/pkg/client"
)

type GlobalConfig struct {
	LogLevel string
	Output   string
	WorkDir  string

	APIEndpoint string
	Namespace   string

	APIClient             *client.Client
	InsecureSkipTLSVerify bool
	Log                   *logrus.Entry
}

// ResourceIDsConfig is used to inject resource IDs into the config
// when user providers them as names instead of IDs.
type ResourceIDsConfig struct {
	Namespace            string
	Cluster              string
	ClusterAccessSession string
}
