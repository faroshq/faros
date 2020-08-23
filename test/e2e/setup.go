package e2e

// Copyright (c) Faros.sh
// Licensed under the Apache License 2.0.

import (
	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	farosmonitorsclient "github.com/faroshq/faros/pkg/operator/clientset/monitor.faros.sh/v1alpha1/versioned/typed/monitor.faros.sh/v1alpha1"
)

type clientSet struct {
	Kubernetes  kubernetes.Interface
	FarosClient farosmonitorsclient.MonitorV1alpha1Interface
}

var (
	log     *zap.Logger
	clients *clientSet
)

func newClientSet(subscriptionID string) (*clientSet, error) {
	kubeconfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	)

	restconfig, err := kubeconfig.ClientConfig()
	if err != nil {
		return nil, err
	}

	cli := kubernetes.NewForConfigOrDie(restconfig)

	faromonitorsscli, err := farosmonitorsclient.NewForConfig(restconfig)
	if err != nil {
		return nil, err
	}

	return &clientSet{
		Kubernetes:  cli,
		FarosClient: faromonitorsscli,
	}, nil
}
