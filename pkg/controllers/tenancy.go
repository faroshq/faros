package controllers

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"k8s.io/klog"

	farosclientset "github.com/faroshq/faros/pkg/client/clientset/versioned/cluster"
	farosinformers "github.com/faroshq/faros/pkg/client/informers/externalversions"
	"github.com/faroshq/faros/pkg/controllers/tenancy/organizations"
	"github.com/faroshq/faros/pkg/controllers/tenancy/workspaces"
	"github.com/kcp-dev/client-go/kubernetes"
	kcpclientset "github.com/kcp-dev/kcp/pkg/client/clientset/versioned/cluster"
)

var (
	internalTenancyAPIExportName = "internal.tenancy.faros.sh"
	tenancyAPIExportName         = "tenancy.faros.sh"
)

// runSystemTenants controller is running in system tenants virtual workspace and is responsible for
// managing workspaces and tenants
func (c *controllerManager) runTenancyControllers(ctx context.Context) error {
	clusterRest := c.config.FarosKCPConfig.ControllersRestConfig

	var restInternalTenants *rest.Config
	// bootstrap rest config for controllers
	if kcpAPIsGroupPresent(clusterRest) {
		if err := wait.PollImmediateInfinite(time.Second*5, func() (bool, error) {
			klog.Infof("looking up virtual workspace URL - %s", internalTenancyAPIExportName)
			var err error
			restInternalTenants, err = restConfigForAPIExport(ctx, clusterRest, internalTenancyAPIExportName)
			if err != nil {
				return false, nil
			}
			return true, nil
		}); err != nil {
			return err
		}

	} else {
		return fmt.Errorf("kcp APIs group not present in cluster. We don't support non kcp clusters yet")
	}

	var restTenants *rest.Config
	// bootstrap rest config for controllers
	if kcpAPIsGroupPresent(clusterRest) {
		if err := wait.PollImmediateInfinite(time.Second*5, func() (bool, error) {
			klog.Infof("looking up virtual workspace URL - %s", tenancyAPIExportName)
			var err error
			restTenants, err = restConfigForAPIExport(ctx, clusterRest, tenancyAPIExportName)
			if err != nil {
				return false, nil
			}
			return true, nil
		}); err != nil {
			return err
		}

	} else {
		return fmt.Errorf("kcp APIs group not present in cluster. We don't support non kcp clusters yet")
	}

	farosClientSet, err := farosclientset.NewForConfig(restInternalTenants)
	if err != nil {
		return err
	}

	// kcpClientSet is used to create workspaces only. We should move into a separate controller
	// and use apiexports. But this is blocked now by:
	// https://github.com/faroshq/faros/issues/50
	// Everything else, should be via APIExports so its easier to migrate in the future
	kcpClientSet, err := kcpclientset.NewForConfig(c.config.FarosKCPConfig.KCPClusterRestConfig)
	if err != nil {
		return err
	}

	// coreClientSet is used to create user facing configuration, like RBAC.
	// So it points to user facing apiexport
	coreClientSet, err := kubernetes.NewForConfig(restTenants)
	if err != nil {
		return err
	}

	// Must always follow the order. Otherwise informers are not initialized
	// 1. create shared informer factory
	// 2. get listers and informers out of the factory in controller constructors
	// 3. start the factory
	// 4. wait for the factory to sync.
	informer := farosinformers.NewSharedInformerFactory(farosClientSet, resyncPeriod)

	ctrlOrganizations, err := organizations.NewController(
		c.config,
		kcpClientSet,
		farosClientSet,
		informer.Tenancy().V1alpha1().Organizations(),
	)
	if err != nil {
		return err
	}

	ctrlWorkspaces, err := workspaces.NewController(
		c.config,
		kcpClientSet,
		coreClientSet,
		farosClientSet,
		informer.Tenancy().V1alpha1().Workspaces(),
	)
	if err != nil {
		return err
	}

	informer.Start(ctx.Done())
	informer.WaitForCacheSync(ctx.Done())

	go ctrlOrganizations.Start(ctx, 2)
	go ctrlWorkspaces.Start(ctx, 2)

	<-ctx.Done()
	return nil
}
