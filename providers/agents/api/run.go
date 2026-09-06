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
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	agentsv1alpha1 "github.com/faroshq/provider-agents/apis/v1alpha1"
	agentsclient "github.com/faroshq/provider-agents/client"
	"github.com/faroshq/provider-agents/engine"
	"github.com/faroshq/provider-agents/llm"
	"github.com/faroshq/provider-agents/store"
	"github.com/faroshq/provider-agents/tools"
)

// ErrBudgetExceeded is returned when an agent has spent its budget for the
// current window. Interactive callers surface it; background callers suspend.
var ErrBudgetExceeded = errors.New("budget exceeded")

// budgetWindow maps an AgentBudget window to a duration (default 30d/month).
func budgetWindow(b *agentsv1alpha1.AgentBudget) time.Duration {
	if b != nil && b.Window == "day" {
		return 24 * time.Hour
	}
	return 30 * 24 * time.Hour
}

// checkBudget reports an ErrBudgetExceeded (wrapped with detail) when the
// agent's rolling-window usage has reached its token or USD cap. A nil budget,
// or zero limits, never blocks.
func (s *Server) checkBudget(ctx context.Context, scope store.Scope, agent *agentsv1alpha1.Agent, now time.Time) error {
	b := agent.Spec.Budget
	if b == nil || (b.TokenLimit == 0 && strings.TrimSpace(b.USDLimit) == "") {
		return nil
	}
	u, err := s.store.GetUsage(ctx, scope, agent.Name, now, budgetWindow(b))
	if err != nil {
		return nil // fail open on usage-read errors; don't wedge the agent
	}
	if b.TokenLimit > 0 && u.InputTokens+u.OutputTokens >= b.TokenLimit {
		return fmt.Errorf("%w: %d/%d tokens used this %s", ErrBudgetExceeded, u.InputTokens+u.OutputTokens, b.TokenLimit, budgetName(b))
	}
	if usd := strings.TrimSpace(b.USDLimit); usd != "" {
		if lim, perr := strconv.ParseFloat(usd, 64); perr == nil && lim > 0 {
			spent := float64(u.USDMicros) / 1e6
			if spent >= lim {
				return fmt.Errorf("%w: $%.2f/$%.2f used this %s", ErrBudgetExceeded, spent, lim, budgetName(b))
			}
		}
	}
	return nil
}

func budgetName(b *agentsv1alpha1.AgentBudget) string {
	if b != nil && b.Window == "day" {
		return "day"
	}
	return "month"
}

// runResult is the outcome of a non-streaming agent execution. A non-nil
// Pending means the run paused on an approval gate rather than finishing.
type runResult struct {
	RunID   string       `json:"runID"`
	Content string       `json:"content"`
	Pending *pendingInfo `json:"pending,omitempty"`
	Usage   struct {
		InputTokens  int64 `json:"inputTokens"`
		OutputTokens int64 `json:"outputTokens"`
		USDMicros    int64 `json:"usdMicros"`
	} `json:"usage"`
}

// pendingInfo describes the approval gate a paused run is waiting on.
type pendingInfo struct {
	InboxID string `json:"inboxID"`
	Tool    string `json:"tool"`
	Args    string `json:"args"`
}

// runCheckpoint is the payload persisted in store.Run.Checkpoint: the engine's
// loop state plus what the api layer needs to rebuild the toolset on resume.
type runCheckpoint struct {
	Engine        engine.Checkpoint `json:"engine"`
	Tool          string            `json:"tool"`
	Args          string            `json:"args"`
	InboxID       string            `json:"inboxID"`
	SourceName    string            `json:"sourceName,omitempty"`
	NotifyChannel string            `json:"notifyChannel,omitempty"`
}

