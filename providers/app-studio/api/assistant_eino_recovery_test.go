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
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
)

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
