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

	"github.com/faroshq/provider-infrastructure/controller/application"
	"github.com/faroshq/provider-infrastructure/install"
)

// startApplicationController starts the cross-tenant Application instance
// controller (fqdn stamping + OIDC client-secret bridge) in a goroutine and
// returns its terminal result to the shared provider-controller attempt.
//
// It runs on the provider's APIExport virtual workspace, so it needs the
// provider kcp config (the same one the controller manager uses). It's
// opt-in on FAROS_APP_BASE_DOMAIN plus an explicit runtime target: without
// both, it stays disabled, preserving the REST-only/stub flow. The kro runtime
// cluster (where the bridged Secret lands) is resolved the same
// way the kro backend resolves it — explicit KRO_KUBECONFIG, else the pod's
// in-cluster config — so the operator's in-cluster-runtime mode is honored
// (it does NOT mount a KRO_KUBECONFIG in that mode).
func startApplicationController(ctx context.Context, providerConfig *rest.Config, health *controllerHealth) <-chan error {
	if !applicationControllerConfigured() {
		health.markRESTOnly()
		log.Printf("application controller: disabled (need FAROS_APP_BASE_DOMAIN and a kro runtime target)")
		return nil
	}
	result := make(chan error, 1)
	go func() {
		defer close(result)
		result <- runApplicationControllerLifecycle(ctx, health, func() (applicationControllerRunner, string, error) {
			if providerConfig == nil {
				return nil, "", fmt.Errorf("provider kubeconfig is required")
			}
			baseDomain := strings.TrimSpace(os.Getenv("FAROS_APP_BASE_DOMAIN"))
			runtimeClient, runtimeSrc, err := runtimeDynamicClient()
			if err != nil {
				return nil, "", fmt.Errorf("no kro runtime cluster: %w", err)
			}
			ctrl, err := application.New(application.Config{
				ProviderConfig: providerConfig,
				APIExportName:  install.APIExportName,
				BaseDomain:     baseDomain,
				Runtime:        runtimeClient,
			})
			if err != nil {
				return nil, "", err
			}
			return ctrl, fmt.Sprintf("apiExport=%s baseDomain=%s runtime=%s", install.APIExportName, baseDomain, runtimeSrc), nil
		})
	}()
	return result
}

func applicationControllerConfigured() bool {
	if strings.TrimSpace(os.Getenv("FAROS_APP_BASE_DOMAIN")) == "" {
		return false
	}
	// A base domain is harmless in REST-only/stub development. Require an
	// explicit runtime target before making the Application controller part of
	// aggregate readiness. In-cluster deployments intentionally omit
	// KRO_KUBECONFIG and use their pod identity instead.
	return strings.TrimSpace(os.Getenv("KRO_KUBECONFIG")) != "" ||
		strings.TrimSpace(os.Getenv("KUBERNETES_SERVICE_HOST")) != ""
}

type applicationControllerRunner interface {
	Start(context.Context) error
	Ready() <-chan struct{}
}

func runApplicationControllerLifecycle(
	ctx context.Context,
	health *controllerHealth,
	setup func() (applicationControllerRunner, string, error),
) error {
	health.markStarting()
	controller, description, err := setup()
	if err != nil {
		err = fmt.Errorf("application controller setup: %w", err)
		health.markFailed(err)
		return err
	}
	log.Printf("application controller: starting (%s)", description)
	startResult := make(chan error, 1)
	go func() {
		startResult <- controller.Start(ctx)
	}()

	ready := controller.Ready()
	for {
		select {
		case err = <-startResult:
			return finishApplicationControllerLifecycle(ctx, health, err)
		case <-ready:
			// Elected closes only after Start has reached the manager startup/election
			// boundary. A controller that fails before this point remains Starting
			// until the terminal result is recorded as Failed.
			health.markReady()
			ready = nil
		case <-ctx.Done():
			// Join Start before returning. The parent attempt must not launch a
			// replacement while this manager can still reconcile or mutate health.
			err = <-startResult
			return finishApplicationControllerLifecycle(ctx, health, err)
		}
	}
}

func finishApplicationControllerLifecycle(ctx context.Context, health *controllerHealth, err error) error {
	if ctx.Err() != nil {
		health.markStopped(ctx.Err())
		return ctx.Err()
	}
	if err == nil {
		err = errors.New("application controller exited without an error")
	} else {
		err = fmt.Errorf("application controller stopped: %w", err)
	}
	health.markFailed(err)
	return err
}

// runtimeDynamicClient builds a dynamic client for the kro runtime cluster the
// controller bridges OIDC secrets onto — the same cluster the kro backend
// authors RGDs on. It mirrors the kro backend's resolution in
// controller_manager.go: explicit KRO_KUBECONFIG, else the pod's in-cluster
// config (the operator's in-cluster-runtime mode). Errors when neither is
// available (dev/REST-only), so the controller stays disabled rather than
// pointing at the wrong cluster. Returns the source for logging.
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