// taskRun bundles everything one agent execution needs. Creds reads the model
// credential (per-request: tenant client acting as the user; background: the
// virtual-workspace getter). ParentRunID links sub-agent (delegation) runs.
// Edges* configure the optional hub-MCP edges family (interactive runs only —
// it authenticates as the calling user).
type taskRun struct {
	Creds llm.SecretGetter
	CR    tools.CRAccess
	Scope store.Scope
	Agent *agentsv1alpha1.Agent

	// RunID pre-assigns the run's ID (async run-now, chat's SSE start event,
	// resume). Empty → generated. A caller that persisted a Pending intent must
	// set RunIDPersisted; streaming chat supplies an ID to its start event before
	// executeTask creates the Running row.
	RunID string
	// RunIDPersisted distinguishes a durable queue handoff from a caller-owned
	// stable ID. Keeping this explicit prevents the streaming HTTP path from
	// treating its pre-announcement ID as an already-existing store row.
	RunIDPersisted bool
	// ExecutionOwner/ExecutionEpoch fence durable writes for this execution. A
	// scheduler intent starts Pending and receives these values when it is
	// claimed; a recovered run receives them from ClaimStaleRun.
	ExecutionOwner string
	ExecutionEpoch int64

	SessionID   string
	Task        string
	Trigger     string
	SourceName  string // schedule/trigger/connection that fired this run
	ParentRunID string

	// IdempotencyKey is the caller's de-duplication token for an API-invoked run.
	// Carried here because executeTask rewrites the run record from scratch and
	// would otherwise drop what the pre-write stored.
	IdempotencyKey string

	// NotifyChannel is the agent-channel role a paused/resumed background run
	// delivers to (recorded in the checkpoint for resume delivery).
	NotifyChannel string
	// ReplyTarget pins the exact chat a channel conversation came from, so the
	// answer goes back where the question was asked.
	ReplyTarget string
	// DeliveryKind marks a channel conversation ("channel") so a run that dies
	// can be reported in the chat rather than to the agent's notify channel.
	DeliveryKind string

	// Callback, when set, is POSTed the run's outcome once it finishes. Delivery,
	// not execution — consumed by the detached-start helpers. See api/callback.go.
	Callback *runCallback

	// Worker, when non-nil, marks this run as a spawned sub-agent worker and
	// carries the constraints its parent imposed (depth, narrowed families,
	// approval class, tool-turn budget). See api/spawn.go.
	Worker *workerRun

	// ApproveTool/ApproveArgs pre-authorize exactly one tool call — the resume
	// path sets them after the user approved the gated call.
	ApproveTool string
	ApproveArgs string
	approveUsed *bool

	EdgesEndpoint string // hub mcpserver MCP URL ("" → edges family absent)
	// HubToken is the CALLER's bearer token for the hub. It authenticates the
	// edges MCP dial AND the infrastructure data plane, so anything reaching a
	// tenant workload through the platform needs it. Empty on background runs,
	// which have no user to act as.
	HubToken      string
	EdgesInsecure bool

	// ClusterID is the tenant workspace's kcp logical-cluster ID, used with the
	// caller's token to address instance-backed tools over the infrastructure
	// provider's data plane.
	ClusterID string

	OnDelta     func(string)
	OnToolStart func(id, name, args string)
	OnTool      func(engine.ToolEvent)
}

// delivery describes where this run's answer should go, for the run record. A
// worker or a delegated child reports to its parent in memory and has no channel
// of its own, so it records none.
func (r taskRun) delivery() *store.RunDelivery {
	if r.Worker != nil || r.Trigger == agentsv1alpha1.RunTriggerDelegation {
		return nil
	}
	if r.SourceName == "" && r.ReplyTarget == "" && r.NotifyChannel == "" {
		return nil
	}
	return &store.RunDelivery{
		SourceName: r.SourceName, ReplyTarget: r.ReplyTarget,
		NotifyChannel: r.NotifyChannel, Kind: r.DeliveryKind,
	}
}

