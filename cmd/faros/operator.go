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
	farosmonitorscli "github.com/faroshq/faros/pkg/operator/clientset/monitor.faros.sh/v1alpha1/versioned/typed/monitor.faros.sh/v1alpha1"
	"github.com/faroshq/faros/pkg/operator/controllers/config"
	"github.com/faroshq/faros/pkg/operator/controllers/network"
	"github.com/faroshq/faros/pkg/util/logger"
	// +kubebuilder:scaffold:imports
)

func operator(ctx context.Context, log *zap.Logger) error {
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

	fmoncli, err := farosmonitorscli.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	fcfgcli, err := farosconfigscli.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	if err = (config.NewReconciler(
		rlog.WithValues("controller", config.ControllerName),
		kubernetescli, fcfgcli)).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create controller Config: %v", err)
	}
	// +kubebuilder:scaffold:builder

	if err = (network.NewReconciler(
		rlog.WithValues("controller", network.ControllerName),
		kubernetescli, fmoncli)).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create controller Network: %v", err)
	}
	// +kubebuilder:scaffold:builder

	log.Info("starting manager")
	return mgr.Start(ctrl.SetupSignalHandler())
}
