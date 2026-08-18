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
	"sync/atomic"
	"testing"
	"time"
)

func TestRunHeartbeatLoopGatesBeforeAndAfterReadiness(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var state atomic.Int32 // 0: not ready, 1: ready, 2: stopped
	var sends atomic.Int32
	sent := make(chan struct{}, 8)
	go runHeartbeatLoop(ctx, func() error {
		switch state.Load() {
		case 1:
			return nil
		case 2:
			return errors.New("controller manager stopped")
		default:
			return errors.New("provider not ready")
		}
	}, 5*time.Millisecond, func(context.Context) error {
		sends.Add(1)
		sent <- struct{}{}
		return nil
	})

	select {
	case <-sent:
		t.Fatal("heartbeat sent before readiness")
	case <-time.After(25 * time.Millisecond):
	}

	state.Store(1)
	select {
	case <-sent:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("heartbeat was not sent after readiness")
	}

	state.Store(2)
	sentBeforeStop := sends.Load()
	time.Sleep(25 * time.Millisecond)
	if got := sends.Load(); got != sentBeforeStop {
		t.Fatalf("heartbeat sent after readiness failure: before=%d after=%d", sentBeforeStop, got)
	}
}
