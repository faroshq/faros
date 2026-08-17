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
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/client-go/rest"
)

type testApplicationControllerRunner struct {
	ready <-chan struct{}
	start func(context.Context) error
}

func (r testApplicationControllerRunner) Start(ctx context.Context) error { return r.start(ctx) }
func (r testApplicationControllerRunner) Ready() <-chan struct{}          { return r.ready }

func TestApplicationControllerLifecycleHealth(t *testing.T) {
	t.Run("setup failure", func(t *testing.T) {
		health := newControllerHealth(true)
		err := runApplicationControllerLifecycle(context.Background(), health, func() (applicationControllerRunner, string, error) {
			return nil, "", errors.New("cannot discover runtime")
		})
		if err == nil || !strings.Contains(err.Error(), "cannot discover runtime") {
			t.Fatalf("error = %v", err)
		}
		if snapshot := health.snapshot(); snapshot.State != controllerStateFailed || !strings.Contains(snapshot.Error, "cannot discover runtime") {
			t.Fatalf("health = %+v", snapshot)
		}
	})

	t.Run("startup failure never reports ready", func(t *testing.T) {
		health := newControllerHealth(true)
		stateAtStart := make(chan controllerState, 1)
		err := runApplicationControllerLifecycle(context.Background(), health, func() (applicationControllerRunner, string, error) {
			if snapshot := health.snapshot(); snapshot.State != controllerStateStarting {
				t.Fatalf("health before Start = %+v, want starting", snapshot)
			}
			return testApplicationControllerRunner{
				ready: make(chan struct{}),
				start: func(context.Context) error {
					stateAtStart <- health.snapshot().State
					return errors.New("start failed")
				},
			}, "test", nil
		})
		if err == nil || !strings.Contains(err.Error(), "start failed") {
			t.Fatalf("error = %v", err)
		}
		if state := <-stateAtStart; state != controllerStateStarting {
			t.Fatalf("health at Start = %q, want starting", state)
		}
		if snapshot := health.snapshot(); snapshot.State != controllerStateFailed || !strings.Contains(snapshot.Error, "start failed") {
			t.Fatalf("health = %+v", snapshot)
		}
	})

	t.Run("runtime failure after ready", func(t *testing.T) {
		health := newControllerHealth(true)
		ready := make(chan struct{})
		started := make(chan struct{})
		fail := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- runApplicationControllerLifecycle(context.Background(), health, func() (applicationControllerRunner, string, error) {
				return testApplicationControllerRunner{
					ready: ready,
					start: func(context.Context) error {
						close(started)
						<-fail
						return errors.New("watch stopped")
					},
				}, "test", nil
			})
		}()
		<-started
		if snapshot := health.snapshot(); snapshot.State != controllerStateStarting {
			t.Fatalf("pre-ready health = %+v", snapshot)
		}
		close(ready)
		waitForControllerState(t, health, controllerStateReady)
		close(fail)
		if err := <-done; err == nil || !strings.Contains(err.Error(), "watch stopped") {
			t.Fatalf("error = %v", err)
		}
		if snapshot := health.snapshot(); snapshot.State != controllerStateFailed || !strings.Contains(snapshot.Error, "watch stopped") {
			t.Fatalf("health = %+v", snapshot)
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		health := newControllerHealth(true)
		ctx, cancel := context.WithCancel(context.Background())
		ready := make(chan struct{})
		started := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- runApplicationControllerLifecycle(ctx, health, func() (applicationControllerRunner, string, error) {
				return testApplicationControllerRunner{
					ready: ready,
					start: func(runCtx context.Context) error {
						close(started)
						<-runCtx.Done()
						return runCtx.Err()
					},
				}, "test", nil
			})
		}()
		<-started
		if health.ready() {
			t.Fatal("application health was ready before manager startup signal")
		}
		close(ready)
		waitForControllerState(t, health, controllerStateReady)
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
		if snapshot := health.snapshot(); snapshot.State != controllerStateStopped {
			t.Fatalf("health = %+v", snapshot)
		}
	})
}

func TestApplicationControllerRequiresBaseDomainAndRuntime(t *testing.T) {
	t.Setenv("FAROS_APP_BASE_DOMAIN", "")
	t.Setenv("KRO_KUBECONFIG", "")
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	if applicationControllerConfigured() {
		t.Fatal("empty base domain made application controller mandatory")
	}
	t.Setenv("FAROS_APP_BASE_DOMAIN", " apps.example.test ")
	if applicationControllerConfigured() {
		t.Fatal("base domain without a runtime target made application controller mandatory")
	}
	t.Setenv("KRO_KUBECONFIG", " /tmp/kro.kubeconfig ")
	if !applicationControllerConfigured() {
		t.Fatal("base domain and explicit runtime target did not require application controller")
	}
}

func TestStartApplicationControllerReportsConfiguredSetupFailure(t *testing.T) {
	t.Setenv("FAROS_APP_BASE_DOMAIN", "apps.example.test")
	t.Setenv("KRO_KUBECONFIG", filepath.Join(t.TempDir(), "missing-kubeconfig"))
	health := newControllerHealth(true)
	result := startApplicationController(context.Background(), &rest.Config{Host: "https://provider.example"}, health)
	if result == nil {
		t.Fatal("configured application controller returned no lifecycle result")
	}
	if err := <-result; err == nil || !strings.Contains(err.Error(), "no kro runtime cluster") {
		t.Fatalf("setup error = %v", err)
	}
	if snapshot := health.snapshot(); snapshot.State != controllerStateFailed {
		t.Fatalf("health = %+v", snapshot)
	}
}

func TestStartApplicationControllerKeepsDisabledFeatureOptional(t *testing.T) {
	t.Setenv("FAROS_APP_BASE_DOMAIN", "")
	health := newControllerHealth(true)
	if result := startApplicationController(context.Background(), &rest.Config{}, health); result != nil {
		t.Fatal("disabled application controller returned a lifecycle channel")
	}
	if snapshot := health.snapshot(); snapshot.Required || snapshot.State != controllerStateRESTOnly {
		t.Fatalf("disabled application health = %+v", snapshot)
	}
}
