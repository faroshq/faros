package main

// Copyright (c) Faros.sh.
// Licensed under the Apache License 2.0.

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"

	farosconfigscli "github.com/faroshq/faros/pkg/operator/clientset/faros.sh/v1alpha1/versioned/typed/faros.sh/v1alpha1"
	cluster "github.com/faroshq/faros/pkg/operator/controllers/clusters"
	"github.com/faroshq/faros/pkg/util/logger"
	// +kubebuilder:scaffold:imports
)

func hub(ctx context.Context, log *zap.Logger) error {
	rlog := logger.NewLogRLogger(log)
	ctrl.SetLogger(rlog)

	restConfig, err := ctrl.GetConfig()
	if err != nil {
		return err
	}

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		MetricsBindAddress: "0", // disabled
		Port:               8443,
	})
	if err != nil {
		return err
	}

	kubernetescli, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	fcfgcli, err := farosconfigscli.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	if err = (cluster.NewReconciler(
		rlog.WithValues("controller", cluster.ControllerName),
		kubernetescli, fcfgcli)).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create controller Clusters: %v", err)
	}
	// +kubebuilder:scaffold:builder

	log.Info("starting manager")
	return mgr.Start(ctrl.SetupSignalHandler())
}
