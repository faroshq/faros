/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

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

	"github.com/cloudwego/eino/adk"
	einotool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

const (
	projectEinoAssistantToolBatchMaxCalls         = 12
	projectEinoAssistantToolBatchMaxReads         = 8
	projectEinoAssistantToolBatchMaxPrimary       = 1
	projectEinoAssistantToolBatchMaxParallelReads = 4
	projectEinoAssistantToolBatchCorrectionMarker = "[App Studio tool-batch correction]"
)

var errProjectAssistantInvalidToolBatch = errors.New("invalid assistant tool batch")

type projectEinoAssistantInvalidToolBatchError struct {
	Code   string
	Reason string
}

func (e *projectEinoAssistantInvalidToolBatchError) Error() string {
	if e == nil {
		return errProjectAssistantInvalidToolBatch.Error()
	}
	return fmt.Sprintf("%s (%s): %s", errProjectAssistantInvalidToolBatch, e.Code, e.Reason)
}

func (e *projectEinoAssistantInvalidToolBatchError) Unwrap() error {
	return errProjectAssistantInvalidToolBatch
}

type projectEinoAssistantToolBatchMiddleware struct {
	*adk.BaseChatModelAgentMiddleware

	runState *projectEinoAssistantRunState
	readGate chan struct{}
	effectMu sync.RWMutex
}

func projectEinoAssistantToolBatchAdmissionMiddleware(
	runState *projectEinoAssistantRunState,
) adk.ChatModelAgentMiddleware {
	return &projectEinoAssistantToolBatchMiddleware{
		BaseChatModelAgentMiddleware: &adk.BaseChatModelAgentMiddleware{},
		runState:                     runState,
		readGate:                     make(chan struct{}, projectEinoAssistantToolBatchMaxParallelReads),
	}
}

// AfterModelRewriteState is the last admission boundary before Eino hands an
// assistant response to its tools node. The retry policy has already rejected
// structurally invalid batches by this point; this hook canonicalizes the
// accepted calls, collapses duplicate reads, and assigns stable call IDs.
func (m *projectEinoAssistantToolBatchMiddleware) AfterModelRewriteState(
	ctx context.Context,
	state *adk.ChatModelAgentState,
	_ *adk.ModelContext,
) (context.Context, *adk.ChatModelAgentState, error) {
	if state == nil || len(state.Messages) == 0 {
		return ctx, state, nil
	}
	response := state.Messages[len(state.Messages)-1]
	if response == nil || response.Role != schema.Assistant || len(response.ToolCalls) == 0 {
		return ctx, state, nil
	}

	rawCalls := append([]schema.ToolCall(nil), response.ToolCalls...)
	modelCallOrdinal := m.runState.CurrentModelCallOrdinal()
	if modelCallOrdinal == 0 {
		// Lifecycle normally advances the durable model-call ordinal before
		// admission. Keep the admission boundary safe when it is exercised in
		// isolation or by a future middleware ordering change.
		modelCallOrdinal = m.runState.NextModelCallOrdinal()
	}
	normalized, err := projectEinoAssistantNormalizeToolBatch(rawCalls, modelCallOrdinal)
	if err != nil {
		// This should only be reachable when another middleware rewrites the
		// response after retry admission. Never dispatch an unvalidated batch.
		m.runState.discardLatestModelToolBatch(response.ToolCalls)
		return ctx, nil, err
	}
	response.ToolCalls = normalized
	m.runState.reconcileLatestModelToolBatch(rawCalls, normalized, false)
	return ctx, state, nil
}

// WrapInvokableToolCall supplies the execution half of admission. Eino may run
// an admitted read-only batch concurrently, but no more than four reads reach
// backends at once. Primary actions take the exclusive side of the same gate.
func (m *projectEinoAssistantToolBatchMiddleware) WrapInvokableToolCall(
	_ context.Context,
	endpoint adk.InvokableToolCallEndpoint,
	toolCtx *adk.ToolContext,
) (adk.InvokableToolCallEndpoint, error) {
	name := ""
	if toolCtx != nil {
		name = toolCtx.Name
	}
	return func(ctx context.Context, argumentsInJSON string, opts ...einotool.Option) (string, error) {
		release, err := m.acquire(ctx, name)
		if err != nil {
			return "", err
		}
		defer release()
		return endpoint(ctx, argumentsInJSON, opts...)
	}, nil
}

