package bootstrap

import (
	"context"

	"github.com/faroshq/faros/pkg/bootstrap/templates/root"
	bootstraputils "github.com/faroshq/faros/pkg/util/bootstrap"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"
)

func (b *bootstrap) BootstrapRootWorkspace(ctx context.Context) error {
	klog.Info("Bootstrapping root workspace")
	targetRest, err := b.clientFactory.GetWorkspaceRestConfig(ctx, "root")
	if err != nil {
		return err
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(targetRest)
	if err != nil {
		return err
	}

	dynamicClient, err := dynamic.NewForConfig(targetRest)
	if err != nil {
		return err
	}

	return root.Bootstrap(ctx, discoveryClient, dynamicClient, bootstraputils.ReplaceOption())
}
