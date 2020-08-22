package main

// Copyright (c) Faros.sh.
// Licensed under the Apache License 2.0.

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"

	farosclient "github.com/faroshq/faros/pkg/operator/clientset/versioned/typed/operator.faros.sh/v1alpha1"
	"github.com/faroshq/faros/pkg/operator/controllers"
	"github.com/faroshq/faros/pkg/operator/controllers/alertwebhook"
	"github.com/faroshq/faros/pkg/operator/controllers/internetchecker"
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
	faroscli, err := farosclient.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	if err = (alertwebhook.NewReconciler(
		rlog.WithValues("controller", controllers.AlertwebhookControllerName),
		kubernetescli)).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create controller AlertWebhook: %v", err)
	}

	if err = (internetchecker.NewReconciler(
		rlog.WithValues("controller", controllers.InternetCheckerControllerName),
		kubernetescli, faroscli)).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create controller InternetChecker: %v", err)
	}
	// +kubebuilder:scaffold:builder

	log.Info("starting manager")
	return mgr.Start(ctrl.SetupSignalHandler())
}
