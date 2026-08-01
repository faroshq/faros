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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/faroshq/provider-app-studio/store"
)

const (
	projectAssistantRunToolCallEventType   = "tool_call"
	projectAssistantRunToolResultEventType = "tool_result"
	projectAssistantRunEventPageSize       = 500
)

var (
	errProjectAssistantRunToolCallIDConflict = errors.New("assistant run tool call id conflicts with durable input")
	errProjectAssistantRunIncompleteEffect   = errors.New("assistant run contains an incomplete effectful tool call")
	errProjectAssistantRunIncompleteNonRead  = errors.New("assistant run contains an incomplete non-read tool call")
	errProjectAssistantRunToolLedgerCorrupt  = errors.New("assistant run tool ledger is corrupt")
)

// projectAssistantRunEventLedger makes the append-only AssistantRunEvent log
// the durable idempotency boundary for v2 tool dispatch. BeginToolCall must
// complete before a backend is invoked; FinishToolCall is called only after the
// invocation has produced the exact result or error that the model will see.
//
// The mutex intentionally covers both state reconstruction and each CAS append.
// A single run-scoped ledger can therefore admit concurrent Eino tool calls
// without racing its expected sequence. CAS conflicts caused by another server
// process are resolved by refreshing the durable log and re-evaluating the call.
type projectAssistantRunEventLedger struct {
	store store.Store
	scope store.Scope
	runID string

	mu           sync.Mutex
	loaded       bool
	lastSequence int64
	calls        map[string]*projectAssistantRunToolCallState
}

type projectAssistantRunToolCallState struct {
	ToolName   string
	ArgsDigest string
	Read       bool
	Effect     bool
	Attempts   int
	Outcome    *projectAssistantRunToolCallOutcome
}

// projectAssistantRunToolCallToken binds a post-dispatch result to the exact
// durable call event that authorized the dispatch.
type projectAssistantRunToolCallToken struct {
	CallID     string
	ToolName   string
	ArgsDigest string
	Read       bool
	Effect     bool
}

// projectAssistantRunToolCallDecision tells the caller either to dispatch with
// Token or to return Replay without touching the tool backend.
type projectAssistantRunToolCallDecision struct {
	Token  projectAssistantRunToolCallToken
	Replay *projectAssistantRunToolCallOutcome
}

func (d projectAssistantRunToolCallDecision) ShouldDispatch() bool {
	return d.Replay == nil
}

// projectAssistantRunToolCallOutcome preserves the exact model-visible values.
// Failed distinguishes a successful empty result from an error with empty text.
type projectAssistantRunToolCallOutcome struct {
	Result string
	Error  string
	Failed bool
}

func (o projectAssistantRunToolCallOutcome) InvokeResult() (string, error) {
	if !o.Failed {
		return o.Result, nil
	}
	return o.Result, errors.New(o.Error)
}

type projectAssistantRunToolCallPayload struct {
	Arguments json.RawMessage `json:"arguments"`
	Read      bool            `json:"read"`
	Effect    bool            `json:"effect"`
	Attempt   int             `json:"attempt"`
}

type projectAssistantRunToolResultPayload struct {
	Result string `json:"result"`
	Error  string `json:"error"`
	Failed bool   `json:"failed"`
}

func newProjectAssistantRunEventLedger(
	messageStore store.Store,
	scope store.Scope,
	runID string,
) *projectAssistantRunEventLedger {
	return &projectAssistantRunEventLedger{
		store: messageStore,
		scope: scope,
		runID: strings.TrimSpace(runID),
		calls: map[string]*projectAssistantRunToolCallState{},
	}
}