// prepareTaskRun establishes durable ownership before any model or tool work
// starts. Background jobs already have a Pending intent and must atomically
// claim it; direct runs create their Running row with the first fence epoch.
func (s *Server) prepareTaskRun(ctx context.Context, run taskRun, now time.Time) (taskRun, store.Run, error) {
	now = now.UTC()
	preassigned := run.RunID != "" && run.RunIDPersisted
	if run.RunID == "" {
		run.RunID = uuid.NewString()
	}
	if run.ExecutionOwner == "" {
		run.ExecutionOwner = uuid.NewString()
	}
	if preassigned {
		stored, err := s.store.GetRun(ctx, run.Scope, run.RunID)
		if err != nil {
			return run, store.Run{}, fmt.Errorf("loading pending run %s: %w", run.RunID, err)
		}
		if stored.Phase != store.RunPhasePending {
			return run, store.Run{}, fmt.Errorf("%w: run %s is %s", store.ErrRunAlreadyClaimed, run.RunID, stored.Phase)
		}
		claimed, err := s.store.ClaimRun(ctx, run.Scope, run.RunID, run.ExecutionOwner, now)
		if err != nil {
			return run, store.Run{}, err
		}
		run.ExecutionOwner, run.ExecutionEpoch = claimed.ExecutionOwner, claimed.ExecutionEpoch
		// The intent owns the stable identity. Refresh mutable delivery/input fields
		// from the job before entering the model loop, while the claim fence is held.
		claimed.SessionID, claimed.Trigger, claimed.Input = run.SessionID, run.Trigger, run.Task
		claimed.Delivery = run.delivery()
		claimed.UpdatedAt = now
		lease := now.Add(store.RunLeaseDuration)
		claimed.LeaseUntil = &lease
		if err := s.store.SaveRunOwned(ctx, run.Scope, claimed, run.ExecutionOwner, run.ExecutionEpoch); err != nil {
			return run, store.Run{}, fmt.Errorf("persisting claimed run %s: %w", run.RunID, err)
		}
		return run, claimed, nil
	}

	lease := now.Add(store.RunLeaseDuration)
	record := store.Run{
		ID: run.RunID, AgentName: run.Agent.Name, SessionID: run.SessionID, Trigger: run.Trigger,
		ParentRunID: run.ParentRunID, IdempotencyKey: run.IdempotencyKey, Delivery: run.delivery(),
		Phase: store.RunPhaseRunning, Input: run.Task, CreatedAt: now, UpdatedAt: now, StartedAt: &now,
		ExecutionOwner: run.ExecutionOwner, ExecutionEpoch: 1, LeaseUntil: &lease,
	}
	if err := s.store.SaveRun(ctx, run.Scope, record); err != nil {
		return run, store.Run{}, fmt.Errorf("creating run %s: %w", run.RunID, err)
	}
	run.ExecutionEpoch = record.ExecutionEpoch
	return run, record, nil
}

