// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"errors"
	"testing"
)

func TestControllerReadinessCacheSyncCannotRearmAfterStop(t *testing.T) {
	var ready, stopped int
	lifecycle := &controllerReadiness{
		onReady:   func() { ready++ },
		onStopped: func(error) { stopped++ },
	}

	// This ordering can occur when manager.Start returns while the cache-sync
	// waiter is concurrently unwinding. A terminal stop must win; the late
	// cache-sync callback cannot make /readyz healthy again.
	lifecycle.stopped(errors.New("manager exited"))
	lifecycle.ready()
	lifecycle.stopped(errors.New("second stop"))

	if ready != 0 {
		t.Fatalf("ready callback count = %d, want 0 after terminal stop", ready)
	}
	if stopped != 1 {
		t.Fatalf("stopped callback count = %d, want exactly one", stopped)
	}
}
