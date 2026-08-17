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
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/client-go/rest"
)

func TestLateConfigRetryReplacesRequestSurfacesAndCancelsFailedAttempt(t *testing.T) {
	initial := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	cancelledInstances := make(chan string, 2)
	surfaces := newServeSurfaces(initial, func(config *rest.Config) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Test-Config", config.Host)
			w.WriteHeader(http.StatusNoContent)
		})
	}, func(ctx context.Context, config *rest.Config, _ *controllerHealth) <-chan error {
		go func() {
			<-ctx.Done()
			cancelledInstances <- config.Host
		}()
		return nil
	})

	configs := []*rest.Config{
		{Host: "https://stale.example"},
		{Host: "https://corrected.example"},
	}
	var loads atomic.Int32
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	allowRetry := make(chan struct{})
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		runControllerManagerWithRetryGate(
			ctx,
			newControllerHealth(true),
			func() (*rest.Config, error) {
				index := int(loads.Add(1)) - 1
				return configs[index], nil
			},
			func(attemptCtx context.Context, config *rest.Config, _ *controllerHealth) error {
				surfaces.configure(attemptCtx, config, nil)
				if config == configs[0] {
					close(firstStarted)
					return errors.New("stale config failed manager startup")
				}
				close(secondStarted)
				<-attemptCtx.Done()
				return attemptCtx.Err()
			},
			time.Second,
			func(retryCtx context.Context, _ time.Duration) bool {
				select {
				case <-allowRetry:
					return true
				case <-retryCtx.Done():
					return false
				}
			},
		)
		close(done)
	}()

	waitForSignal(t, firstStarted, "failed first attempt")
	assertSurfaceConfig(t, surfaces, configs[0].Host)
	select {
	case got := <-cancelledInstances:
		if got != configs[0].Host {
			t.Fatalf("cancelled Instance config = %q, want %q", got, configs[0].Host)
		}
	case <-time.After(time.Second):
		t.Fatal("failed attempt's Instance controller was not cancelled")
	}

	close(allowRetry)
	waitForSignal(t, secondStarted, "corrected second attempt")
	assertSurfaceConfig(t, surfaces, configs[1].Host)

	cancel()
	waitForSignal(t, done, "controller lifecycle shutdown")
	select {
	case got := <-cancelledInstances:
		if got != configs[1].Host {
			t.Fatalf("cancelled replacement Instance config = %q, want %q", got, configs[1].Host)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement Instance controller was not cancelled on shutdown")
	}
}

func TestAggregateReadinessIncludesConfiguredInstanceController(t *testing.T) {
	platform := newControllerHealth(true)
	instance := newControllerHealth(true)
	platform.markReady()
	instance.markFailed(errors.New("instance startup failed"))

	readiness := aggregateReadiness(platform, instance)
	if readiness.Ready || readiness.Controller != "instance-failed" || readiness.Error != "instance startup failed" {
		t.Fatalf("aggregate readiness = %+v", readiness)
	}

	instance.markReady()
	readiness = aggregateReadiness(platform, instance)
	if !readiness.Ready || readiness.Controller != string(controllerStateReady) {
		t.Fatalf("ready aggregate = %+v", readiness)
	}

	disabled := newControllerHealth(false)
	readiness = aggregateReadiness(platform, disabled)
	if !readiness.Ready {
		t.Fatalf("disabled instance became mandatory: %+v", readiness)
	}
}

func TestInstanceControllerConfiguredAcceptsExplicitAndInClusterRuntime(t *testing.T) {
	t.Setenv("FAROS_APP_BASE_DOMAIN", "")
	t.Setenv("KRO_KUBECONFIG", "")
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	if instanceControllerConfigured() {
		t.Fatal("instance controller configured without a runtime")
	}

	t.Setenv("FAROS_APP_BASE_DOMAIN", "apps.127.0.0.1.sslip.io")
	if instanceControllerConfigured() {
		t.Fatal("stub mode made instance controller mandatory")
	}

	t.Setenv("KRO_KUBECONFIG", "/tmp/kro.kubeconfig")
	if !instanceControllerConfigured() {
		t.Fatal("explicit kro runtime did not configure instance controller")
	}

	t.Setenv("KRO_KUBECONFIG", "")
	t.Setenv("KUBERNETES_SERVICE_HOST", "kubernetes.default.svc")
	if !instanceControllerConfigured() {
		t.Fatal("in-cluster runtime did not configure instance controller")
	}
}

