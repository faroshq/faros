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

package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMemoryStoreAssistantRunEventsAreAppendOnlyAndScoped(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	scope := Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo", ProjectUID: "project-a"}
	otherScope := Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo", ProjectUID: "project-b"}
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	for _, candidate := range []Scope{scope, otherScope} {
		if err := s.SaveAssistantRun(ctx, candidate, AssistantRun{ID: "run-1", Mode: AssistantRunModeDefault, Status: AssistantRunStatusRunning, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveAssistantRun(%s): %v", candidate.ProjectUID, err)
		}
	}

	payload := json.RawMessage(`{"path":"src/App.vue"}`)
	first, err := s.AppendAssistantRunEvent(ctx, scope, AssistantRunEvent{
		RunID:      "run-1",
		Type:       "tool_call",
		CallID:     "call-1",
		ToolName:   "read_file",
		ArgsDigest: "sha256:first",
		Payload:    payload,
		CreatedAt:  now,
	}, 0)
	if err != nil {
		t.Fatalf("AppendAssistantRunEvent first: %v", err)
	}
	if first.Sequence != 1 || first.ProjectUID != scope.ProjectUID || !first.CreatedAt.Equal(now) {
		t.Fatalf("first event = %#v, want scoped sequence 1", first)
	}
	payload[2] = 'X'
	first.Payload[2] = 'Y'

	second, err := s.AppendAssistantRunEvent(ctx, scope, AssistantRunEvent{
		RunID:     "run-1",
		Sequence:  2,
		Type:      "tool_result",
		CallID:    "call-1",
		ToolName:  "read_file",
		Payload:   json.RawMessage(`{"bytes":42}`),
		CreatedAt: now.Add(time.Second),
	}, 1)
	if err != nil {
		t.Fatalf("AppendAssistantRunEvent second: %v", err)
	}
	if second.Sequence != 2 {
		t.Fatalf("second sequence = %d, want 2", second.Sequence)
	}

	if _, err := s.AppendAssistantRunEvent(ctx, scope, AssistantRunEvent{RunID: "run-1", Type: "duplicate"}, 1); !errors.Is(err, ErrAssistantRunEventConflict) {
		t.Fatalf("duplicate expected sequence error = %v, want ErrAssistantRunEventConflict", err)
	}
	if _, err := s.AppendAssistantRunEvent(ctx, scope, AssistantRunEvent{RunID: "run-1", Sequence: 4, Type: "skip"}, 2); !errors.Is(err, ErrAssistantRunEventConflict) {
		t.Fatalf("skipped event sequence error = %v, want ErrAssistantRunEventConflict", err)
	}

	page, err := s.ListAssistantRunEvents(ctx, scope, "run-1", 0, 1)
	if err != nil {
		t.Fatalf("ListAssistantRunEvents first page: %v", err)
	}
	if len(page) != 1 || page[0].Sequence != 1 || string(page[0].Payload) != `{"path":"src/App.vue"}` {
		t.Fatalf("first event page = %#v", page)
	}
	next, err := s.ListAssistantRunEvents(ctx, scope, "run-1", page[0].Sequence, 10)
	if err != nil {
		t.Fatalf("ListAssistantRunEvents next page: %v", err)
	}
	if len(next) != 1 || next[0].Sequence != 2 {
		t.Fatalf("next event page = %#v", next)
	}
	isolated, err := s.ListAssistantRunEvents(ctx, otherScope, "run-1", 0, 10)
	if err != nil {
		t.Fatalf("ListAssistantRunEvents other project: %v", err)
	}
	if len(isolated) != 0 {
		t.Fatalf("other project events = %#v, want none", isolated)
	}
	if err := s.DeleteProjectMessages(ctx, scope); err != nil {
		t.Fatalf("DeleteProjectMessages: %v", err)
	}
	if _, err := s.ListAssistantRunEvents(ctx, scope, "run-1", 0, 10); !errors.Is(err, ErrAssistantRunNotFound) {
		t.Fatalf("deleted project event listing error = %v, want ErrAssistantRunNotFound", err)
	}
	if _, err := s.GetAssistantRun(ctx, otherScope, "run-1"); err != nil {
		t.Fatalf("DeleteProjectMessages removed another project run: %v", err)
	}
}

func TestMemoryStoreAssistantRunEventCASSerializesWriters(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	scope := Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo", ProjectUID: "project-a"}
	if err := s.SaveAssistantRun(ctx, scope, AssistantRun{ID: "run-1", Mode: AssistantRunModeDefault, Status: AssistantRunStatusRunning}); err != nil {
		t.Fatalf("SaveAssistantRun: %v", err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := s.AppendAssistantRunEvent(ctx, scope, AssistantRunEvent{RunID: "run-1", Type: "race"}, 0)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)

	var successes, conflicts int
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrAssistantRunEventConflict):
			conflicts++
		default:
			t.Fatalf("concurrent append error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent appends successes=%d conflicts=%d, want 1 and 1", successes, conflicts)
	}
}

