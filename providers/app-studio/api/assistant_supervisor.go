// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package api

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/faroshq/provider-app-studio/store"
)

const projectAssistantTextSnapshotInterval = 250 * time.Millisecond

// projectAssistantRunSnapshot is the complete durable view sent to a
// subscriber. A consumer replaces its current view; it never needs event
// replay to reconstruct assistant state.
type projectAssistantRunSnapshot struct {
	Run     store.AssistantRun `json:"run"`
	Message store.Message      `json:"message"`
}

type projectAssistantSupervisor struct {
	store  store.Store
	ctx    context.Context
	cancel context.CancelFunc

	mu   sync.Mutex
	runs map[projectAssistantRunKey]*projectAssistantSupervisedRun
}

type projectAssistantSupervisedRun struct {
	transitionMu     sync.Mutex
	scope            store.Scope
	run              store.AssistantRun
	message          store.Message
	committedRun     store.AssistantRun
	committedMessage store.Message
	cancel           context.CancelCauseFunc
	subscribers      map[uint64]chan projectAssistantRunSnapshot
	nextSubID        uint64
	lastText         time.Time
	textFlush        *time.Timer
	workerStarted    bool
}

type projectAssistantSnapshotAccumulator struct {
	supervisor *projectAssistantSupervisor
	key        projectAssistantRunKey
	runID      string
}

func newProjectAssistantSupervisor(parent context.Context, msgStore store.Store) *projectAssistantSupervisor {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	return &projectAssistantSupervisor{
		store: msgStore, ctx: ctx, cancel: cancel, runs: map[projectAssistantRunKey]*projectAssistantSupervisedRun{},
	}
}

func (s *projectAssistantSupervisor) Shutdown(ctx context.Context) {
	if s == nil {
		return
	}
	s.mu.Lock()
	accumulators := make([]*projectAssistantSnapshotAccumulator, 0, len(s.runs))
	for key, active := range s.runs {
		if !assistantRunTerminal(active.run.Status) {
			accumulators = append(accumulators, &projectAssistantSnapshotAccumulator{supervisor: s, key: key, runID: active.run.ID})
		}
	}
	s.mu.Unlock()
	for _, accumulator := range accumulators {
		_ = accumulator.SetStatus(ctx, store.AssistantRunStatusInterrupted)
	}
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *projectAssistantSupervisor) Attach(scope store.Scope, run store.AssistantRun, message store.Message) (*projectAssistantSnapshotAccumulator, error) {
	if s == nil || s.store == nil {
		return nil, errors.New("assistant supervisor store not configured")
	}
	key := projectAssistantRunKey{OrgUUID: scope.OrgUUID, WorkspaceUUID: scope.WorkspaceUUID, ProjectName: scope.ProjectName}
	if !key.valid() || run.ID == "" {
		return nil, errors.New("assistant supervisor scope and run id are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.runs[key]; existing != nil {
		if existing.run.ID == run.ID {
			return &projectAssistantSnapshotAccumulator{supervisor: s, key: key, runID: run.ID}, nil
		}
		return nil, store.ErrAssistantRunConflict
	}
	_, cancel := context.WithCancelCause(s.ctx)
	s.runs[key] = &projectAssistantSupervisedRun{scope: scope, run: run, message: message, committedRun: run, committedMessage: message, cancel: cancel, subscribers: map[uint64]chan projectAssistantRunSnapshot{}}
	return &projectAssistantSnapshotAccumulator{supervisor: s, key: key, runID: run.ID}, nil
}

func (s *projectAssistantSupervisor) accumulatorFor(scope store.Scope, runID string) *projectAssistantSnapshotAccumulator {
	if s == nil {
		return nil
	}
	key := projectAssistantRunKey{OrgUUID: scope.OrgUUID, WorkspaceUUID: scope.WorkspaceUUID, ProjectName: scope.ProjectName}
	s.mu.Lock()
	defer s.mu.Unlock()
	if active := s.runs[key]; active != nil && active.run.ID == runID {
		return &projectAssistantSnapshotAccumulator{supervisor: s, key: key, runID: runID}
	}
	return nil
}

// Start deliberately ignores starterCtx. The worker is derived from the
// provider lifecycle so an HTTP disconnect can only detach a subscriber.
func (s *projectAssistantSupervisor) Start(_ context.Context, scope store.Scope, run store.AssistantRun, message store.Message, worker func(context.Context, *projectAssistantSnapshotAccumulator)) error {
	acc, err := s.Attach(scope, run, message)
	if err != nil {
		return err
	}
	s.mu.Lock()
	active := s.runs[acc.key]
	if active.workerStarted {
		s.mu.Unlock()
		return store.ErrAssistantRunConflict
	}
	active.workerStarted = true
	workerCtx, _ := context.WithCancelCause(s.ctx)
	// Use the active cancellation function created by Attach; the context must
	// share it, rather than derive from the initiating request.
	workerCtx, active.cancel = context.WithCancelCause(s.ctx)
	s.mu.Unlock()
	go func() {
		defer s.finish(acc.key, run.ID)
		worker(workerCtx, acc)
	}()
	return nil
}

func (s *projectAssistantSupervisor) finish(key projectAssistantRunKey, runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if active := s.runs[key]; active != nil && active.run.ID == runID {
		// Permission/input checkpoints deliberately retain the in-memory
		// snapshot, but no worker owns them once the Eino segment returns.
		active.workerStarted = false
		if assistantRunTerminal(active.run.Status) {
			delete(s.runs, key)
		}
	}
}

func assistantRunTerminal(status store.AssistantRunStatus) bool {
	switch status {
	case store.AssistantRunStatusCompleted, store.AssistantRunStatusAborted, store.AssistantRunStatusFailed, store.AssistantRunStatusInterrupted:
		return true
	}
	return false
}

func (s *projectAssistantSupervisor) Abort(scope store.Scope, runID string) bool {
	if s == nil {
		return false
	}
	key := projectAssistantRunKey{OrgUUID: scope.OrgUUID, WorkspaceUUID: scope.WorkspaceUUID, ProjectName: scope.ProjectName}
	s.mu.Lock()
	active := s.runs[key]
	if active == nil || active.run.ID != runID {
		s.mu.Unlock()
		return false
	}
	s.mu.Unlock()
	active.transitionMu.Lock()
	defer active.transitionMu.Unlock()
	s.mu.Lock()
	if current := s.runs[key]; current != active || active.run.ID != runID {
		s.mu.Unlock()
		return false
	}
	// A worker may have finished between the initial lookup and transition
	// ownership. Terminal state is immutable: Abort never revives it.
	if assistantRunTerminal(active.run.Status) {
		s.mu.Unlock()
		return active.run.Status == store.AssistantRunStatusAborted
	}
	active.run.Status = store.AssistantRunStatusAborted
	active.run.Revision++
	active.run.UpdatedAt = time.Now().UTC()
	active.message.UpdatedAt = active.run.UpdatedAt
	run, message := active.run, active.message
	active.cancel(context.Canceled)
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), projectMessagePersistTimeout)
	defer cancel()
	if err := s.store.SaveAssistantRunSnapshot(ctx, scope, run, []store.Message{message}, run.Revision-1); err != nil {
		return false
	}
	s.mu.Lock()
	if current := s.runs[key]; current != nil && current.run.ID == runID && current.run.Revision == run.Revision {
		current.committedRun, current.committedMessage = run, message
		for _, subscriber := range current.subscribers {
			s.sendCoalesced(subscriber, projectAssistantRunSnapshot{Run: run, Message: message})
		}
	}
	s.mu.Unlock()
	return true
}

