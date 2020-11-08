package main

// Copyright (c) Faros.sh.
// Licensed under the Apache License 2.0.

import (
	"context"
	"fmt"
	"os"

	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/faroshq/faros/pkg/monitor"
	faroscli "github.com/faroshq/faros/pkg/operator/clientset/faros.sh/v1alpha1/versioned/typed/faros.sh/v1alpha1"
	cluster "github.com/faroshq/faros/pkg/operator/controllers/clusters"
	worker "github.com/faroshq/faros/pkg/operator/controllers/workers"
	"github.com/faroshq/faros/pkg/util/logger"
	"github.com/faroshq/faros/pkg/util/strings"
	// +kubebuilder:scaffold:imports
)

func hub(ctx_ context.Context, log *zap.Logger) error {
	rlog := logger.NewLogRLogger(log)
	ctrl.SetLogger(rlog)
	ctx, cancel := context.WithCancel(ctx_)

	restConfig, err := ctrl.GetConfig()
	if err != nil {
		return err
	}

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		MetricsBindAddress:      "0", // disabled
		Port:                    8443,
		LeaderElection:          true,
		LeaderElectionID:        "faros-cluster-controller",
		LeaderElectionNamespace: "faros-hub",
	})
	if err != nil {
		return err
	}

	kubernetescli, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	fcli, err := faroscli.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	name := os.Getenv("POD_NAME")
	if name == "" {
		name = strings.RandomString(4)
	}

	cctrl, err := cluster.NewReconciler(rlog.WithValues("controller", cluster.ControllerName), kubernetescli, fcli)
	if err := cctrl.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create controller Clusters: %v", err)
	}

	wctrl, err := worker.NewReconciler(ctx, rlog.WithValues("controller", worker.ControllerName), name, kubernetescli, fcli)
	if err := wctrl.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to create controller Workers: %v", err)
	}
	// +kubebuilder:scaffold:builder

	// initiate & start monitoring hub
	mon, err := monitor.NewMonitor(log, name, kubernetescli, fcli)
	if err != nil {
		return err
	}

	signal := ctrl.SetupSignalHandler()
	done := make(chan struct{})

	log.Info("starting manager")
	go mgr.Start(signal)

	log.Info("starting monitor")
	go mon.Run(ctx, done)

	<-signal
	log.Info("signal detected")
	cancel()
	<-done

	return nil
}