// executeTask runs one agent turn against a task prompt, persisting the
// transcript (including tool steps) and run record. It is the shared execution
// path for chat, run-now, background fires, channel messages, and delegation.
// A gated tool call pauses the run (phase PendingApproval, checkpoint saved)
// instead of finishing; resolveApproval resumes it.
func (s *Server) executeTask(ctx context.Context, run taskRun) (runResult, error) {
	scope, agent := run.Scope, run.Agent
	now := time.Now().UTC()
	var record store.Run
	var err error
	run, record, err = s.prepareTaskRun(ctx, run, now)
	if err != nil {
		return runResult{RunID: run.RunID}, err
	}
	runID := record.ID
	// Establish the run timeout, live registry entry, and durable lease before
	// model/tool setup. Credential or tool discovery can block for a while; a
	// lease that starts only once the engine is entered could expire and let a
	// recovery worker steal work that is still being prepared.
	timeout := time.Hour
	if v := agent.Spec.Limits.TimeoutSeconds; v > 0 {
		timeout = time.Duration(v) * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	s.liveRuns.register(runID, cancel)
	defer s.liveRuns.unregister(runID)
	stopLease := s.startRunLease(ctx, run, cancel)
	defer stopLease()

	finishFailure := func(err error) (runResult, error) {
		if !s.finishRunOwned(ctx, scope, runID, run.ExecutionOwner, run.ExecutionEpoch,
			runOutcome{Phase: store.RunPhaseFailed, Message: err.Error()}, time.Now().UTC()) {
			return runResult{RunID: runID}, runOutcomeNotCommitted(runID, err)
		}
		return runResult{RunID: runID}, err
	}
	if err := s.checkBudget(ctx, scope, agent, now); err != nil {
		return finishFailure(err)
	}

	// Workers resolve the "background" model purpose (falling back to chat), so a
	// fan-out of ten sub-tasks can run on a cheap model while the parent — which
	// does the synthesis — keeps the strong one.
	purpose := llm.PurposeChat
	if run.Worker != nil {
		purpose = llm.PurposeBackground
	}
	model, err := s.buildModelForPurpose(ctx, run.Creds, agent, purpose)
	if err != nil {
		return finishFailure(err)
	}
	sessionID := run.SessionID
	if sessionID == "" {
		sessionID = run.Trigger // e.g. schedules share a per-trigger session
	}

	// Assemble the agent's tools for this trigger class (policy + approvals +
	// audit + delegation); MCP sessions are released when the run ends.
	toolset, mcpInstructions, closeTools := s.buildToolset(ctx, tools.Deps{
		Store: s.store, Scope: scope, Agent: agent, CR: run.CR,
		Secrets: run.Creds, ConnSecretName: connectionSecretName,
		RunID:     runID,
		DataPlane: s.dataPlaneFor(run),
	}, run)
	defer closeTools()

	maxIters := 16
	if v := int(agent.Spec.Limits.MaxToolTurns); v > 0 {
		maxIters = min(v, 32)
	}
	// A worker works one sub-task on a budget its parent chose, which is shorter
	// than the agent's own per-run allowance.
	if run.Worker != nil {
		maxIters = run.Worker.MaxToolTurns
	}

	// Fold the session's older messages into a summary when replaying them all
	// would crowd the model's window. Must happen BEFORE the turn is assembled —
	// the point is to be under the limit when the request goes out.
	modelName := s.modelNameForPurpose(ctx, run.Creds, agent, purpose)
	s.maybeCompactSession(ctx, run, sessionID, modelName)

	// Assemble the turn before persisting the task message — LoadRecentMessages
	// has no notion of "current run", so appending first would replay the task
	// into history and the model would see it twice.
	// Derived from the toolset that was actually built, not from the grant: a
	// depth-limited worker has no spawn tool and must not be told to fan out.
	msgs := s.assembleTurnCtx(ctx, run, sessionID, mcpInstructions, hasToolNamed(toolset, "spawn"))

	_ = s.store.AppendMessage(ctx, scope, store.Message{
		ID: uuid.NewString(), AgentName: agent.Name, SessionID: sessionID, RunID: runID,
		Role: "user", Content: run.Task, CreatedAt: now,
	})
	s.publishRunEvent(scope, runEvent{ID: runID, Agent: agent.Name, Trigger: run.Trigger, ParentRunID: run.ParentRunID, Phase: store.RunPhaseRunning})

	cb := s.runCallbacks(ctx, run, sessionID)
	// Periodic checkpoints make a long run recoverable: if this replica dies, the
	// sweep (api/sweep.go) resumes from the last one instead of losing the work.
	cb.OnCheckpoint = s.checkpointRecorder(ctx, run, sessionID)
	res, err := s.engine.StreamTurnWithTools(ctx, model, msgs, toolset, engine.TurnConfig{
		MaxIters:            maxIters,
		ContextBudgetTokens: turnContextBudget(modelName),
		CheckpointEvery:     checkpointEveryIterations,
	}, cb)
	// An engine is allowed to finish a response after cancellation has raced its
	// final read. The cancellation is still the caller's decision, so do not let
	// a late nil error publish a successful result after the run was stopped.
	if err == nil {
		err = ctx.Err()
	}
	end := time.Now().UTC()
	if err != nil {
		phase := store.RunPhaseFailed
		// A registry cancel (or run timeout) surfaces as a context error —
		// record it as Aborted, not Failed.
		if ctx.Err() != nil {
			phase = store.RunPhaseAborted
		}
		if !s.finishRunOwned(ctx, scope, runID, run.ExecutionOwner, run.ExecutionEpoch, runOutcome{Phase: phase, Message: err.Error()}, end) {
			return runResult{RunID: runID}, runOutcomeNotCommitted(runID, err)
		}
		s.publishRunEvent(scope, runEvent{ID: runID, Agent: agent.Name, Trigger: run.Trigger, ParentRunID: run.ParentRunID, Phase: phase})
		return runResult{RunID: runID}, err
	}

	// Estimate cost from the catalog so budgets enforce dollars (not just
	// tokens) and the Models dashboard can show spend. Cost is attributed to the
	// model this run actually used — a worker on the cheap background model must
	// not be billed at the chat model's rate; unknown models cost 0 rather than a
	// fabricated number.
	costMicros := llm.CostMicros(modelName, res.Usage.InputTokens, res.Usage.OutputTokens)
	_, _ = s.store.AddUsage(ctx, scope, agent.Name, res.Usage.InputTokens, res.Usage.OutputTokens, costMicros, end, 30*24*time.Hour)

	// Paused on an approval gate: persist the checkpoint and stop here — the
	// approval resolution resumes the run in place.
	if res.Interrupt != nil {
		ck := runCheckpoint{
			Engine: res.Interrupt.Checkpoint, Tool: res.Interrupt.Tool, Args: res.Interrupt.Args,
			InboxID: res.Interrupt.RequestID, SourceName: run.SourceName, NotifyChannel: run.NotifyChannel,
		}
		ckJSON, _ := json.Marshal(ck)
		stored, gerr := s.store.GetRun(ctx, scope, runID)
		if gerr != nil {
			return runResult{RunID: runID}, runOutcomeNotCommitted(runID, gerr)
		}
		stored.Phase = store.RunPhasePendingApproval
		stored.Checkpoint = ckJSON
		stored.InputTokens = res.Usage.InputTokens
		stored.OutputTokens = res.Usage.OutputTokens
		stored.USDMicros = costMicros
		stored.UpdatedAt = end
		if err := s.store.SaveRunOwned(ctx, scope, stored, run.ExecutionOwner, run.ExecutionEpoch); err != nil {
			return runResult{RunID: runID}, runOutcomeNotCommitted(runID, err)
		}
		s.publishRunEvent(scope, runEvent{ID: runID, Agent: agent.Name, Trigger: run.Trigger, ParentRunID: run.ParentRunID, Phase: store.RunPhasePendingApproval})
		out := runResult{RunID: runID, Content: res.Content,
			Pending: &pendingInfo{InboxID: res.Interrupt.RequestID, Tool: res.Interrupt.Tool, Args: res.Interrupt.Args}}
		out.Usage.InputTokens = res.Usage.InputTokens
		out.Usage.OutputTokens = res.Usage.OutputTokens
		out.Usage.USDMicros = costMicros
		return out, nil
	}

	// The answer goes on the run record too, so a programmatic reader (the parent
	// of a spawned worker, GET /api/runs/{id}) finds the result where it found the
	// phase instead of having to locate the session and dig out its last message.
	body, sources := splitSources(res.Content)
	fin := runOutcome{
		Phase: store.RunPhaseSucceeded, Usage: res.Usage, CostMicros: costMicros,
		Output: body, Sources: sources,
	}
	if !s.finishRunOwned(ctx, scope, runID, run.ExecutionOwner, run.ExecutionEpoch, fin, end) {
		// Do not append a transcript answer or publish/deliver a success after a
		// recovery claim superseded this executor's fence. The durable owner is the
		// only authority allowed to announce the terminal result.
		return runResult{RunID: runID}, runOutcomeNotCommitted(runID, nil)
	}
	_ = s.store.AppendMessage(ctx, scope, store.Message{
		ID: uuid.NewString(), AgentName: agent.Name, SessionID: sessionID, RunID: runID,
		Role: "assistant", Content: res.Content, CreatedAt: end,
	})
	s.publishRunEvent(scope, runEvent{ID: runID, Agent: agent.Name, Trigger: run.Trigger, ParentRunID: run.ParentRunID, Phase: store.RunPhaseSucceeded})

	out := runResult{RunID: runID, Content: res.Content}
	out.Usage.InputTokens = res.Usage.InputTokens
	out.Usage.OutputTokens = res.Usage.OutputTokens
	out.Usage.USDMicros = costMicros
	return out, nil
}

// startDetachedRun pre-creates a Pending run record and executes the task in a
// detached goroutine as the calling user, so run-now endpoints return 202 with
// a runID immediately instead of blocking the HTTP request on the whole agent
// loop. Output is delivered to the run's notify channel when it finishes, like
// a real background fire.
func (s *Server) startDetachedRun(r *http.Request, c *agentsclient.Client, id identity, agent *agentsv1alpha1.Agent, tr taskRun) (string, error) {
	runID := uuid.NewString()
	now := time.Now().UTC()
	scope := id.scope(agent.Name)
	tr.RunID = runID
	tr.RunIDPersisted = true
	tr.Creds = c
	tr.CR = clientCR{c}
	tr.Scope = scope
	tr.Agent = agent
	tr.EdgesEndpoint = s.edgesEndpoint(id.clusterID)
	tr.HubToken = id.token
	tr.EdgesInsecure = s.cfg.HubInsecure
	tr.ClusterID = id.clusterID

	// Detach from the request context: the response returns immediately while
	// the run continues (executeTask applies the agent's own timeout).
	ctx := context.WithoutCancel(r.Context())
	if err := s.store.SaveRun(ctx, scope, store.Run{
		ID: runID, AgentName: agent.Name, SessionID: tr.SessionID, Trigger: tr.Trigger,
		IdempotencyKey: tr.IdempotencyKey,
		Phase:          store.RunPhasePending, Input: tr.Task, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		return "", fmt.Errorf("persisting run intent: %w", err)
	}
	go func() {
		res, err := s.executeTask(ctx, tr)
		if err != nil || res.Pending != nil {
			if errors.Is(err, store.ErrRunLeaseLost) {
				// A recovery/cancellation fence superseded this executor. Its
				// in-memory result is not an outcome for the current owner, so it
				// must not trigger a callback that re-reads and reports this run.
				return
			}
			// The outcome is on the run record (approvals resume separately), but a
			// caller that asked to be told must hear about a failure or a pause too —
			// otherwise it waits forever for a callback that never comes.
			s.deliverRunCallback(ctx, scope, runID, tr.Callback)
			return
		}
		// An API-invoked run answers its caller, which polls, waits, or named a
		// callback. Pushing to the agent's channel as well would message the user
		// about work they did not ask to hear about; run-now for a schedule/trigger
		// is the opposite — its whole point is the channel delivery.
		if tr.Trigger == agentsv1alpha1.RunTriggerAPI {
			s.deliverRunCallback(ctx, scope, runID, tr.Callback)
			return
		}
		s.deliverToNotifyChannel(ctx, c, agent, tr.NotifyChannel, tr.SourceName, res.Content)
	}()
	return runID, nil
}

// dataPlaneFor describes how instance-backed tools reach tenant workloads for
// this run. Background runs carry no user token, so the result is unusable by
// design — the tool reports that precisely rather than failing at the hub.
func (s *Server) dataPlaneFor(run taskRun) tools.DataPlane {
	return tools.DataPlane{
		HubBase:   s.cfg.HubURL,
		ClusterID: run.ClusterID,
		Token:     run.HubToken,
		Insecure:  s.cfg.HubInsecure,
	}
}

// runCallbacks chains the caller's streaming callbacks with tool-step
// persistence: every completed tool call lands in the session transcript so
// follow-up turns (and the transcript view) can see what the run actually did.
func (s *Server) runCallbacks(ctx context.Context, run taskRun, sessionID string) engine.Callbacks {
	return engine.Callbacks{
		OnDelta:     run.OnDelta,
		OnToolStart: run.OnToolStart,
		OnTool: func(ev engine.ToolEvent) {
			_ = s.store.AppendMessage(ctx, run.Scope, store.Message{
				ID: uuid.NewString(), AgentName: run.Agent.Name, SessionID: sessionID, RunID: run.RunID,
				Role: "tool", Content: safeTruncate(ev.Result, 8*1024),
				Metadata: map[string]any{
					"tool": ev.Name, "args": redactArgs(ev.Args),
					"error": ev.Err, "durationMS": ev.Duration.Milliseconds(),
				},
				CreatedAt: time.Now().UTC(),
			})
			if run.OnTool != nil {
				run.OnTool(ev)
			}
		},
	}
}

// runOutcome is what a run ended with. Message carries the failure reason (or
// "" on success); Output/Sources carry the answer, so a caller reading the run
// record gets the result and not just the phase.
type runOutcome struct {
	Phase      store.RunPhase
	Message    string
	Usage      engine.Usage
	CostMicros int64
	Output     string
	Sources    []string
}

// finishRun stamps a run's terminal phase, result, usage, and timestamps, and
// clears any checkpoint.
func (s *Server) finishRun(ctx context.Context, scope store.Scope, runID string, out runOutcome, end time.Time) bool {
	writeCtx, cancel := runWriteContext(ctx)
	defer cancel()
	stored, err := s.store.GetRun(writeCtx, scope, runID)
	if err != nil {
		return false
	}
	applyRunOutcome(&stored, out, end)
	if stored.ExecutionOwner != "" && stored.ExecutionEpoch > 0 {
		return s.store.SaveRunOwned(writeCtx, scope, stored, stored.ExecutionOwner, stored.ExecutionEpoch) == nil
	}
	return s.store.SaveRun(writeCtx, scope, stored) == nil
}

// finishRunOwned stamps a terminal result only while the supplied execution
// fence still owns the run. A previous owner that returns after recovery gets a
// fenced no-op instead of replacing the recovered result.
func (s *Server) finishRunOwned(ctx context.Context, scope store.Scope, runID, owner string, epoch int64, out runOutcome, end time.Time) bool {
	if owner == "" || epoch <= 0 {
		return s.finishRun(ctx, scope, runID, out, end)
	}
	writeCtx, cancel := runWriteContext(ctx)
	defer cancel()
	stored, err := s.store.GetRun(writeCtx, scope, runID)
	if err != nil || stored.ExecutionOwner != owner || stored.ExecutionEpoch != epoch {
		if err != nil {
			log.Printf("run: loading %s to finish: %v", runID, err)
		}
		return false
	}
	applyRunOutcome(&stored, out, end)
	if err := s.store.SaveRunOwned(writeCtx, scope, stored, owner, epoch); err != nil {
		log.Printf("run: finishing %s lost ownership: %v", runID, err)
		return false
	}
	return true
}

const runPersistTimeout = 10 * time.Second

// runWriteContext keeps terminal bookkeeping alive after a model request was
// canceled or the HTTP caller disconnected. Without detaching here, the
// cancellation that stops execution also cancels the only write that records
// Aborted/Failed, leaving a run indistinguishable from a crashed owner.
func runWriteContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), runPersistTimeout)
}