func (s *projectAssistantSupervisor) Subscribe(scope store.Scope, runID string, afterRevision int64) (<-chan projectAssistantRunSnapshot, func(), error) {
	if s == nil {
		return nil, nil, errors.New("assistant supervisor not configured")
	}
	key := projectAssistantRunKey{OrgUUID: scope.OrgUUID, WorkspaceUUID: scope.WorkspaceUUID, ProjectName: scope.ProjectName}
	s.mu.Lock()
	active := s.runs[key]
	if active == nil || active.run.ID != runID {
		s.mu.Unlock()
		return nil, nil, store.ErrAssistantRunNotFound
	}
	if afterRevision >= active.run.Revision && assistantRunTerminal(active.run.Status) {
		s.mu.Unlock()
		return nil, func() {}, nil
	}
	id := active.nextSubID
	active.nextSubID++
	ch := make(chan projectAssistantRunSnapshot, 1)
	active.subscribers[id] = ch
	snapshot := projectAssistantRunSnapshot{Run: active.committedRun, Message: active.committedMessage}
	s.sendCoalesced(ch, snapshot)
	s.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			s.mu.Lock()
			if current := s.runs[key]; current != nil && current.run.ID == runID {
				delete(current.subscribers, id)
			}
			s.mu.Unlock()
		})
	}, nil
}

func (s *projectAssistantSupervisor) sendCoalesced(ch chan projectAssistantRunSnapshot, snapshot projectAssistantRunSnapshot) {
	select {
	case ch <- snapshot:
		return
	default:
	}
	select {
	case <-ch:
	default:
	}
	select {
	case ch <- snapshot:
	default:
	}
}

func (a *projectAssistantSnapshotAccumulator) UpdateText(ctx context.Context, content string, immediate bool) error {
	return a.update(ctx, func(active *projectAssistantSupervisedRun) { active.message.Content = content }, immediate)
}

func (a *projectAssistantSnapshotAccumulator) SetStatus(ctx context.Context, status store.AssistantRunStatus) error {
	return a.update(ctx, func(active *projectAssistantSupervisedRun) { active.run.Status = status }, true)
}