func TestMemoryStoreAssistantRunContractAndEventRetention(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	scope := Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo", ProjectUID: "project-a"}
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	run := AssistantRun{
		ID:              "run-1",
		Mode:            AssistantRunModeDefault,
		ApprovalMode:    AssistantApprovalModeAutoApprove,
		Status:          AssistantRunStatusRunning,
		ClientRequestID: "request-1",
		UserMessageID:   "user-1",
		ActiveMessageID: "assistant-1",
		Revision:        1,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	user := Message{ID: run.UserMessageID, Role: "user", ActorID: "alice", CreatedAt: now, UpdatedAt: now}
	assistant := Message{ID: run.ActiveMessageID, Role: "assistant", CreatedAt: now, UpdatedAt: now}
	created, err := s.CreateAssistantRun(ctx, scope, user, assistant, run)
	if err != nil {
		t.Fatalf("CreateAssistantRun: %v", err)
	}
	changed := created
	changed.Mode = AssistantRunModePlan
	changed.Revision++
	changed.UpdatedAt = now.Add(time.Second)
	if err := s.SaveAssistantRunSnapshot(ctx, scope, changed, nil, created.Revision); !errors.Is(err, ErrAssistantRunConflict) {
		t.Fatalf("changed run contract snapshot error = %v, want ErrAssistantRunConflict", err)
	}

	terminal := created
	terminal.Status = AssistantRunStatusCompleted
	terminal.UpdatedAt = now.Add(2 * time.Second)
	if err := s.SaveAssistantRun(ctx, scope, terminal); err != nil {
		t.Fatalf("SaveAssistantRun terminal: %v", err)
	}
	if _, err := s.AppendAssistantRunEvent(ctx, scope, AssistantRunEvent{RunID: run.ID, Type: "completed"}, 0); err != nil {
		t.Fatalf("AppendAssistantRunEvent: %v", err)
	}
	deleted, err := s.DeleteMessagesOlderThan(ctx, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("DeleteMessagesOlderThan: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("deleted = %d, want two unassigned messages plus one run", deleted)
	}
	if _, err := s.ListAssistantRunEvents(ctx, scope, run.ID, 0, 10); !errors.Is(err, ErrAssistantRunNotFound) {
		t.Fatalf("events after retention error = %v, want ErrAssistantRunNotFound", err)
	}
}

func TestEncryptedStoreEncryptsAssistantRunEventPayload(t *testing.T) {
	ctx := context.Background()
	base := NewMemoryStore()
	rawKey := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	keys, err := ParseEncryptionKeys("primary:" + rawKey)
	if err != nil {
		t.Fatalf("ParseEncryptionKeys: %v", err)
	}
	encrypted, err := NewEncryptedStore(base, keys)
	if err != nil {
		t.Fatalf("NewEncryptedStore: %v", err)
	}
	scope := Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo", ProjectUID: "project-a"}
	if err := encrypted.SaveAssistantRun(ctx, scope, AssistantRun{ID: "run-1", Mode: AssistantRunModeDefault, Status: AssistantRunStatusRunning}); err != nil {
		t.Fatalf("SaveAssistantRun: %v", err)
	}
	wantPayload := json.RawMessage(`{"secret":"tool-result-secret"}`)
	if _, err := encrypted.AppendAssistantRunEvent(ctx, scope, AssistantRunEvent{RunID: "run-1", Type: "tool_result", Payload: wantPayload}, 0); err != nil {
		t.Fatalf("AppendAssistantRunEvent: %v", err)
	}
	raw, err := base.ListAssistantRunEvents(ctx, scope, "run-1", 0, 10)
	if err != nil {
		t.Fatalf("raw ListAssistantRunEvents: %v", err)
	}
	if len(raw) != 1 || strings.Contains(string(raw[0].Payload), "tool-result-secret") {
		t.Fatalf("raw event payload was not encrypted: %#v", raw)
	}
	got, err := encrypted.ListAssistantRunEvents(ctx, scope, "run-1", 0, 10)
	if err != nil {
		t.Fatalf("encrypted ListAssistantRunEvents: %v", err)
	}
	if len(got) != 1 || string(got[0].Payload) != string(wantPayload) {
		t.Fatalf("decrypted events = %#v, want payload %s", got, wantPayload)
	}
}
