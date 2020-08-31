package main

// Copyright (c) Faros.sh.
// Licensed under the Apache License 2.0.

import (
	"context"

	"go.uber.org/zap"
	extensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"

	// +kubebuilder:scaffold:imports
	faroscfgcli "github.com/faroshq/faros/pkg/operator/clientset/faros.sh/v1alpha1/versioned/typed/faros.sh/v1alpha1"
	deployer "github.com/faroshq/faros/pkg/operator/deploy"
)

func deploy(ctx context.Context, log *zap.Logger, deployment string) error {
	restConfig, err := ctrl.GetConfig()
	if err != nil {
		return err
	}

	kubernetescli, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	extcli, err := extensionsclient.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	fcfgcli, err := faroscfgcli.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	log.Info("starting deployer")

	operator, err := deployer.New(log, deployment, kubernetescli, extcli, fcfgcli)
	if err != nil {
		return err
	}

	err = operator.CreateOrUpdate(ctx)
	if err != nil {
		return err
	}

	// TODO: re-enable this
	//wait.PollImmediateUntil(time.Minute*10, func() (bool, error) {
	//	// We use the outer context, not the timeout context, as we do not want
	//	// to time out the condition function itself, only stop retrying once
	//	// timeoutCtx's timeout has fired.
	//	return operator.IsReady(ctx)
	//}, ctx.Done())

	return nil
}
