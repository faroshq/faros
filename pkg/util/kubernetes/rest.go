package kubernetes

import (
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	kapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/tools/clientcmd/api/latest"
	v1 "k8s.io/client-go/tools/clientcmd/api/v1"
)

// NewRestConfig returns restconfig, based on configuration
func NewRestConfig(log *logrus.Entry, config *v1.Config) (*rest.Config, error) {
	restconfig, err := RestConfigFromV1Config(config)
	if err != nil {
		return nil, err
	}

	return restconfig, nil
}

// RestConfigFromV1Config takes a v1 config and returns a kubeconfig
func RestConfigFromV1Config(kc *v1.Config) (*rest.Config, error) {
	var c kapi.Config
	err := latest.Scheme.Convert(kc, &c, nil)
	if err != nil {
		return nil, err
	}

	kubeconfig := clientcmd.NewDefaultClientConfig(c, &clientcmd.ConfigOverrides{})
	return kubeconfig.ClientConfig()
}
