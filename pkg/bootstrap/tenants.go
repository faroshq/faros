package bootstrap

import (
	"context"

	rootfarosservicestenants "github.com/faroshq/faros/pkg/bootstrap/templates/root-faros-services-tenants"
	bootstraputils "github.com/faroshq/faros/pkg/util/bootstrap"
	"github.com/kcp-dev/logicalcluster/v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"
)

func (b *bootstrap) BootstrapTenantsWorkspace(ctx context.Context) error {
	klog.Infof("Bootstrapping %s workspace", b.config.ControllersTenantWorkspace)
	targetRest, err := b.clientFactory.GetWorkspaceRestConfig(ctx, b.config.ControllersTenantWorkspace)
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

	clusterPath := logicalcluster.NewPath("root")
	exportTenancy, err := b.kcpClientSet.Cluster(clusterPath).ApisV1alpha1().APIExports().Get(ctx, "tenancy.kcp.io", metav1.GetOptions{})
	if err != nil {
		return err
	}

	return rootfarosservicestenants.Bootstrap(ctx, discoveryClient, dynamicClient, bootstraputils.ReplaceOption(
		"ROOT_TENANCY_IDENTITY", exportTenancy.Status.IdentityHash,
		"CONTROLLERS_TENANCY_IDENTITY", exportTenancy.Status.IdentityHash, // TODO: Why this identity is same?
	))
}
