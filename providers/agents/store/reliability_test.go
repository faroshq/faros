// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestMemoryStore_StaleClaimFencesPreviousOwner(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	scope := Scope{OrgUUID: "org", WorkspaceUUID: "workspace", AgentName: "agent"}
	old := time.Now().UTC().Add(-time.Hour)
	oldLease := old.Add(time.Minute)
	if err := s.SaveRun(ctx, scope, Run{
		ID: "run", AgentName: scope.AgentName, Phase: RunPhaseRunning,
		Checkpoint: []byte(`{"engine":{}}`), UpdatedAt: old, CreatedAt: old,
		ExecutionOwner: "old-owner", ExecutionEpoch: 4, LeaseUntil: &oldLease,
	}); err != nil {
		t.Fatal(err)
	}

	claimed, err := s.ClaimStaleRun(ctx, scope, "run", old, "recovery-owner", time.Now().UTC())
	if err != nil {
		t.Fatalf("claim stale: %v", err)
	}
	if claimed.ExecutionOwner != "recovery-owner" || claimed.ExecutionEpoch != 5 || claimed.Attempt != 1 {
		t.Fatalf("claim = %+v", claimed)
	}
	if err := s.SaveRunOwned(ctx, scope, Run{ID: "run", Phase: RunPhaseSucceeded, UpdatedAt: time.Now().UTC()}, "old-owner", 4); !errors.Is(err, ErrRunLeaseLost) {
		t.Fatalf("old owner save error = %v, want ErrRunLeaseLost", err)
	}

	var wins sync.WaitGroup
	wins.Add(2)
	results := make(chan error, 2)
	for i := range 2 {
		go func(i int) {
			defer wins.Done()
			_, err := s.ClaimStaleRun(ctx, scope, "run", claimed.UpdatedAt, "racer-"+string(rune('a'+i)), time.Now().UTC())
			results <- err
		}(i)
	}
	wins.Wait()
	close(results)
	var successes int
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrRunNotStale) {
			t.Fatalf("competing claim error = %v", err)
		}
	}
	if successes != 0 {
		t.Fatalf("a leased recovery owner was stolen by %d racers", successes)
	}
}

func TestMemoryStore_DurableIntentIsIdempotentAndClaimableOnce(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	scope := Scope{OrgUUID: "org", WorkspaceUUID: "workspace", AgentName: "agent"}
	now := time.Now().UTC()
	intent := Run{ID: "schedule-run", AgentName: scope.AgentName, Phase: RunPhasePending,
		IdempotencyKey: "schedule:one:occurrence", CreatedAt: now, UpdatedAt: now}
	first, created, err := s.CreateRunIfAbsent(ctx, scope, intent)
	if err != nil || !created {
		t.Fatalf("first intent: created=%v err=%v", created, err)
	}
	retry := intent
	retry.ID += "-retry"
	second, created, err := s.CreateRunIfAbsent(ctx, scope, retry)
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("retry intent: run=%+v created=%v err=%v", second, created, err)
	}
	if _, err := s.ClaimRun(ctx, scope, intent.ID, "worker", now); err != nil {
		t.Fatalf("claim intent: %v", err)
	}
	if _, err := s.ClaimRun(ctx, scope, intent.ID, "duplicate", now); !errors.Is(err, ErrRunAlreadyClaimed) {
		t.Fatalf("duplicate claim error = %v", err)
	}
}
