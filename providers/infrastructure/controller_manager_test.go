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
	"testing"
)

func TestControllerReadinessCacheSyncCannotRearmAfterStop(t *testing.T) {
	var ready, stopped int
	lifecycle := &controllerReadiness{
		onReady:   func() { ready++ },
		onStopped: func(error) { stopped++ },
	}
	term := lifecycle.newTerm()

	// This ordering can occur when manager.Start returns while the cache-sync
	// waiter is concurrently unwinding. A terminal stop must win; the late
	// cache-sync callback cannot make /readyz healthy again.
	term.stopped(errors.New("manager exited"))
	term.ready()
	term.stopped(errors.New("second stop"))

	if ready != 0 {
		t.Fatalf("ready callback count = %d, want 0 after terminal stop", ready)
	}
	if stopped != 1 {
		t.Fatalf("stopped callback count = %d, want exactly one", stopped)
	}
}

func TestControllerReadinessStaleTermCannotRearmAfterNewTerm(t *testing.T) {
	var backendReady, ready, stopped int
	lifecycle := &controllerReadiness{
		onBackendReady: func() { backendReady++ },
		onReady:        func() { ready++ },
		onStopped:      func(error) { stopped++ },
	}
	oldTerm := lifecycle.newTerm()
	newTerm := lifecycle.newTerm()

	// The old manager's cache and shutdown goroutines may report after a new
	// election term has started. Neither callback may alter the new term.
	oldTerm.backendReady()
	oldTerm.ready()
	oldTerm.stopped(errors.New("old term exited"))

	newTerm.backendReady()
	newTerm.ready()

	if backendReady != 1 {
		t.Fatalf("backend-ready callback count = %d, want only the current term", backendReady)
	}
	if ready != 1 {
		t.Fatalf("ready callback count = %d, want only the current term", ready)
	}
	if stopped != 1 {
		t.Fatalf("stopped callback count = %d, want the replaced term transition", stopped)
	}
}

func TestControllerReadinessLeaderElectionFailureStopsReadyTerm(t *testing.T) {
	var stoppedErr error
	lifecycle := &controllerReadiness{
		onReady:   func() {},
		onStopped: func(err error) { stoppedErr = err },
	}
	term := lifecycle.newTerm()
	term.ready()

	electionErr := errors.New("lease client failed")
	stopControllerReadinessOnLeaderElectionError(lifecycle, electionErr)
	term.ready()

	if !errors.Is(stoppedErr, electionErr) {
		t.Fatalf("stopped error = %v, want %v", stoppedErr, electionErr)
	}

	// Context shutdown is the normal termination path and must not invoke the
	// failure transition.
	stoppedErr = nil
	active := lifecycle.newTerm()
	stopControllerReadinessOnLeaderElectionError(lifecycle, context.Canceled)
	active.ready()
	if stoppedErr != nil {
		t.Fatalf("stopped error after context cancellation = %v, want nil", stoppedErr)
	}
}