func (m *projectEinoAssistantToolBatchMiddleware) WrapEnhancedInvokableToolCall(
	_ context.Context,
	endpoint adk.EnhancedInvokableToolCallEndpoint,
	toolCtx *adk.ToolContext,
) (adk.EnhancedInvokableToolCallEndpoint, error) {
	name := ""
	if toolCtx != nil {
		name = toolCtx.Name
	}
	return func(ctx context.Context, argument *schema.ToolArgument, opts ...einotool.Option) (*schema.ToolResult, error) {
		release, err := m.acquire(ctx, name)
		if err != nil {
			return nil, err
		}
		defer release()
		return endpoint(ctx, argument, opts...)
	}, nil
}

func (m *projectEinoAssistantToolBatchMiddleware) acquire(
	ctx context.Context,
	name string,
) (func(), error) {
	if !projectEinoAssistantToolBatchRead(name) {
		m.effectMu.Lock()
		return m.effectMu.Unlock, nil
	}
	select {
	case m.readGate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	m.effectMu.RLock()
	return func() {
		m.effectMu.RUnlock()
		<-m.readGate
	}, nil
}

func projectEinoAssistantNormalizeToolBatch(
	calls []schema.ToolCall,
	modelCallOrdinal int,
) ([]schema.ToolCall, error) {
	normalized, err := projectEinoAssistantAnalyzeToolBatch(calls)
	if err != nil {
		return nil, err
	}
	usedIDs := make(map[string]struct{}, len(normalized))
	for index := range normalized {
		call := &normalized[index]
		if call.ID == "" {
			call.ID = projectEinoAssistantDeterministicToolCallID(modelCallOrdinal, index, *call)
		}
		if _, exists := usedIDs[call.ID]; exists {
			return nil, &projectEinoAssistantInvalidToolBatchError{
				Code:   "conflicting_tool_call_id",
				Reason: fmt.Sprintf("admitted call %d reuses another admitted call ID", index+1),
			}
		}
		usedIDs[call.ID] = struct{}{}
		position := index
		call.Index = &position
	}
	return normalized, nil
}

func projectEinoAssistantAnalyzeToolBatch(calls []schema.ToolCall) ([]schema.ToolCall, error) {
	if len(calls) == 0 {
		return nil, nil
	}

	normalized := make([]schema.ToolCall, 0, min(len(calls), projectEinoAssistantToolBatchMaxCalls))
	callBySignature := make(map[string]int, len(calls))
	signatureByID := make(map[string]string, len(calls))
	readCount := 0
	primaryCount := 0

	for index := range calls {
		call, signature, isRead, err := projectEinoAssistantCanonicalToolCall(calls[index], index)
		if err != nil {
			return nil, err
		}
		if call.ID != "" {
			if previous, exists := signatureByID[call.ID]; exists && previous != signature {
				return nil, &projectEinoAssistantInvalidToolBatchError{
					Code:   "conflicting_tool_call_id",
					Reason: fmt.Sprintf("call %d reuses an existing call ID for a different operation", index+1),
				}
			}
			signatureByID[call.ID] = signature
		}
		if admittedIndex, duplicate := callBySignature[signature]; duplicate {
			if !isRead {
				return nil, &projectEinoAssistantInvalidToolBatchError{
					Code:   "duplicate_primary_action",
					Reason: fmt.Sprintf("call %d repeats an effectful or interactive action", index+1),
				}
			}
			if normalized[admittedIndex].ID == "" && call.ID != "" {
				normalized[admittedIndex].ID = call.ID
			}
			continue
		}
		callBySignature[signature] = len(normalized)
		normalized = append(normalized, call)
		if isRead {
			readCount++
		} else {
			primaryCount++
		}
	}

	switch {
	case len(normalized) > projectEinoAssistantToolBatchMaxCalls:
		return nil, &projectEinoAssistantInvalidToolBatchError{
			Code:   "too_many_calls",
			Reason: fmt.Sprintf("the response contains %d distinct calls; the maximum is %d", len(normalized), projectEinoAssistantToolBatchMaxCalls),
		}
	case readCount > projectEinoAssistantToolBatchMaxReads:
		return nil, &projectEinoAssistantInvalidToolBatchError{
			Code:   "too_many_reads",
			Reason: fmt.Sprintf("the response contains %d distinct evidence reads; the maximum is %d", readCount, projectEinoAssistantToolBatchMaxReads),
		}
	case primaryCount > projectEinoAssistantToolBatchMaxPrimary:
		return nil, &projectEinoAssistantInvalidToolBatchError{
			Code:   "too_many_primary_actions",
			Reason: "the response contains more than one effectful, interactive, or progress action",
		}
	case readCount > 0 && primaryCount > 0:
		return nil, &projectEinoAssistantInvalidToolBatchError{
			Code:   "mixed_reads_and_primary_action",
			Reason: "evidence reads and a primary action must be returned in separate model responses",
		}
	}
	return normalized, nil
}

func projectEinoAssistantCanonicalToolCall(
	raw schema.ToolCall,
	index int,
) (schema.ToolCall, string, bool, error) {
	call := raw
	call.ID = strings.TrimSpace(call.ID)
	call.Type = strings.TrimSpace(call.Type)
	if call.Type == "" {
		call.Type = "function"
	}
	if call.Type != "function" {
		return schema.ToolCall{}, "", false, &projectEinoAssistantInvalidToolBatchError{
			Code:   "unsupported_tool_call_type",
			Reason: fmt.Sprintf("call %d is not a function call", index+1),
		}
	}
	call.Function.Name = strings.TrimSpace(call.Function.Name)
	if call.Function.Name == "" {
		return schema.ToolCall{}, "", false, &projectEinoAssistantInvalidToolBatchError{
			Code:   "missing_tool_name",
			Reason: fmt.Sprintf("call %d has no tool name", index+1),
		}
	}
	arguments, err := projectEinoAssistantCanonicalToolArguments(call.Function.Arguments)
	if err != nil {
		return schema.ToolCall{}, "", false, &projectEinoAssistantInvalidToolBatchError{
			Code:   "invalid_tool_arguments",
			Reason: fmt.Sprintf("call %d does not contain one JSON object for its arguments", index+1),
		}
	}
	call.Function.Arguments = arguments
	sum := sha256.Sum256([]byte(call.Function.Name + "\x00" + arguments))
	signature := hex.EncodeToString(sum[:])
	return call, signature, projectEinoAssistantToolBatchRead(call.Function.Name), nil
}

func projectEinoAssistantCanonicalToolArguments(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "{}"
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var arguments map[string]any
	if err := decoder.Decode(&arguments); err != nil || arguments == nil {
		return "", errors.New("tool arguments must be a JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("tool arguments contain trailing JSON")
	}
	canonical, err := json.Marshal(arguments)
	if err != nil {
		return "", err
	}
	return string(canonical), nil
}

func projectEinoAssistantDeterministicToolCallID(
	modelCallOrdinal int,
	index int,
	call schema.ToolCall,
) string {
	return projectEinoAssistantSyntheticToolCallID(
		modelCallOrdinal,
		index,
		call.Function.Name,
		call.Function.Arguments,
	)
}

func projectEinoAssistantSyntheticToolCallID(
	modelCallOrdinal int,
	index int,
	name string,
	arguments string,
) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf(
		"%d\x00%d\x00%s\x00%s",
		modelCallOrdinal,
		index,
		strings.TrimSpace(name),
		arguments,
	)))
	return "call_appstudio_" + hex.EncodeToString(sum[:12])
}

