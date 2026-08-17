// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHeartbeatOnlyPublishesWhenControllerReady(t *testing.T) {
	requests := make(chan struct{}, 1)
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		select {
		case requests <- struct{}{}:
		default:
		}
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}

	var ready atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runHeartbeatLoop(ctx, &ready, client, "https://hub.example", "deployments", "")
	}()
	select {
	case <-requests:
		t.Fatal("heartbeat published while controller was unready")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	<-done

	ready.Store(true)
	ctx, cancel = context.WithCancel(context.Background())
	done = make(chan struct{})
	go func() {
		defer close(done)
		runHeartbeatLoop(ctx, &ready, client, "https://hub.example", "deployments", "")
	}()
	select {
	case <-requests:
	case <-time.After(time.Second):
		t.Fatal("ready controller did not publish heartbeat")
	}
	cancel()
	<-done
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