// runOutcomeNotCommitted tells callers that the model result is not theirs to
// publish. A recovery claim, an explicit cancellation, or a transient store
// failure can invalidate the execution fence between model completion and the
// terminal write. Treating that as an ordinary model error would emit a stale
// notification and cause a queue worker to record a false failure.
func runOutcomeNotCommitted(runID string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: terminal outcome for run %s was not persisted", store.ErrRunLeaseLost, runID)
	}
	return fmt.Errorf("%w: terminal outcome for run %s was not persisted: %v", store.ErrRunLeaseLost, runID, cause)
}

func applyRunOutcome(stored *store.Run, out runOutcome, end time.Time) {
	stored.Phase = out.Phase
	stored.Message = out.Message
	stored.Checkpoint = nil
	if out.Output != "" {
		stored.Output = safeTruncate(out.Output, maxStoredOutput)
	}
	if len(out.Sources) > 0 {
		stored.Sources = out.Sources
	}
	if out.Usage.InputTokens > 0 || out.Usage.OutputTokens > 0 {
		stored.InputTokens = out.Usage.InputTokens
		stored.OutputTokens = out.Usage.OutputTokens
	}
	if out.CostMicros > 0 {
		stored.USDMicros = out.CostMicros
	}
	stored.UpdatedAt = end
	stored.FinishedAt = &end
}

