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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/client-go/rest"
)

func TestControllerOptionsForRetryableManagerAllowsStableNames(t *testing.T) {
	options := controllerOptionsForRetryableManager()
	if options.SkipNameValidation == nil || !*options.SkipNameValidation {
		t.Fatal("retryable manager must allow stable controller names across process-lifetime rebuilds")
	}
}

func TestControllerReadyRunnableMarksHealthWhenLaunched(t *testing.T) {
	health := newControllerHealth(true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		if err := controllerReadyRunnable(health, nil).Start(ctx); err != nil {
			t.Errorf("controller ready runnable: %v", err)
		}
		close(done)
	}()

	waitForControllerState(t, health, controllerStateReady)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("controller ready runnable did not stop after cancellation")
	}
}

func TestControllerReadyRunnableWaitsForSeedResources(t *testing.T) {
	health := newControllerHealth(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var published atomic.Bool
	checked := make(chan struct{})
	var checkedOnce sync.Once
	done := make(chan error, 1)

	go func() {
		done <- controllerReadyRunnableWithInterval(health, func(context.Context) (bool, error) {
			checkedOnce.Do(func() { close(checked) })
			return published.Load(), nil
		}, time.Millisecond).Start(ctx)
	}()

	waitForSignal(t, checked, "initial publication check")
	if got := health.snapshot().State; got != controllerStateStarting {
		t.Fatalf("controller state before publication = %q, want starting", got)
	}
	published.Store(true)
	waitForControllerState(t, health, controllerStateReady)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("controller ready runnable: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("controller ready runnable did not stop after cancellation")
	}
}

func TestControllerReadyRunnableTracksPublicationTransitions(t *testing.T) {
	health := newControllerHealth(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var published atomic.Bool
	published.Store(true)
	done := make(chan error, 1)

	go func() {
		done <- controllerReadyRunnableWithInterval(health, func(context.Context) (bool, error) {
			return published.Load(), nil
		}, time.Millisecond).Start(ctx)
	}()

	waitForControllerState(t, health, controllerStateReady)
	published.Store(false)
	waitForControllerState(t, health, controllerStateStarting)
	published.Store(true)
	waitForControllerState(t, health, controllerStateReady)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("controller ready runnable: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("controller ready runnable did not stop after cancellation")
	}
}

func TestControllerReadyRunnableSlowsChecksAfterReady(t *testing.T) {
	health := newControllerHealth(true)
	ctx, cancel := context.WithCancel(context.Background())
	checks := make(chan struct{}, 2)
	done := make(chan error, 1)

	go func() {
		done <- controllerReadyRunnableWithIntervals(health, func(context.Context) (bool, error) {
			checks <- struct{}{}
			return true, nil
		}, time.Millisecond, 100*time.Millisecond).Start(ctx)
	}()

	waitForSignal(t, checks, "initial publication check")
	waitForControllerState(t, health, controllerStateReady)
	select {
	case <-checks:
		t.Fatal("ready publication check repeated at the startup cadence")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("controller ready runnable: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("controller ready runnable did not stop after cancellation")
	}
}

func TestControllerHealthLifecycle(t *testing.T) {
	health := newControllerHealth(true)
	if got := health.snapshot(); got.State != controllerStateStarting || !got.Required {
		t.Fatalf("initial controller health = %+v, want required/starting", got)
	}
	if health.ready() {
		t.Fatal("required controller should not be ready before start")
	}

	health.markFailed(errors.New("endpoint slice unavailable"))
	if got := health.snapshot(); got.State != controllerStateFailed || got.Error != "endpoint slice unavailable" {
		t.Fatalf("failed controller health = %+v, want failure", got)
	}
	health.markStarting()
	health.markReady()
	if got := health.snapshot(); got.State != controllerStateReady || !health.ready() {
		t.Fatalf("running controller health = %+v, want ready", got)
	}
	health.markStopped(context.Canceled)
	if got := health.snapshot(); got.State != controllerStateStopped || got.Error != context.Canceled.Error() || health.ready() {
		t.Fatalf("stopped controller health = %+v, want stopped/not ready", got)
	}
}

func TestRunControllerManagerRetriesSetupAndPostStartFailures(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	health := newControllerHealth(true)
	var loadCalls atomic.Int32
	var startCalls atomic.Int32
	firstLoadDone := make(chan struct{})
	firstStartDone := make(chan struct{})
	firstRetry := make(chan struct{})
	allowFirstRetry := make(chan struct{})
	secondRetry := make(chan struct{})
	allowSecondRetry := make(chan struct{})
	secondReady := make(chan struct{})
	done := make(chan struct{})

	loadConfig := func() (*rest.Config, error) {
		if loadCalls.Add(1) == 1 {
			close(firstLoadDone)
			return nil, errors.New("provider kubeconfig is not bootstrapped")
		}
		return &rest.Config{}, nil
	}
	start := func(startCtx context.Context, _ *rest.Config, state *controllerHealth) error {
		if startCalls.Add(1) == 1 {
			close(firstStartDone)
			return errors.New("manager exited after start")
		}
		state.markReady()
		close(secondReady)
		<-startCtx.Done()
		return startCtx.Err()
	}
	var retryCalls atomic.Int32
	retryGate := func(retryCtx context.Context, _ time.Duration) bool {
		call := retryCalls.Add(1)
		var entered, release chan struct{}
		switch call {
		case 1:
			entered, release = firstRetry, allowFirstRetry
		case 2:
			entered, release = secondRetry, allowSecondRetry
		default:
			return false
		}
		close(entered)
		select {
		case <-release:
			return true
		case <-retryCtx.Done():
			return false
		}
	}

	go func() {
		runControllerManagerWithRetryGate(ctx, health, loadConfig, start, time.Second, retryGate)
		close(done)
	}()

	waitForSignal(t, firstLoadDone, "initial config load")
	waitForSignal(t, firstRetry, "first retry gate")
	if got := health.snapshot(); got.State != controllerStateFailed || got.Error != "provider kubeconfig is not bootstrapped" {
		t.Fatalf("first failed controller health = %+v", got)
	}
	close(allowFirstRetry)
	waitForSignal(t, firstStartDone, "first manager start")
	waitForSignal(t, secondRetry, "second retry gate")
	if got := health.snapshot(); got.State != controllerStateFailed || got.Error != "manager exited after start" {
		t.Fatalf("second failed controller health = %+v", got)
	}
	close(allowSecondRetry)
	waitForSignal(t, secondReady, "recovered manager start")
	if got := startCalls.Load(); got != 2 {
		t.Fatalf("manager starts = %d, want 2", got)
	}
	if !health.ready() {
		t.Fatal("controller should be ready after recovery")
	}

	cancel()
	waitForSignal(t, done, "controller loop shutdown")
	if got := health.snapshot().State; got != controllerStateStopped {
		t.Fatalf("final controller state = %q, want stopped", got)
	}
}

func TestRunControllerManagerKeepsRESTOnlyModeAvailable(t *testing.T) {
	health := newControllerHealth(false)
	var loadCalls atomic.Int32
	runControllerManager(context.Background(), health, func() (*rest.Config, error) {
		loadCalls.Add(1)
		return nil, errors.New("must not load")
	}, func(context.Context, *rest.Config, *controllerHealth) error {
		return errors.New("must not start")
	}, 0)
	if got := health.snapshot(); got.State != controllerStateRESTOnly || !health.ready() {
		t.Fatalf("REST-only health = %+v, want ready/rest-only", got)
	}
	if loadCalls.Load() != 0 {
		t.Fatalf("REST-only lifecycle loaded config %d times", loadCalls.Load())
	}
}

func TestControllerModeFromEnv(t *testing.T) {
	t.Setenv("INFRASTRUCTURE_KUBECONFIG", "")
	t.Setenv("INFRASTRUCTURE_CONTROLLER_KUBECONFIG", "")
	t.Setenv("KUBECONFIG", "")
	t.Setenv("INFRASTRUCTURE_REST_ONLY", "")
	t.Setenv("INFRASTRUCTURE_CONTROLLER_MODE", "")
	if got := controllerModeFromEnv(false); got != controllerModeRESTOnly {
		t.Fatalf("unconfigured mode = %q, want rest-only", got)
	}
	if got := controllerModeFromEnv(true); got != controllerModeRequired {
		t.Fatalf("configured mode = %q, want required", got)
	}
	t.Setenv("INFRASTRUCTURE_CONTROLLER_MODE", "rest-only")
	if got := controllerModeFromEnv(true); got != controllerModeRESTOnly {
		t.Fatalf("explicit mode = %q, want rest-only", got)
	}
	t.Setenv("INFRASTRUCTURE_CONTROLLER_MODE", "typo")
	if got := controllerModeFromEnv(false); got != controllerModeRequired {
		t.Fatalf("invalid mode = %q, want required", got)
	}
}

func waitForControllerState(t *testing.T, health *controllerHealth, want controllerState) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if health.snapshot().State == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("controller state = %q, want %q", health.snapshot().State, want)
}

func waitForSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}