func TestConfiguredInstanceFailureFailsAttemptAndCancelsPlatformManager(t *testing.T) {
	platformHealth := newControllerHealth(true)
	instanceHealth := newControllerHealth(true)
	instanceFailure := errors.New("instance watch exited")
	managerCancelled := make(chan struct{})
	surfaces := newServeSurfaces(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		func(*rest.Config) http.Handler { return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}) },
		func(context.Context, *rest.Config, *controllerHealth) <-chan error {
			instanceHealth.markFailed(instanceFailure)
			result := make(chan error, 1)
			result <- instanceFailure
			close(result)
			return result
		},
	)

	runControllerManagerWithRetryGate(
		context.Background(),
		platformHealth,
		func() (*rest.Config, error) { return &rest.Config{Host: "https://provider.example"}, nil },
		func(ctx context.Context, config *rest.Config, health *controllerHealth) error {
			return runControllerAttempt(ctx, config, health, instanceHealth, surfaces, func(managerCtx context.Context, _ *rest.Config, _ *controllerHealth) error {
				<-managerCtx.Done()
				close(managerCancelled)
				return managerCtx.Err()
			})
		},
		time.Second,
		func(context.Context, time.Duration) bool { return false },
	)

	waitForSignal(t, managerCancelled, "platform manager cancellation after instance failure")
	readiness := aggregateReadiness(newReadyControllerHealth(), instanceHealth)
	if readiness.Ready || readiness.Controller != "instance-failed" {
		t.Fatalf("instance failure did not block aggregate readiness: %+v", readiness)
	}
}

func TestRunControllerAttemptJoinsDelayedSiblingBeforeReplacement(t *testing.T) {
	instanceHealth := newControllerHealth(true)
	var instanceStarts atomic.Int32
	var activeInstances atomic.Int32
	var maxActiveInstances atomic.Int32
	firstCancelled := make(chan struct{})
	allowFirstExit := make(chan struct{})
	secondStarted := make(chan struct{})
	surfaces := newServeSurfaces(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		func(*rest.Config) http.Handler { return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}) },
		func(ctx context.Context, _ *rest.Config, health *controllerHealth) <-chan error {
			attempt := instanceStarts.Add(1)
			result := make(chan error, 1)
			go func() {
				active := activeInstances.Add(1)
				for {
					max := maxActiveInstances.Load()
					if active <= max || maxActiveInstances.CompareAndSwap(max, active) {
						break
					}
				}
				health.markReady()
				if attempt == 2 {
					close(secondStarted)
				}
				<-ctx.Done()
				if attempt == 1 {
					close(firstCancelled)
					<-allowFirstExit
				}
				// This terminal health mutation must finish before the next attempt
				// is allowed to publish its Ready state.
				health.markStopped(ctx.Err())
				activeInstances.Add(-1)
				result <- ctx.Err()
				close(result)
			}()
			return result
		},
	)

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- runControllerAttempt(
			context.Background(),
			&rest.Config{Host: "https://first.example"},
			newControllerHealth(true),
			instanceHealth,
			surfaces,
			func(context.Context, *rest.Config, *controllerHealth) error {
				return errors.New("platform startup failed")
			},
		)
	}()
	waitForSignal(t, firstCancelled, "first Instance cancellation")
	select {
	case err := <-firstDone:
		t.Fatalf("attempt returned before delayed sibling exited: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(allowFirstExit)
	if err := <-firstDone; err == nil || !strings.Contains(err.Error(), "platform startup failed") {
		t.Fatalf("first attempt error = %v", err)
	}
	if active := activeInstances.Load(); active != 0 {
		t.Fatalf("active Instances after joined attempt = %d, want 0", active)
	}
	if got := instanceHealth.snapshot().State; got != controllerStateStopped {
		t.Fatalf("first terminal health = %q, want stopped", got)
	}

	secondCtx, cancelSecond := context.WithCancel(context.Background())
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- runControllerAttempt(
			secondCtx,
			&rest.Config{Host: "https://second.example"},
			newControllerHealth(true),
			instanceHealth,
			surfaces,
			func(ctx context.Context, _ *rest.Config, _ *controllerHealth) error {
				<-ctx.Done()
				return ctx.Err()
			},
		)
	}()
	waitForSignal(t, secondStarted, "replacement Instance start")
	if got := instanceHealth.snapshot().State; got != controllerStateReady {
		t.Fatalf("replacement health = %q, want ready", got)
	}
	if max := maxActiveInstances.Load(); max != 1 {
		t.Fatalf("maximum overlapping Instances = %d, want 1", max)
	}
	cancelSecond()
	if err := <-secondDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("replacement attempt error = %v, want context canceled", err)
	}
}

func newReadyControllerHealth() *controllerHealth {
	health := newControllerHealth(true)
	health.markReady()
	return health
}

func TestControllerConfigLoaderReloadsCorrectedCLIConfigAfterNonNilStartup(t *testing.T) {
	startup := &rest.Config{Host: "https://stale-startup.example"}
	current := startup
	loader := controllerConfigLoader(startup, func() (*rest.Config, error) {
		return rest.CopyConfig(current), nil
	})

	first, err := loader()
	if err != nil || first.Host != startup.Host {
		t.Fatalf("first load = %#v, %v", first, err)
	}
	current = &rest.Config{Host: "https://corrected-file.example"}
	second, err := loader()
	if err != nil || second.Host != current.Host {
		t.Fatalf("corrected load = %#v, %v", second, err)
	}
	if second.Host == startup.Host {
		t.Fatal("CLI loader reused the stale successfully parsed startup config")
	}
}

func assertSurfaceConfig(t *testing.T, handler http.Handler, want string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("X-Test-Config") != want {
		t.Fatalf("request surface = status %d config %q, want %d/%q", response.Code, response.Header().Get("X-Test-Config"), http.StatusNoContent, want)
	}
}