// runEvent is one run lifecycle change pushed to /api/events subscribers. A
// struct rather than positional arguments: id, agent, trigger and parentRunID
// are all strings, and transposing two of them would be invisible at the call
// site and wrong on the wire.
type runEvent struct {
	ID      string
	Agent   string
	Trigger string
	// ParentRunID lets a client watching a run recognize a child it has not seen
	// before. Without it the run-detail view could only refresh for children it
	// already knew about, so a worker spawned after the page opened stayed
	// invisible until a manual reload.
	ParentRunID string
	Phase       store.RunPhase
}

// publishRunEvent pushes a run lifecycle change to /api/events subscribers.
func (s *Server) publishRunEvent(scope store.Scope, ev runEvent) {
	payload := map[string]any{
		"id": ev.ID, "agent": ev.Agent, "trigger": ev.Trigger, "phase": string(ev.Phase),
	}
	if ev.ParentRunID != "" {
		payload["parentRunID"] = ev.ParentRunID
	}
	s.events.publish(store.Scope{OrgUUID: scope.OrgUUID, WorkspaceUUID: scope.WorkspaceUUID}, "run", payload)
}

// memoryNoteClip bounds one injected note's body, and memoryNoteLimit how many
// notes are injected (spec.memory.maxNotes, capped). Shared with the compaction
// estimator so it measures the same injection this assembles.
const memoryNoteClip = 500

