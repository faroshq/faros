package controllers

import (
	"context"
)

var (
	workloadAPIExportName = "workload.faros.sh"
)

// runWorkloadsControllers controller is running in system controllers workspace and is responsible for
// managing workloads related objects
func (c *controllerManager) runWorkloadsControllers(ctx context.Context) error {
	//restConfig, err := c.clientFactory.GetWorkspaceRestConfig(ctx, c.config.FarosKCPConfig.ControllersWorkspace)
	//if err != nil {
	//	return err
	//}

	//var rest *rest.Config
	//if kcpAPIsGroupPresent(restConfig) {
	//	if err := wait.PollImmediateInfinite(time.Second*5, func() (bool, error) {
	//		klog.Infof("looking up virtual workspace URL - %s", workloadAPIExportName)
	//		rest, err = restConfigForAPIExport(ctx, restConfig, workloadAPIExportName)
	//		if err != nil {
	//			return false, nil
	//		}
	//		return true, nil
	//	}); err != nil {
	//		return err
	//	}
	//
	//} else {
	//	return fmt.Errorf("kcp APIs group not present in cluster. We don't support non kcp clusters yet")
	//}

	//kcpClientSet, err := kcpclientset.NewForConfig(rest)
	//if err != nil {
	//	return err
	//}

	// Must always follow the order. Otherwise informers are not initialized
	// 1. create shared informer factory
	// 2. get listers and informers out of the factory in controller constructors
	// 3. start the factory
	// 4. wait for the factory to sync.
	//informer := kcpinformers.NewSharedInformerFactory(kcpClientSet, resyncPeriod)

	//ctrlSynctargets, err := synctargets.NewController(
	//	c.config,
	//	kcpClientSet,
	//	c.coreClientSet,
	//	informer.Workload().V1alpha1().SyncTargets(),
	//)
	//if err != nil {
	//	return err
	//}

	//informer.Start(ctx.Done())
	//informer.WaitForCacheSync(ctx.Done())

	//go ctrlSynctargets.Start(ctx, 2)

	<-ctx.Done()
	return nil
}
