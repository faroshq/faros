package bootstrap

import (
	"context"

	"github.com/kcp-dev/client-go/kubernetes"
	kcpclient "github.com/kcp-dev/kcp/pkg/client/clientset/versioned/cluster"
	clientgokubernetes "k8s.io/client-go/kubernetes"

	"github.com/faroshq/faros/pkg/config"
	utilkubernetes "github.com/faroshq/faros/pkg/util/kubernetes"
)

const (
	workspaceClusterAnnotationKey = "internal.tenancy.kcp.io/cluster"
)

type Bootstraper interface {
	Bootstrap(ctx context.Context)

	CreateWorkspace(ctx context.Context, name string) error
	BootstrapRootWorkspace(ctx context.Context) error
	BootstrapOrganizationsWorkspace(ctx context.Context) error
	BootstrapControllersWorkspace(ctx context.Context) error
	BootstrapTenantsWorkspace(ctx context.Context) error

	//BootstrapServiceTenantAssets(ctx context.Context) error
	//BootstrapServiceWorkloadAssets(ctx context.Context) error
	//BootstrapServiceTenancyAssets(ctx context.Context) error
	//DeployKustomizeAssetsCRD(ctx context.Context) error
	//DeployKustomizeAssetsKCP(ctx context.Context) error
}

type bootstrap struct {
	config *config.FarosKCPConfig

	clientFactory utilkubernetes.ClientFactory
	kcpClientSet  kcpclient.ClusterInterface
	coreClientSet kubernetes.ClusterInterface

	hostingCoreClientSet clientgokubernetes.Interface
}

func New(config *config.FarosKCPConfig) (*bootstrap, error) {
	cf, err := utilkubernetes.NewClientFactory(config.KCPClusterRestConfig)
	if err != nil {
		return nil, err
	}

	rootRest, err := cf.GetRootRestConfig()
	if err != nil {
		return nil, err
	}

	kcpClientSet, err := kcpclient.NewForConfig(rootRest)
	if err != nil {
		return nil, err
	}

	coreClientSet, err := kubernetes.NewForConfig(rootRest)
	if err != nil {
		return nil, err
	}

	hostingCoreClientSet, err := clientgokubernetes.NewForConfig(config.HostingClusterRestConfig)
	if err != nil {
		return nil, err
	}

	b := &bootstrap{
		config:               config,
		clientFactory:        cf,
		kcpClientSet:         kcpClientSet,
		coreClientSet:        coreClientSet,
		hostingCoreClientSet: hostingCoreClientSet,
	}

	return b, nil
}

func (c *bootstrap) Bootstrap(ctx context.Context) error {
	// bootstrap root workspace
	if err := c.BootstrapRootWorkspace(ctx); err != nil {
		return err
	}

	// create controllers workspace
	for _, w := range []string{
		c.config.ControllersTenantWorkspace,
		c.config.ControllersWorkspace,
		c.config.ControllersOrganizationWorkspace,
	} {
		if err := c.CreateWorkspace(ctx, w); err != nil {
			return err
		}
	}

	// bootstrap controllers workspace
	if err := c.BootstrapControllersWorkspace(ctx); err != nil {
		return err
	}

	// bootstrap controllers tenant workspace
	if err := c.BootstrapTenantsWorkspace(ctx); err != nil {
		return err
	}

	// bootstrap organizations workspace
	if err := c.BootstrapOrganizationsWorkspace(ctx); err != nil {
		return err
	}

	return nil
}