func (a *projectAssistantSnapshotAccumulator) SetMessageMetadata(ctx context.Context, metadata map[string]any) error {
	return a.update(ctx, func(active *projectAssistantSupervisedRun) { active.message.Metadata = metadata }, true)
}

func (a *projectAssistantSnapshotAccumulator) UpdateRun(ctx context.Context, mutate func(*store.AssistantRun)) error {
	return a.update(ctx, func(active *projectAssistantSupervisedRun) { mutate(&active.run) }, true)
}

func (a *projectAssistantSnapshotAccumulator) update(ctx context.Context, mutate func(*projectAssistantSupervisedRun), immediate bool) error {
	if a == nil || a.supervisor == nil {
		return errors.New("assistant snapshot accumulator not configured")
	}
	s := a.supervisor
	s.mu.Lock()
	active := s.runs[a.key]
	if active == nil || active.run.ID != a.runID {
		s.mu.Unlock()
		return store.ErrAssistantRunNotFound
	}
	active.transitionMu.Lock()
	s.mu.Unlock()
	defer active.transitionMu.Unlock()
	s.mu.Lock()
	if current := s.runs[a.key]; current != active || active.run.ID != a.runID {
		s.mu.Unlock()
		return store.ErrAssistantRunNotFound
	}
	if assistantRunTerminal(active.run.Status) {
		s.mu.Unlock()
		return nil
	}
	if !immediate && !active.lastText.IsZero() && time.Since(active.lastText) < projectAssistantTextSnapshotInterval {
		mutate(active)
		if active.textFlush == nil {
			active.textFlush = time.AfterFunc(projectAssistantTextSnapshotInterval-time.Since(active.lastText), a.flushText)
		}
		s.mu.Unlock()
		return nil
	}
	if active.textFlush != nil {
		active.textFlush.Stop()
		active.textFlush = nil
	}
	mutate(active)
	active.run.Revision++
	active.run.UpdatedAt = time.Now().UTC()
	active.message.UpdatedAt = active.run.UpdatedAt
	if !immediate {
		active.lastText = active.run.UpdatedAt
	}
	run, message, scope := active.run, active.message, active.scope
	s.mu.Unlock()
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), projectMessagePersistTimeout)
	err := s.store.SaveAssistantRunSnapshot(persistCtx, scope, run, []store.Message{message}, run.Revision-1)
	cancel()
	if err != nil {
		s.recordPersistenceFailure(a.key, a.runID, err)
		return fmt.Errorf("persist assistant snapshot: %w", err)
	}
	s.mu.Lock()
	if current := s.runs[a.key]; current != nil && current.run.ID == a.runID && current.run.Revision == run.Revision {
		current.committedRun, current.committedMessage = run, message
		for _, subscriber := range current.subscribers {
			s.sendCoalesced(subscriber, projectAssistantRunSnapshot{Run: run, Message: message})
		}
	}
	s.mu.Unlock()
	return nil
}

func (a *projectAssistantSnapshotAccumulator) flushText() {
	if a == nil || a.supervisor == nil {
		return
	}
	a.supervisor.mu.Lock()
	active := a.supervisor.runs[a.key]
	if active == nil || active.run.ID != a.runID {
		a.supervisor.mu.Unlock()
		return
	}
	content := active.message.Content
	active.textFlush = nil
	a.supervisor.mu.Unlock()
	_ = a.UpdateText(context.Background(), content, true)
}

// recordPersistenceFailure makes a best effort to leave an explicit terminal
// state. The failing save may be transient (for example a dropped database
// connection); a second detached save is therefore useful, but never permits
// orchestration to continue as though a snapshot had been durable.
func (s *projectAssistantSupervisor) recordPersistenceFailure(key projectAssistantRunKey, runID string, _ error) {
	s.mu.Lock()
	active := s.runs[key]
	if active == nil || active.run.ID != runID {
		s.mu.Unlock()
		return
	}
	active.run, active.message = active.committedRun, active.committedMessage
	active.run.Status = store.AssistantRunStatusFailed
	active.run.Revision++
	active.run.UpdatedAt = time.Now().UTC()
	active.message.UpdatedAt = active.run.UpdatedAt
	run, message, scope := active.run, active.message, active.scope
	active.cancel(errors.New("assistant snapshot persistence failed"))
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), projectMessagePersistTimeout)
	defer cancel()
	if s.store.SaveAssistantRunSnapshot(ctx, scope, run, []store.Message{message}, run.Revision-1) != nil {
		return
	}
	s.mu.Lock()
	if current := s.runs[key]; current != nil && current.run.ID == runID && current.run.Revision == run.Revision {
		current.committedRun, current.committedMessage = run, message
		for _, subscriber := range current.subscribers {
			s.sendCoalesced(subscriber, projectAssistantRunSnapshot{Run: run, Message: message})
		}
	}
	s.mu.Unlock()
}
