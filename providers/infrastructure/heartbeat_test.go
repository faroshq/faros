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
	instance := newControllerHealth(true)
	platform.markReady()
	instance.markFailed(errors.New("instance manager exited"))
	if heartbeatCanSend(platform, instance) {
		t.Fatal("configured instance failure must suppress heartbeat")
	}
	instance.markReady()
	if !heartbeatCanSend(platform, instance) {
		t.Fatal("heartbeat should resume when both configured controllers are ready")
	}
	if !heartbeatCanSend(platform, newControllerHealth(false)) {
		t.Fatal("disabled instance controller must not become mandatory")
	}
}
