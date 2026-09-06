// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package api

import (
	"context"
	"log"
	"time"

	"github.com/faroshq/provider-agents/store"
)

// startRunLease renews a run's durable owner lease while its model/tool loop is
// active. Checkpoints are intentionally sparse, and one external call can be
// slow; tying liveness to checkpoint callbacks would let a healthy run be
// recovered mid-call. A renewal failure cancels the loop so an expired lease
// cannot leave two owners executing concurrently.
func (s *Server) startRunLease(ctx context.Context, run taskRun, cancel context.CancelFunc) func() {
	if run.ExecutionOwner == "" || run.ExecutionEpoch <= 0 {
		return func() {}
	}
	leaseCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(store.RunLeaseDuration / 3)
		defer ticker.Stop()
		for {
			select {
			case <-leaseCtx.Done():
				return
			case now := <-ticker.C:
				if err := s.store.RenewRun(leaseCtx, run.Scope, run.RunID, run.ExecutionOwner, run.ExecutionEpoch, now.UTC()); err != nil {
					log.Printf("run lease: run %s owner %s: %v", run.RunID, run.ExecutionOwner, err)
					cancel()
					return
				}
			}
		}
	}()
	return func() {
		stop()
		<-done
	}
}
