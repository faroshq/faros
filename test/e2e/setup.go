package e2e

// Copyright (c) Faros.sh
// Licensed under the Apache License 2.0.

import (
	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	farosclient "github.com/faroshq/faros/pkg/operator/clientset/versioned/typed/operator.faros.sh/v1alpha1"
)

type clientSet struct {
	Kubernetes  kubernetes.Interface
	FarosClient farosclient.OperatorV1alpha1Interface
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

	faroscli, err := farosclient.NewForConfig(restconfig)
	if err != nil {
		return nil, err
	}

	return &clientSet{
		Kubernetes:  cli,
		FarosClient: faroscli,
	}, nil
}
