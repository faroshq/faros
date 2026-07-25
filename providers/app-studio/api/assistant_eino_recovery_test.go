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
	"errors"
	"io"
	"strings"
	"testing"

	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"google.golang.org/genai"
)

func TestProjectEinoAssistantShouldRetryModelError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"openai 429", &openaimodel.APIError{HTTPStatusCode: 429}, true},
		{"openai 503", &openaimodel.APIError{HTTPStatusCode: 503}, true},
		{"openai 400", &openaimodel.APIError{HTTPStatusCode: 400}, false},
		{"openai 401", &openaimodel.APIError{HTTPStatusCode: 401}, false},
		{"gemini 429", genai.APIError{Code: 429}, true},
		{"gemini 503", genai.APIError{Code: 503}, true},
		{"gemini 403", genai.APIError{Code: 403}, false},
		{"unexpected eof", io.ErrUnexpectedEOF, true},
		{"generic", errors.New("provider failed"), false},
		{"canceled", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := projectEinoAssistantShouldRetryModelError(tt.err); got != tt.want {
				t.Fatalf("projectEinoAssistantShouldRetryModelError(%v) = %t, want %t", tt.err, got, tt.want)
			}
		})
	}
}

func TestProjectEinoAssistantModelRetryConfig(t *testing.T) {
	config := projectEinoAssistantModelRetryConfig()
	if config.MaxRetries != 2 {
		t.Fatalf("MaxRetries = %d, want 2", config.MaxRetries)
	}

	retry := config.ShouldRetry(context.Background(), &adk.RetryContext{Err: io.ErrUnexpectedEOF})
	if !retry.Retry {
		t.Fatalf("retryable decision = %#v, want retry", retry)
	}
	if retry.RejectReason != "transient model provider failure" {
		t.Fatalf("RejectReason = %#v, want transient model provider failure", retry.RejectReason)
	}

	permanent := config.ShouldRetry(context.Background(), &adk.RetryContext{Err: errors.New("provider failed")})
	if permanent.Retry {
		t.Fatalf("permanent decision = %#v, want no retry", permanent)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled := config.ShouldRetry(ctx, &adk.RetryContext{Err: io.ErrUnexpectedEOF})
	if canceled.Retry {
		t.Fatalf("canceled decision = %#v, want no retry", canceled)
	}
}

func TestProjectEinoAssistantPatchToolCallsMarksCompletionUnknown(t *testing.T) {
	middleware, err := projectEinoAssistantPatchToolCallsMiddleware(context.Background())
	if err != nil {
		t.Fatalf("create recovery middleware: %v", err)
	}
	_, state, err := middleware.BeforeModelRewriteState(
		context.Background(),
		&adk.ChatModelAgentState{Messages: []adk.Message{
			schema.AssistantMessage("", []schema.ToolCall{{
				ID:   "call-write-file",
				Type: "function",
				Function: schema.FunctionCall{
					Name:      projectToolWriteFile,
					Arguments: `{"path":"src/App.tsx","content":"updated"}`,
				},
			}}),
		}},
		&adk.ModelContext{},
	)
	if err != nil {
		t.Fatalf("rewrite dangling tool call: %v", err)
	}
	if len(state.Messages) != 2 {
		t.Fatalf("messages = %#v, want assistant call and patched tool result", state.Messages)
	}
	patched := state.Messages[1]
	if patched.Role != schema.Tool || patched.ToolCallID != "call-write-file" {
		t.Fatalf("patched message = %#v, want tool result for dangling call", patched)
	}
	if !strings.Contains(patched.Content, "completion is unknown") || !strings.Contains(patched.Content, "inspect current") {
		t.Fatalf("patched content = %q, want completion uncertainty and inspection guidance", patched.Content)
	}
}
