// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package executor

import (
	"context"
	"errors"
	"testing"
	"time"
)

// blockedExecutor returns a started executor whose single worker is parked on
// a job and whose queue is filled to capacity, plus the release func that
// unblocks the worker.
func blockedExecutor(t *testing.T) (*InProcess, func()) {
	t.Helper()
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	e := NewInProcess(func(ctx context.Context, _ Job) error {
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	}, 1, time.Minute)
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Stop)
	// First job occupies the worker …
	if err := e.Submit(context.Background(), Job{ID: "busy"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker never picked up the first job")
	}
	// … then fill every queue slot.
	for i := 0; i < cap(e.jobs); i++ {
		if err := e.Submit(context.Background(), Job{ID: "fill"}); err != nil {
			t.Fatalf("slot %d: %v", i, err)
		}
	}
	if e.Depth() != cap(e.jobs) {
		t.Fatalf("queue depth %d, want %d", e.Depth(), cap(e.jobs))
	}
	return e, func() { close(release) }
}

func TestSubmitBlocksThenFailsWhenContextEnds(t *testing.T) {
	e, release := blockedExecutor(t)
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	begin := time.Now()
	err := e.Submit(ctx, Job{ID: "overflow", Kind: KindChannel, SourceName: "slack-1"})
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("want ErrQueueFull, got %v", err)
	}
	// It waited for the context rather than failing immediately.
	if waited := time.Since(begin); waited < 100*time.Millisecond {
		t.Fatalf("Submit returned after %s; it should have waited on the context", waited)
	}
	if e.Depth() != cap(e.jobs) {
		t.Fatalf("a refused job must not occupy a slot: depth %d", e.Depth())
	}
}

func TestSubmitSucceedsWhenASlotFrees(t *testing.T) {
	e, release := blockedExecutor(t)

	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		done <- e.Submit(ctx, Job{ID: "late"})
	}()
	// Nothing frees up for a moment; the submitter must still be waiting.
	select {
	case err := <-done:
		t.Fatalf("Submit returned early: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	release() // the worker drains the queue, freeing slots
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Submit should succeed once a slot frees: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Submit never returned after the queue drained")
	}
}

func TestSubmitCancelledContextFailsFast(t *testing.T) {
	e, release := blockedExecutor(t)
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	begin := time.Now()
	err := e.Submit(ctx, Job{ID: "cancelled"})
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("want ErrQueueFull, got %v", err)
	}
	if time.Since(begin) > time.Second {
		t.Fatal("a cancelled context must not wait for SubmitWait")
	}
}

func TestSubmitBeforeStart(t *testing.T) {
	e := NewInProcess(func(context.Context, Job) error { return nil }, 1, time.Minute)
	if err := e.Submit(context.Background(), Job{}); !errors.Is(err, ErrStopped) {
		t.Fatalf("want ErrStopped, got %v", err)
	}
}
