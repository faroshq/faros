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
	"encoding/json"
	"testing"
	"time"

	"github.com/faroshq/provider-app-studio/store"
)

func TestAssistantThreadAgentMessageCarriesWorkedDuration(t *testing.T) {
	progress := projectAssistantProgressSnapshot{
		Version:          1,
		Messages:         []string{},
		MessageSequences: []int{},
		WorkedDurationMS: 83_400,
	}
	item := assistantThreadItemWithMessagePresentation(assistantThreadItem{
		ID:     "assistant-1",
		Type:   assistantThreadEventAssistantMessage,
		Status: "completed",
	}, map[string]any{projectAssistantMetadataProgress: progress})

	var data map[string]any
	if err := json.Unmarshal(item.Data, &data); err != nil {
		t.Fatal(err)
	}
	got, ok := projectAssistantProgressSnapshotFromMetadata(data[projectAssistantMetadataProgress])
	if !ok {
		t.Fatalf("agent message data = %#v, want assistant progress", data)
	}
	if got.WorkedDurationMS != progress.WorkedDurationMS {
		t.Fatalf("worked duration = %d, want %d", got.WorkedDurationMS, progress.WorkedDurationMS)
	}
}

func TestAttachAssistantThreadMessagePresentationRepairsHistoricalItems(t *testing.T) {
	memoryStore := store.NewMemoryStore()
	server := NewWithWorkspace(nil, memoryStore, nil, "", false)
	scope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo", ProjectUID: "project-uid"}
	now := time.Now().UTC()
	progress := projectAssistantProgressSnapshot{
		Version:          1,
		Messages:         []string{},
		MessageSequences: []int{},
		WorkedDurationMS: 146_045,
	}
	if err := memoryStore.AppendMessage(context.Background(), scope, store.Message{
		ID:        "assistant-historical",
		Role:      "assistant",
		Metadata:  map[string]any{projectAssistantMetadataProgress: progress},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	items, err := server.attachAssistantThreadMessagePresentation(context.Background(), scope, []assistantThreadItem{{
		ID:        "assistant-historical",
		TurnID:    "run-1",
		Type:      assistantThreadEventAssistantMessage,
		Status:    "completed",
		CreatedAt: now,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	var data map[string]any
	if err := json.Unmarshal(items[0].Data, &data); err != nil {
		t.Fatal(err)
	}
	got, ok := projectAssistantProgressSnapshotFromMetadata(data[projectAssistantMetadataProgress])
	if !ok || got.WorkedDurationMS != progress.WorkedDurationMS {
		t.Fatalf("historical progress = %#v, want duration %d", data, progress.WorkedDurationMS)
	}
}
