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
	cancelledApplications := make(chan string, 2)
	surfaces := newServeSurfaces(initial, func(config *rest.Config) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Test-Config", config.Host)
			w.WriteHeader(http.StatusNoContent)
		})
	}, func(ctx context.Context, config *rest.Config, _ *controllerHealth) <-chan error {
		go func() {
			<-ctx.Done()
			cancelledApplications <- config.Host
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
	case got := <-cancelledApplications:
		if got != configs[0].Host {
			t.Fatalf("cancelled Application config = %q, want %q", got, configs[0].Host)
		}
	case <-time.After(time.Second):
		t.Fatal("failed attempt's Application controller was not cancelled")
	}

	close(allowRetry)
	waitForSignal(t, secondStarted, "corrected second attempt")
	assertSurfaceConfig(t, surfaces, configs[1].Host)

	cancel()
	waitForSignal(t, done, "controller lifecycle shutdown")
	select {
	case got := <-cancelledApplications:
		if got != configs[1].Host {
			t.Fatalf("cancelled replacement Application config = %q, want %q", got, configs[1].Host)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement Application controller was not cancelled on shutdown")
	}
}

func TestAggregateReadinessIncludesConfiguredApplicationController(t *testing.T) {
	platform := newControllerHealth(true)
	application := newControllerHealth(true)
	platform.markReady()
	application.markFailed(errors.New("application startup failed"))

	readiness := aggregateReadiness(platform, application)
	if readiness.Ready || readiness.Controller != "application-failed" || readiness.Error != "application startup failed" {
		t.Fatalf("aggregate readiness = %+v", readiness)
	}

	application.markReady()
	readiness = aggregateReadiness(platform, application)
	if !readiness.Ready || readiness.Controller != string(controllerStateReady) {
		t.Fatalf("ready aggregate = %+v", readiness)
	}

	disabled := newControllerHealth(false)
	readiness = aggregateReadiness(platform, disabled)
	if !readiness.Ready {
		t.Fatalf("disabled application became mandatory: %+v", readiness)
	}
}

func TestApplicationControllerConfiguredRequiresDomainAndRuntime(t *testing.T) {
	t.Setenv("FAROS_APP_BASE_DOMAIN", "")
	t.Setenv("KRO_KUBECONFIG", "")
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	if applicationControllerConfigured() {
		t.Fatal("application controller configured without a base domain or runtime")
	}

	t.Setenv("FAROS_APP_BASE_DOMAIN", "apps.127.0.0.1.sslip.io")
	if applicationControllerConfigured() {
		t.Fatal("stub mode made application controller mandatory")
	}

	t.Setenv("KRO_KUBECONFIG", "/tmp/kro.kubeconfig")
	if !applicationControllerConfigured() {
		t.Fatal("explicit kro runtime did not configure application controller")
	}

	t.Setenv("KRO_KUBECONFIG", "")
	t.Setenv("KUBERNETES_SERVICE_HOST", "kubernetes.default.svc")
	if !applicationControllerConfigured() {
		t.Fatal("in-cluster runtime did not configure application controller")
	}
}

func TestConfiguredApplicationFailureFailsAttemptAndCancelsPlatformManager(t *testing.T) {
	platformHealth := newControllerHealth(true)
	applicationHealth := newControllerHealth(true)
	applicationFailure := errors.New("application watch exited")
	managerCancelled := make(chan struct{})
	surfaces := newServeSurfaces(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		func(*rest.Config) http.Handler { return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}) },
		func(context.Context, *rest.Config, *controllerHealth) <-chan error {
			applicationHealth.markFailed(applicationFailure)
			result := make(chan error, 1)
			result <- applicationFailure
			close(result)
			return result
		},
	)

	runControllerManagerWithRetryGate(
		context.Background(),
		platformHealth,
		func() (*rest.Config, error) { return &rest.Config{Host: "https://provider.example"}, nil },
		func(ctx context.Context, config *rest.Config, health *controllerHealth) error {
			return runControllerAttempt(ctx, config, health, applicationHealth, surfaces, func(managerCtx context.Context, _ *rest.Config, _ *controllerHealth) error {
				<-managerCtx.Done()
				close(managerCancelled)
				return managerCtx.Err()
			})
		},
		time.Second,
		func(context.Context, time.Duration) bool { return false },
	)

	waitForSignal(t, managerCancelled, "platform manager cancellation after application failure")
	readiness := aggregateReadiness(newReadyControllerHealth(), applicationHealth)
	if readiness.Ready || readiness.Controller != "application-failed" {
		t.Fatalf("application failure did not block aggregate readiness: %+v", readiness)
	}
}

func TestRunControllerAttemptJoinsDelayedSiblingBeforeReplacement(t *testing.T) {
	applicationHealth := newControllerHealth(true)
	var applicationStarts atomic.Int32
	var activeApplications atomic.Int32
	var maxActiveApplications atomic.Int32
	firstCancelled := make(chan struct{})
	allowFirstExit := make(chan struct{})
	secondStarted := make(chan struct{})
	surfaces := newServeSurfaces(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		func(*rest.Config) http.Handler { return http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}) },
		func(ctx context.Context, _ *rest.Config, health *controllerHealth) <-chan error {
			attempt := applicationStarts.Add(1)
			result := make(chan error, 1)
			go func() {
				active := activeApplications.Add(1)
				for {
					max := maxActiveApplications.Load()
					if active <= max || maxActiveApplications.CompareAndSwap(max, active) {
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
				activeApplications.Add(-1)
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
			applicationHealth,
			surfaces,
			func(context.Context, *rest.Config, *controllerHealth) error {
				return errors.New("platform startup failed")
			},
		)
	}()
	waitForSignal(t, firstCancelled, "first Application cancellation")
	select {
	case err := <-firstDone:
		t.Fatalf("attempt returned before delayed sibling exited: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(allowFirstExit)
	if err := <-firstDone; err == nil || !strings.Contains(err.Error(), "platform startup failed") {
		t.Fatalf("first attempt error = %v", err)
	}
	if active := activeApplications.Load(); active != 0 {
		t.Fatalf("active Applications after joined attempt = %d, want 0", active)
	}
	if got := applicationHealth.snapshot().State; got != controllerStateStopped {
		t.Fatalf("first terminal health = %q, want stopped", got)
	}

	secondCtx, cancelSecond := context.WithCancel(context.Background())
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- runControllerAttempt(
			secondCtx,
			&rest.Config{Host: "https://second.example"},
			newControllerHealth(true),
			applicationHealth,
			surfaces,
			func(ctx context.Context, _ *rest.Config, _ *controllerHealth) error {
				<-ctx.Done()
				return ctx.Err()
			},
		)
	}()
	waitForSignal(t, secondStarted, "replacement Application start")
	if got := applicationHealth.snapshot().State; got != controllerStateReady {
		t.Fatalf("replacement health = %q, want ready", got)
	}
	if max := maxActiveApplications.Load(); max != 1 {
		t.Fatalf("maximum overlapping Applications = %d, want 1", max)
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