func projectEinoAssistantToolBatchRead(name string) bool {
	rawName := strings.TrimSpace(name)
	baseName := projectToolBaseName(rawName)
	if projectEinoAssistantFilesystemReadTool(baseName) || baseName == projectEinoAssistantToolSearchTool {
		return true
	}
	if spec, ok := projectAssistantWorkflowToolSpec(baseName); ok {
		return spec.Risk == projectAssistantToolRiskRead
	}
	registry := projectAssistantLocalToolRegistry(nil)
	if spec, ok := registry.Spec(rawName); ok {
		return spec.Risk == projectAssistantToolRiskRead
	}
	if spec, ok := registry.Spec(baseName); ok {
		return spec.Risk == projectAssistantToolRiskRead
	}
	if spec, ok := projectAssistantMCPToolSpec(projectMCPTool{Name: rawName}); ok {
		return spec.Risk == projectAssistantToolRiskRead
	}
	// This optional local tool is absent from the nil-server registry used for
	// classification, but its contract is always read-only when registered.
	return baseName == projectToolGetPreviewConsoleLogs
}

func projectEinoAssistantToolBatchCorrection(
	input []*schema.Message,
	batchErr error,
) []*schema.Message {
	messages := append([]*schema.Message(nil), input...)
	reason := "the response did not satisfy the App Studio action-batch contract"
	var admissionErr *projectEinoAssistantInvalidToolBatchError
	if errors.As(batchErr, &admissionErr) {
		reason = admissionErr.Code + ": " + admissionErr.Reason
	}
	messages = append(messages, schema.SystemMessage(
		projectEinoAssistantToolBatchCorrectionMarker+" The previous response was rejected before any tool executed ("+reason+"). Return exactly one corrected response: either up to eight distinct independent evidence reads, or one primary action. Do not mix reads with an action, repeat effectful calls, or reuse a call ID for different arguments.",
	))
	return messages
}

