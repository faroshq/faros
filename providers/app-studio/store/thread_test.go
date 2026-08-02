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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestAssistantThreadTurnItemContract(t *testing.T) {
	for _, fixture := range []struct {
		name string
		new  func(*testing.T) Store
	}{
		{name: "memory", new: func(*testing.T) Store { return NewMemoryStore() }},
		{name: "encrypted", new: func(t *testing.T) Store {
			wrapped, err := NewEncryptedStore(NewMemoryStore(), testEncryptionKeys(t))
			if err != nil {
				t.Fatal(err)
			}
			return wrapped
		}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			ctx := context.Background()
			s := fixture.new(t)
			scope := Scope{OrgUUID: "org", WorkspaceUUID: "workspace", ProjectName: "demo", ProjectUID: "uid"}
			now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
			thread, err := s.CreateAssistantThread(ctx, scope, AssistantThread{ID: "thread-1", ActorID: "alice", CreatedAt: now, UpdatedAt: now}, []AssistantThreadEvent{
				{Type: "thread.created", Payload: json.RawMessage(`{"title":""}`), CreatedAt: now},
			})
			if err != nil {
				t.Fatal(err)
			}
			if thread.Status != AssistantThreadStatusIdle {
				t.Fatalf("thread status = %q", thread.Status)
			}

			turn1 := AssistantTurn{ID: "turn-1", ThreadID: thread.ID, ActorID: "alice", ClientUserMessageID: "client-1", Mode: AssistantRunModeDefault, Status: AssistantTurnStatusInProgress, CreatedAt: now, UpdatedAt: now}
			created, err := s.CreateAssistantTurn(ctx, scope, turn1, []AssistantThreadEvent{
				{Type: "turn.started", Payload: json.RawMessage(`{"turn":"turn-1"}`), CreatedAt: now},
				{Type: "item.completed", ItemID: "user-1", Payload: json.RawMessage(`{"content":"build it"}`), CreatedAt: now},
			})
			if err != nil {
				t.Fatal(err)
			}
			if created.ApprovalMode != AssistantApprovalModeOnRequest {
				t.Fatalf("approval mode = %q", created.ApprovalMode)
			}
			if replay, err := s.CreateAssistantTurn(ctx, scope, turn1, nil); err != nil || replay.ID != turn1.ID {
				t.Fatalf("idempotent turn = %#v, %v", replay, err)
			}
			if _, err := s.CreateAssistantTurn(ctx, scope, AssistantTurn{ID: "turn-conflict", ThreadID: thread.ID, ActorID: "alice", ClientUserMessageID: "client-conflict"}, nil); !errors.Is(err, ErrAssistantTurnConflict) {
				t.Fatalf("active turn conflict = %v", err)
			}

			created.Status = AssistantTurnStatusCompleted
			created.UpdatedAt = now.Add(time.Second)
			if err := s.SaveAssistantTurnWithEvent(ctx, scope, created, AssistantThreadEvent{Type: "turn.completed", Payload: json.RawMessage(`{"turn":"turn-1"}`)}, 3); err != nil {
				t.Fatal(err)
			}
			turn2, err := s.CreateAssistantTurn(ctx, scope, AssistantTurn{ID: "turn-2", ThreadID: thread.ID, ActorID: "alice", ClientUserMessageID: "client-2", CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second)}, []AssistantThreadEvent{
				{Type: "turn.started", Payload: json.RawMessage(`{"turn":"turn-2"}`)},
			})
			if err != nil {
				t.Fatal(err)
			}
			if turn2.ID != "turn-2" {
				t.Fatalf("turn 2 = %#v", turn2)
			}
			events, err := s.ListAssistantThreadEvents(ctx, scope, thread.ID, 0, 50)
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 5 {
				t.Fatalf("events = %#v", events)
			}
			for index, event := range events {
				if event.Sequence != int64(index+1) {
					t.Fatalf("event %d sequence = %d", index, event.Sequence)
				}
			}
		})
	}
}

func TestAssistantThreadListingIsActorScoped(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	scope := Scope{OrgUUID: "org", WorkspaceUUID: "workspace", ProjectName: "demo", ProjectUID: "uid"}
	for _, actor := range []string{"alice", "bob"} {
		if _, err := s.CreateAssistantThread(ctx, scope, AssistantThread{ID: "thread-" + actor, ActorID: actor}, nil); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.ListAssistantThreads(ctx, scope, "alice", false, 50, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ActorID != "alice" {
		t.Fatalf("actor-scoped threads = %#v", page.Items)
	}
}

func TestAssistantThreadLockKeyIsPostgresTextSafeAndUnambiguous(t *testing.T) {
	first := assistantThreadLockKey(
		Scope{OrgUUID: "org", WorkspaceUUID: "workspace", ProjectName: "demo", ProjectUID: "uid"},
		"thread-1",
	)
	if bytes.IndexByte([]byte(first), 0) >= 0 {
		t.Fatalf("assistant thread lock key contains a PostgreSQL-invalid NUL byte: %q", first)
	}
	second := assistantThreadLockKey(
		Scope{OrgUUID: "org1", WorkspaceUUID: "workspace", ProjectName: "demo", ProjectUID: "uid"},
		"thread-1",
	)
	third := assistantThreadLockKey(
		Scope{OrgUUID: "org", WorkspaceUUID: "1workspace", ProjectName: "demo", ProjectUID: "uid"},
		"thread-1",
	)
	if second == third {
		t.Fatalf("length-prefixed assistant thread lock keys collided: %q", second)
	}
}

func TestEncryptedAssistantThreadPayloadIsNotPlaintextAtRest(t *testing.T) {
	base := NewMemoryStore()
	wrapped, err := NewEncryptedStore(base, testEncryptionKeys(t))
	if err != nil {
		t.Fatal(err)
	}
	scope := Scope{OrgUUID: "org", WorkspaceUUID: "workspace", ProjectName: "demo", ProjectUID: "uid"}
	secret := json.RawMessage(`{"content":"secret prompt"}`)
	if _, err := wrapped.CreateAssistantThread(context.Background(), scope, AssistantThread{ID: "thread-1", ActorID: "alice"}, []AssistantThreadEvent{{Type: "thread.created", Payload: secret}}); err != nil {
		t.Fatal(err)
	}
	raw, err := base.ListAssistantThreadEvents(context.Background(), scope, "thread-1", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 1 || bytes.Contains(raw[0].Payload, []byte("secret prompt")) {
		t.Fatalf("plaintext persisted: %s", raw[0].Payload)
	}
	clear, err := wrapped.ListAssistantThreadEvents(context.Background(), scope, "thread-1", 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(clear) != 1 || !bytes.Equal(clear[0].Payload, secret) {
		t.Fatalf("decrypted payload = %s", clear[0].Payload)
	}
}