func memoryNoteLimit(agent *agentsv1alpha1.Agent) int {
	if v := int(agent.Spec.Memory.MaxNotes); v > 0 {
		return min(v, 100)
	}
	return 20
}

// assembleTurnCtx builds the message list (system prompt + recent history +
// task) using a context rather than an *http.Request, so background callers
// (scheduler) can reuse it.
// hasToolNamed reports whether the assembled toolset contains a tool.
func hasToolNamed(ts []engine.Tool, name string) bool {
	for _, t := range ts {
		if t.Name == name {
			return true
		}
	}
	return false
}

func (s *Server) assembleTurnCtx(ctx context.Context, run taskRun, sessionID, mcpInstructions string, fanOut bool) []engine.Message {
	scope, agent, task, trigger := run.Scope, run.Agent, run.Task, run.Trigger
	var msgs []engine.Message
	// A worker gets a fixed sub-agent preamble above the agent's persona: it is
	// the same agent, but answering one scoped question as data rather than
	// holding a conversation.
	if run.Worker != nil {
		msgs = append(msgs, engine.Message{Role: engine.RoleSystem,
			Content: workerPreamble(agent.Name, run.Worker.ParentTask)})
	}
	if sp := agent.Spec.SystemPrompt; sp != "" {
		msgs = append(msgs, engine.Message{Role: engine.RoleSystem, Content: sp})
	}
	if fanOut && run.Worker == nil {
		msgs = append(msgs, engine.Message{Role: engine.RoleSystem, Content: fanOutGuidance})
	}
	if run.Worker != nil {
		if instr := strings.TrimSpace(run.Worker.Instructions); instr != "" {
			msgs = append(msgs, engine.Message{Role: engine.RoleSystem, Content: "Guidance for this sub-task:\n\n" + instr})
		}
		// A worker runs on a fresh session with no history, and gets no memory
		// injection: the parent owns recall and synthesis, and ten workers each
		// carrying the agent's whole note pile is cost without benefit.
		if mi := strings.TrimSpace(mcpInstructions); mi != "" {
			msgs = append(msgs, engine.Message{Role: engine.RoleSystem, Content: "Guidance from connected tools/services:\n\n" + mi})
		}
		return append(msgs, engine.Message{Role: engine.RoleUser, Content: task})
	}
	// Long-term memory: auto-inject the agent's saved notes so recall does not
	// depend on the model remembering to call memory_list. spec.memory.enabled
	// defaults to true; maxNotes bounds the injection.
	if agent.Spec.Memory.Enabled == nil || *agent.Spec.Memory.Enabled {
		if notes, err := s.store.ListMemories(ctx, scope, memoryNoteLimit(agent)); err == nil && len(notes) > 0 {
			var b strings.Builder
			b.WriteString("Long-term notes you saved earlier (use memory_save to add or update):\n")
			for _, n := range notes {
				fmt.Fprintf(&b, "- %s: %s\n", n.Title, safeTruncate(n.Body, memoryNoteClip))
			}
			msgs = append(msgs, engine.Message{Role: engine.RoleSystem, Content: b.String()})
		}
	}
	// Ambient guidance from connected MCP servers (e.g. an edges Service's
	// spec.instructions describing its entity layout / quirks). Injected as a
	// system message so it reaches the model even though MCP clients don't
	// surface server `initialize` instructions on their own.
	if mi := strings.TrimSpace(mcpInstructions); mi != "" {
		msgs = append(msgs, engine.Message{Role: engine.RoleSystem, Content: "Guidance from connected tools/services:\n\n" + mi})
	}
	// History, with any compaction summary standing in for the older messages it
	// covers (see api/compact.go). Without compaction this is just the last-N
	// window it always was.
	sc := s.loadSessionContext(ctx, scope, sessionID, chatHistoryLimit)
	if sc.Summary != nil {
		msgs = append(msgs, summaryMessage(*sc.Summary))
	}
	history := sc.Messages
	for _, m := range history {
		switch m.Role {
		case "assistant":
			msgs = append(msgs, engine.Message{Role: engine.RoleAssistant, Content: m.Content})
		case "tool":
			// Replay persisted tool steps as compact context so a follow-up turn
			// can see what earlier turns actually did.
			toolName, _ := m.Metadata["tool"].(string)
			args, _ := m.Metadata["args"].(string)
			msgs = append(msgs, engine.Message{Role: engine.RoleUser, Content: fmt.Sprintf(
				"[record of an earlier tool call]\ntool: %s\nargs: %s\nresult: %s",
				toolName, safeTruncate(args, 500), safeTruncate(m.Content, 1500))})
		default:
			msgs = append(msgs, engine.Message{Role: engine.RoleUser, Content: m.Content})
		}
	}
	// Background sessions (schedule/heartbeat/wakeup) accumulate the agent's
	// own prior replies turn after turn — kept deliberately, so e.g. a news
	// schedule can see what it already posted and not repeat itself. That pile
	// can outweigh the one persona line at the top, so re-assert it after
	// history to keep the agent in character.
	if sp := agent.Spec.SystemPrompt; sp != "" && !isInteractive(trigger) && (len(history) > 0 || sc.Summary != nil) {
		msgs = append(msgs, engine.Message{Role: engine.RoleSystem, Content: "Reminder — your persona and standing instructions still apply to this reply:\n\n" + sp})
	}
	msgs = append(msgs, engine.Message{Role: engine.RoleUser, Content: task})
	return msgs
}