// BeginToolCall durably records a call before dispatch. A completed exact call
// is replayed. Reusing an ID for different input is rejected. An interrupted
// read may be retried because it has no side effect; an interrupted effect is
// failed closed because dispatch may already have happened.
func (l *projectAssistantRunEventLedger) BeginToolCall(
	ctx context.Context,
	callID string,
	spec projectAssistantToolSpec,
	args map[string]any,
) (projectAssistantRunToolCallDecision, error) {
	callID = strings.TrimSpace(callID)
	toolName := projectAssistantToolKey(spec.Name)
	if l == nil || l.store == nil || strings.TrimSpace(l.runID) == "" {
		return projectAssistantRunToolCallDecision{}, fmt.Errorf("assistant run tool ledger is not configured")
	}
	if callID == "" || toolName == "" {
		return projectAssistantRunToolCallDecision{}, fmt.Errorf("assistant run tool call id and name are required")
	}
	canonicalArgs, digest, err := projectAssistantRunToolCallDigest(toolName, args)
	if err != nil {
		return projectAssistantRunToolCallDecision{}, err
	}
	token := projectAssistantRunToolCallToken{
		CallID:     callID,
		ToolName:   toolName,
		ArgsDigest: digest,
		Read:       spec.Risk == projectAssistantToolRiskRead,
		Effect:     projectAssistantToolHasEffect(spec),
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	for {
		if err := l.refreshLocked(ctx); err != nil {
			return projectAssistantRunToolCallDecision{}, err
		}
		state := l.calls[callID]
		if state != nil {
			if state.ToolName != token.ToolName || state.ArgsDigest != token.ArgsDigest {
				return projectAssistantRunToolCallDecision{}, fmt.Errorf(
					"%w: call %q was already recorded as %s (%s)",
					errProjectAssistantRunToolCallIDConflict,
					callID,
					state.ToolName,
					state.ArgsDigest,
				)
			}
			if state.Read != token.Read || state.Effect != token.Effect {
				return projectAssistantRunToolCallDecision{}, fmt.Errorf(
					"%w: call %q changed risk classification",
					errProjectAssistantRunToolCallIDConflict,
					callID,
				)
			}
			if state.Outcome != nil {
				outcome := *state.Outcome
				return projectAssistantRunToolCallDecision{Replay: &outcome}, nil
			}
			if state.Effect {
				return projectAssistantRunToolCallDecision{}, fmt.Errorf(
					"%w: %s call %q may already have been dispatched",
					errProjectAssistantRunIncompleteEffect,
					state.ToolName,
					callID,
				)
			}
			if !state.Read {
				return projectAssistantRunToolCallDecision{}, fmt.Errorf(
					"%w: %s call %q has no durable result",
					errProjectAssistantRunIncompleteNonRead,
					state.ToolName,
					callID,
				)
			}
		}

		attempt := 1
		if state != nil {
			attempt = state.Attempts + 1
		}
		payload, err := json.Marshal(projectAssistantRunToolCallPayload{
			Arguments: canonicalArgs,
			Read:      token.Read,
			Effect:    token.Effect,
			Attempt:   attempt,
		})
		if err != nil {
			return projectAssistantRunToolCallDecision{}, fmt.Errorf("encode assistant run tool call event: %w", err)
		}
		event := store.AssistantRunEvent{
			RunID:      l.runID,
			Type:       projectAssistantRunToolCallEventType,
			CallID:     callID,
			ToolName:   toolName,
			ArgsDigest: digest,
			Payload:    payload,
		}
		saved, err := l.store.AppendAssistantRunEvent(ctx, l.scope, event, l.lastSequence)
		if errors.Is(err, store.ErrAssistantRunEventConflict) {
			// Another process won the sequence. Refresh and make the durable
			// state decide whether this call is now a replay or a conflict.
			continue
		}
		if err != nil {
			return projectAssistantRunToolCallDecision{}, fmt.Errorf("append assistant run tool call event: %w", err)
		}
		if err := l.applyEventLocked(saved); err != nil {
			return projectAssistantRunToolCallDecision{}, err
		}
		return projectAssistantRunToolCallDecision{Token: token}, nil
	}
}

// FinishToolCall durably records the exact model-visible outcome after
// dispatch. The first completion is authoritative if retryable reads overlap.
func (l *projectAssistantRunEventLedger) FinishToolCall(
	ctx context.Context,
	token projectAssistantRunToolCallToken,
	result string,
	invokeErr error,
) (projectAssistantRunToolCallOutcome, error) {
	outcome := projectAssistantRunToolCallOutcome{Result: result}
	if invokeErr != nil {
		outcome.Error = invokeErr.Error()
		outcome.Failed = true
	}
	if l == nil || l.store == nil || strings.TrimSpace(l.runID) == "" {
		return projectAssistantRunToolCallOutcome{}, fmt.Errorf("assistant run tool ledger is not configured")
	}
	persistCtx, cancelPersist := detachedProjectPersistenceContext(ctx)
	defer cancelPersist()

	l.mu.Lock()
	defer l.mu.Unlock()
	for {
		if err := l.refreshLocked(persistCtx); err != nil {
			return projectAssistantRunToolCallOutcome{}, err
		}
		state := l.calls[strings.TrimSpace(token.CallID)]
		if state == nil {
			return projectAssistantRunToolCallOutcome{}, fmt.Errorf(
				"%w: result for call %q has no preceding call event",
				errProjectAssistantRunToolLedgerCorrupt,
				token.CallID,
			)
		}
		if state.ToolName != token.ToolName || state.ArgsDigest != token.ArgsDigest ||
			state.Read != token.Read || state.Effect != token.Effect {
			return projectAssistantRunToolCallOutcome{}, fmt.Errorf(
				"%w: result token for call %q does not match durable input",
				errProjectAssistantRunToolCallIDConflict,
				token.CallID,
			)
		}
		if state.Outcome != nil {
			return *state.Outcome, nil
		}
		payload, err := json.Marshal(projectAssistantRunToolResultPayload{
			Result: outcome.Result,
			Error:  outcome.Error,
			Failed: outcome.Failed,
		})
		if err != nil {
			return projectAssistantRunToolCallOutcome{}, fmt.Errorf("encode assistant run tool result event: %w", err)
		}
		event := store.AssistantRunEvent{
			RunID:      l.runID,
			Type:       projectAssistantRunToolResultEventType,
			CallID:     token.CallID,
			ToolName:   token.ToolName,
			ArgsDigest: token.ArgsDigest,
			Payload:    payload,
		}
		saved, err := l.store.AppendAssistantRunEvent(persistCtx, l.scope, event, l.lastSequence)
		if errors.Is(err, store.ErrAssistantRunEventConflict) {
			continue
		}
		if err != nil {
			return projectAssistantRunToolCallOutcome{}, fmt.Errorf("append assistant run tool result event: %w", err)
		}
		if err := l.applyEventLocked(saved); err != nil {
			return projectAssistantRunToolCallOutcome{}, err
		}
		return outcome, nil
	}
}

func (l *projectAssistantRunEventLedger) refreshLocked(ctx context.Context) error {
	if !l.loaded {
		l.calls = map[string]*projectAssistantRunToolCallState{}
		l.lastSequence = 0
		l.loaded = true
	}
	for {
		events, err := l.store.ListAssistantRunEvents(
			ctx,
			l.scope,
			l.runID,
			l.lastSequence,
			projectAssistantRunEventPageSize,
		)
		if err != nil {
			return fmt.Errorf("load assistant run tool ledger: %w", err)
		}
		if len(events) == 0 {
			return nil
		}
		for _, event := range events {
			if event.Sequence != l.lastSequence+1 {
				return fmt.Errorf(
					"%w: event sequence advanced from %d to %d",
					errProjectAssistantRunToolLedgerCorrupt,
					l.lastSequence,
					event.Sequence,
				)
			}
			if err := l.applyEventLocked(event); err != nil {
				return err
			}
		}
		if len(events) < projectAssistantRunEventPageSize {
			return nil
		}
	}
}

func (l *projectAssistantRunEventLedger) applyEventLocked(event store.AssistantRunEvent) error {
	if event.Sequence != l.lastSequence+1 {
		return fmt.Errorf(
			"%w: event sequence advanced from %d to %d",
			errProjectAssistantRunToolLedgerCorrupt,
			l.lastSequence,
			event.Sequence,
		)
	}
	if event.Type != projectAssistantRunToolCallEventType && event.Type != projectAssistantRunToolResultEventType {
		l.lastSequence = event.Sequence
		return nil
	}
	callID := strings.TrimSpace(event.CallID)
	toolName := projectAssistantToolKey(event.ToolName)
	digest := strings.TrimSpace(event.ArgsDigest)
	if callID == "" || toolName == "" || digest == "" {
		return fmt.Errorf("%w: sequence %d is missing tool identity", errProjectAssistantRunToolLedgerCorrupt, event.Sequence)
	}
	state := l.calls[callID]
	switch event.Type {
	case projectAssistantRunToolCallEventType:
		var payload projectAssistantRunToolCallPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil || !json.Valid(payload.Arguments) || payload.Attempt < 1 {
			return fmt.Errorf("%w: invalid tool call payload at sequence %d", errProjectAssistantRunToolLedgerCorrupt, event.Sequence)
		}
		_, persistedDigest, err := projectAssistantRunToolCallDigest(toolName, payload.Arguments)
		if err != nil || persistedDigest != digest {
			return fmt.Errorf("%w: tool call digest mismatch at sequence %d", errProjectAssistantRunToolLedgerCorrupt, event.Sequence)
		}
		if state == nil {
			l.calls[callID] = &projectAssistantRunToolCallState{
				ToolName:   toolName,
				ArgsDigest: digest,
				Read:       payload.Read,
				Effect:     payload.Effect,
				Attempts:   payload.Attempt,
			}
		} else if state.ToolName != toolName || state.ArgsDigest != digest || state.Read != payload.Read || state.Effect != payload.Effect {
			return fmt.Errorf("%w: call %q has conflicting durable inputs", errProjectAssistantRunToolLedgerCorrupt, callID)
		} else if state.Outcome != nil || !state.Read || payload.Attempt != state.Attempts+1 {
			return fmt.Errorf("%w: call %q has an invalid retry event", errProjectAssistantRunToolLedgerCorrupt, callID)
		} else {
			state.Attempts = payload.Attempt
		}
	case projectAssistantRunToolResultEventType:
		if state == nil {
			return fmt.Errorf("%w: result for call %q precedes its call event", errProjectAssistantRunToolLedgerCorrupt, callID)
		}
		if state.ToolName != toolName || state.ArgsDigest != digest {
			return fmt.Errorf("%w: result for call %q does not match durable input", errProjectAssistantRunToolLedgerCorrupt, callID)
		}
		var payload projectAssistantRunToolResultPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Errorf("%w: invalid tool result payload at sequence %d", errProjectAssistantRunToolLedgerCorrupt, event.Sequence)
		}
		outcome := projectAssistantRunToolCallOutcome{
			Result: payload.Result,
			Error:  payload.Error,
			Failed: payload.Failed,
		}
		if state.Outcome != nil && *state.Outcome != outcome {
			return fmt.Errorf("%w: call %q has conflicting durable results", errProjectAssistantRunToolLedgerCorrupt, callID)
		}
		state.Outcome = &outcome
	}
	// Advance only after the complete event has validated and been applied.
	// A corrupt durable event must remain the next event on every refresh so
	// this ledger fails closed instead of silently skipping past it.
	l.lastSequence = event.Sequence
	return nil
}

func projectAssistantRunToolCallDigest(toolName string, args any) (json.RawMessage, string, error) {
	toolName = projectAssistantToolKey(toolName)
	if toolName == "" {
		return nil, "", fmt.Errorf("assistant run tool name is required")
	}
	canonicalArgs, err := projectAssistantCanonicalJSON(args)
	if err != nil {
		return nil, "", fmt.Errorf("canonicalize %s tool arguments: %w", toolName, err)
	}
	digest := sha256.Sum256(append(append([]byte(toolName), 0), canonicalArgs...))
	return canonicalArgs, "sha256:" + hex.EncodeToString(digest[:]), nil
}

func projectAssistantCanonicalJSON(value any) (json.RawMessage, error) {
	if raw, ok := value.(json.RawMessage); ok {
		var decoded any
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.UseNumber()
		if err := decoder.Decode(&decoded); err != nil {
			return nil, err
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			if err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("multiple JSON values are not allowed")
		}
		value = decoded
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("arguments are not valid JSON")
	}
	return json.RawMessage(raw), nil
}
