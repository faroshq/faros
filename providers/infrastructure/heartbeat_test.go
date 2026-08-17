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

func TestHeartbeatCanSendFollowsControllerReadiness(t *testing.T) {
	if !heartbeatCanSend(nil) {
		t.Fatal("heartbeat without health should remain compatible")
	}
	if !heartbeatCanSend(newControllerHealth(false)) {
		t.Fatal("REST-only mode should heartbeat")
	}
	required := newControllerHealth(true)
	if heartbeatCanSend(required) {
		t.Fatal("starting required controller must not heartbeat")
	}
	required.markFailed(errors.New("manager exited"))
	if heartbeatCanSend(required) {
		t.Fatal("failed required controller must not heartbeat")
	}
	required.markReady()
	if !heartbeatCanSend(required) {
		t.Fatal("ready required controller should heartbeat")
	}
	required.markStopped(errors.New("shutdown"))
	if heartbeatCanSend(required) {
		t.Fatal("stopped required controller must not heartbeat")
	}
}

func TestHeartbeatRequiresEveryConfiguredController(t *testing.T) {
	platform := newControllerHealth(true)
	application := newControllerHealth(true)
	platform.markReady()
	application.markFailed(errors.New("application manager exited"))
	if heartbeatCanSend(platform, application) {
		t.Fatal("configured application failure must suppress heartbeat")
	}
	application.markReady()
	if !heartbeatCanSend(platform, application) {
		t.Fatal("heartbeat should resume when both configured controllers are ready")
	}
	if !heartbeatCanSend(platform, newControllerHealth(false)) {
		t.Fatal("disabled application controller must not become mandatory")
	}
}
