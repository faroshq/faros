package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEdgeReadinessTracksControllerManager(t *testing.T) {
	health := &edgeControllerHealth{}
	handler := edgeReadyHandler(health)

	notReady := httptest.NewRecorder()
	handler.ServeHTTP(notReady, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if notReady.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz before controller start = %d, want %d", notReady.Code, http.StatusServiceUnavailable)
	}

	health.markReady()
	ready := httptest.NewRecorder()
	handler.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusOK {
		t.Fatalf("readyz after controller start = %d, want %d", ready.Code, http.StatusOK)
	}

	health.markFailed()
	failed := httptest.NewRecorder()
	handler.ServeHTTP(failed, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if failed.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz after controller exit = %d, want %d", failed.Code, http.StatusServiceUnavailable)
	}
}

func TestEdgeControllerReadyRunnableTracksManagerLifetime(t *testing.T) {
	health := &edgeControllerHealth{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- edgeControllerReadyRunnable(health).Start(ctx)
	}()

	waitForEdgeHealth(t, health, true)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("ready runnable returned an error: %v", err)
	}
}

func TestSuperviseEdgeControllerFailsClosed(t *testing.T) {
	health := &edgeControllerHealth{}
	health.markReady()
	want := errors.New("setup failed")

	err := superviseEdgeController(context.Background(), health, func(context.Context) error {
		if health.isReady() {
			t.Fatal("health remained ready while a new controller attempt started")
		}
		return want
	})

	if !errors.Is(err, want) {
		t.Fatalf("supervisor error = %v, want %v", err, want)
	}
	if health.isReady() {
		t.Fatal("setup failure left controller ready")
	}
}

func TestSuperviseEdgeControllerClearsReadinessAfterExit(t *testing.T) {
	health := &edgeControllerHealth{}
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	want := errors.New("manager stopped")

	go func() {
		done <- superviseEdgeController(context.Background(), health, func(context.Context) error {
			health.markReady() // the real manager transitions through its ready runnable
			close(started)
			<-release
			return want
		})
	}()

	<-started
	waitForEdgeHealth(t, health, true)
	close(release)
	if err := <-done; !errors.Is(err, want) {
		t.Fatalf("supervisor error = %v, want %v", err, want)
	}
	if health.isReady() {
		t.Fatal("controller exit left readiness open")
	}
}

func TestEdgeControllerExitErrorTreatsParentCancellationAsShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := edgeControllerExitError(ctx, context.Canceled); err != nil {
		t.Fatalf("parent cancellation was fatal: %v", err)
	}
	if err := edgeControllerExitError(ctx, context.DeadlineExceeded); err != nil {
		t.Fatalf("parent deadline cancellation was fatal: %v", err)
	}

	want := errors.New("manager failed")
	if err := edgeControllerExitError(ctx, want); !errors.Is(err, want) {
		t.Fatalf("manager failure = %v, want wrapped %v", err, want)
	}
	if err := edgeControllerExitError(context.Background(), context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected standalone cancellation = %v, want wrapped context cancellation", err)
	}
}

func waitForEdgeHealth(t *testing.T, health *edgeControllerHealth, ready bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for health.isReady() != ready {
		if time.Now().After(deadline) {
			t.Fatalf("controller ready = %t, want %t", health.isReady(), ready)
		}
		time.Sleep(time.Millisecond)
	}
}
