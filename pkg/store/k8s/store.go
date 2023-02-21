package k8s

import (
	"context"

	farosclientset "github.com/faroshq/faros/pkg/client/clientset/versioned"
	"github.com/faroshq/faros/pkg/config"
	utilkubernetes "github.com/faroshq/faros/pkg/util/kubernetes"
)

type store struct {
	config         config.Config
	farosclientset *farosclientset.Clientset
}

func New(ctx context.Context, config config.Config) (*store, error) {
	cf, err := utilkubernetes.NewClientFactory(config.FarosKCPConfig.KCPClusterRestConfig)
	if err != nil {
		return nil, err
	}

	rest, err := cf.GetWorkspaceRestConfig(ctx, config.FarosKCPConfig.ControllersTenantWorkspace)
	if err != nil {
		return nil, err
	}

	client, err := farosclientset.NewForConfig(rest)
	if err != nil {
		return nil, err
	}

	return &store{
		config:         config,
		farosclientset: client,
	}, nil
}
