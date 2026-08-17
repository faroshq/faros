// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/kcp-dev/multicluster-provider/apiexport"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"

	deploymentcontroller "github.com/faroshq/provider-deployments/controller/deployment"
	deploymentscheme "github.com/faroshq/provider-deployments/scheme"
	sdkinstall "github.com/faroshq/provider-sdk/install"
)

var errControllerDisabled = errors.New("no kubeconfig available; controller manager disabled")

func loadControllerConfig() (*rest.Config, error) {
	for _, key := range []string{"FAROS_PROVIDER_KUBECONFIG", "DEPLOYMENTS_KUBECONFIG", "KUBECONFIG"} {
		if path := os.Getenv(key); path != "" {
			cfg, err := clientcmd.BuildConfigFromFlags("", path)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			return cfg, nil
		}
	}
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, errControllerDisabled
	}
	return cfg, nil
}

func startControllerManager(
	ctx context.Context,
	config *rest.Config,
	ready *atomic.Bool,
	stop context.CancelFunc,
	exited chan<- error,
) error {
	if config == nil {
		return errControllerDisabled
	}
	ctrl.SetLogger(klog.NewKlogr())
	scheme := deploymentscheme.New()
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("create provider workspace client: %w", err)
	}
	if err := sdkinstall.EnsureAPIExportEndpointSlice(
		ctx, client, apiExportName, apiExportName, deploymentWorkspacePath(),
	); err != nil {
		return fmt.Errorf("ensure APIExportEndpointSlice: %w", err)
	}
	provider, err := apiexport.New(config, apiExportName, apiexport.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("create APIExport multicluster provider: %w", err)
	}
	mgr, err := mcmanager.New(config, provider, manager.Options{Scheme: scheme, Metrics: metricsserver.Options{BindAddress: "0"}})
	if err != nil {
		return fmt.Errorf("create multicluster manager: %w", err)
	}
	if err := (&deploymentcontroller.Reconciler{}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("deployment controller: %w", err)
	}
	managerCtx, cancelManager := context.WithCancel(ctx)
	go func() {
		err := mgr.Start(managerCtx)
		if err != nil {
			ctrl.Log.Error(err, "controller manager exited")
		}
		cancelManager()
		ready.Store(false)
		exited <- err
		stop()
	}()
	go func() {
		// The manager is not ready merely because Start was spawned. Its local
		// cache contains the APIExport endpoint-slice watch that discovers tenant
		// workspaces; only publish readiness after that cache has synchronized.
		if mgr.GetLocalManager().GetCache().WaitForCacheSync(managerCtx) && managerCtx.Err() == nil {
			ready.Store(true)
		}
	}()
	return nil
}
