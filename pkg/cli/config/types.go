package config

import (
	"github.com/faroshq/faros/pkg/client"
	"github.com/sirupsen/logrus"
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
