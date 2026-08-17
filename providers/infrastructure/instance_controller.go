// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/faroshq/provider-infrastructure/controller/instance"
	"github.com/faroshq/provider-infrastructure/install"
)

// startInstanceController starts the cross-tenant Instance controller —
// the seam between the flattened tenant-facing Instance kind and the
// per-template kro CRs on the runtime cluster (values validation, platform
// stamps, secret bridging, runtime sync, status mirror) — as part of the
// shared provider-controller attempt.
//
// It runs on the provider's APIExport virtual workspace, so it needs the
// provider kcp config (the same one the controller manager uses). The kro
// runtime cluster is resolved the same way the kro backend resolves it —
// explicit KRO_KUBECONFIG, else the pod's in-cluster config — so the
// operator's in-cluster-runtime mode is honored. Without a runtime cluster
// there is nothing to materialize instances on, so the controller stays
// disabled (dev/REST-only flows).
//
// FAROS_APP_BASE_DOMAIN is optional here: without it, instances that ask to
// be published fail their reconcile with a clear message, while internal
// templates keep provisioning.
func startInstanceController(ctx context.Context, providerConfig *rest.Config, health *controllerHealth) <-chan error {
	if !instanceControllerConfigured() {
		health.markRESTOnly()
		log.Printf("instance controller: disabled (no kro runtime target)")
		return nil
	}
	result := make(chan error, 1)
	go func() {
		defer close(result)
		result <- runInstanceControllerLifecycle(ctx, health, func() (instanceControllerRunner, string, error) {
			if providerConfig == nil {
				return nil, "", fmt.Errorf("provider kubeconfig is required")
			}
			runtimeClient, runtimeSrc, err := runtimeDynamicClient()
			if err != nil {
				return nil, "", fmt.Errorf("no kro runtime cluster: %w", err)
			}
			baseDomain := strings.TrimSpace(os.Getenv("FAROS_APP_BASE_DOMAIN"))
			ctrl, err := instance.New(instance.Config{
				ProviderConfig: providerConfig,
				APIExportName:  install.APIExportName,
				BaseDomain:     baseDomain,
				Runtime:        runtimeClient,
			})
			if err != nil {
				return nil, "", err
			}
			return ctrl, fmt.Sprintf("apiExport=%s baseDomain=%q runtime=%s", install.APIExportName, baseDomain, runtimeSrc), nil
		})
	}()
	return result
}

func instanceControllerConfigured() bool {
	return strings.TrimSpace(os.Getenv("KRO_KUBECONFIG")) != "" ||
		strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_HOST")) != ""
}

type instanceControllerRunner interface {
	Start(context.Context) error
	Ready() <-chan struct{}
}

func runInstanceControllerLifecycle(
	ctx context.Context,
	health *controllerHealth,
	setup func() (instanceControllerRunner, string, error),
) error {
	health.markStarting()
	controller, description, err := setup()
	if err != nil {
		err = fmt.Errorf("instance controller setup: %w", err)
		health.markFailed(err)
		return err
	}
	log.Printf("instance controller: starting (%s)", description)
	startResult := make(chan error, 1)
	go func() {
		startResult <- controller.Start(ctx)
	}()

	ready := controller.Ready()
	for {
		select {
		case err = <-startResult:
			return finishInstanceControllerLifecycle(ctx, health, err)
		case <-ready:
			health.markReady()
			ready = nil
		case <-ctx.Done():
			// Join Start before returning so a retry cannot overlap the old
			// multicluster manager.
			err = <-startResult
			return finishInstanceControllerLifecycle(ctx, health, err)
		}
	}
}

func finishInstanceControllerLifecycle(ctx context.Context, health *controllerHealth, err error) error {
	if ctx.Err() != nil {
		health.markStopped(ctx.Err())
		return ctx.Err()
	}
	if err == nil {
		err = errors.New("instance controller exited without an error")
	} else {
		err = fmt.Errorf("instance controller stopped: %w", err)
	}
	health.markFailed(err)
	return err
}

// runtimeDynamicClient builds a dynamic client for the kro runtime cluster —
// the same cluster the kro backend authors RGDs on. It mirrors the kro
// backend's resolution in controller_manager.go: explicit KRO_KUBECONFIG,
// else the pod's in-cluster config (the operator's in-cluster-runtime mode).
// Errors when neither is available (dev/REST-only), so the controller stays
// disabled rather than pointing at the wrong cluster. Returns the source for
// logging.
func runtimeDynamicClient() (dynamic.Interface, string, error) {
	var cfg *rest.Config
	var src string
	if p := os.Getenv("KRO_KUBECONFIG"); p != "" {
		c, err := clientcmd.BuildConfigFromFlags("", p)
		if err != nil {
			return nil, "", fmt.Errorf("loading KRO_KUBECONFIG: %w", err)
		}
		cfg, src = c, "KRO_KUBECONFIG="+p
	} else if c, err := rest.InClusterConfig(); err == nil {
		cfg, src = c, "in-cluster"
	} else {
		return nil, "", fmt.Errorf("KRO_KUBECONFIG unset and not running in a pod")
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, "", fmt.Errorf("runtime dynamic client: %w", err)
	}
	return dyn, src, nil
}