func projectEinoAssistantHasToolBatchCorrection(messages []*schema.Message) bool {
	for _, message := range messages {
		if message != nil && strings.Contains(message.Content, projectEinoAssistantToolBatchCorrectionMarker) {
			return true
		}
	}
	return false
}

// The model callback runs inside Eino's retry wrapper and therefore observes a
// rejected response before ShouldRetry makes its decision. Keep the durable
// run-state projection aligned with Eino's accepted state by removing rejected
// batches and replacing accepted batches with their normalized form.
func (s *projectEinoAssistantRunState) discardLatestModelToolBatch(raw []schema.ToolCall) {
	s.reconcileLatestModelToolBatch(raw, nil, true)
}

func (s *projectEinoAssistantRunState) reconcileLatestModelToolBatch(
	expected []schema.ToolCall,
	replacement []schema.ToolCall,
	discard bool,
) {
	if s == nil || len(expected) == 0 {
		return
	}
	expectedCalls := projectEinoToolCallsToChat(expected)
	replacementCalls := projectEinoToolCallsToChat(replacement)
	s.mu.Lock()
	defer s.mu.Unlock()
	for messageIndex := len(s.messages) - 1; messageIndex >= 0; messageIndex-- {
		message := &s.messages[messageIndex]
		if message.Role != "assistant" || !projectEinoAssistantChatToolBatchesMatch(message.ToolCalls, expectedCalls) {
			continue
		}
		for _, call := range message.ToolCalls {
			signature := projectEinoAssistantToolCallSignature(call.Function.Name, call.Function.Arguments)
			if s.seenToolCalls[signature] <= 1 {
				delete(s.seenToolCalls, signature)
			} else {
				s.seenToolCalls[signature]--
			}
		}
		if discard {
			s.messages = append(s.messages[:messageIndex], s.messages[messageIndex+1:]...)
			s.toolCalls = nil
			if s.turn > 0 {
				s.turn--
			}
			return
		}
		message.ToolCalls = cloneProjectAssistantToolCalls(replacementCalls)
		s.toolCalls = cloneProjectAssistantToolCalls(replacementCalls)
		for _, call := range replacementCalls {
			signature := projectEinoAssistantToolCallSignature(call.Function.Name, call.Function.Arguments)
			s.seenToolCalls[signature]++
		}
		return
	}
}

func projectEinoAssistantChatToolBatchesMatch(left, right []chatToolCall) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if strings.TrimSpace(left[index].Function.Name) != strings.TrimSpace(right[index].Function.Name) ||
			left[index].Function.Arguments != right[index].Function.Arguments {
			return false
		}
	}
	return true
}
