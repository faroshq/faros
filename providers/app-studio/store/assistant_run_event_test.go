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
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"
)

func TestMemoryStoreAssistantRunEventsAreAppendOnlyAndScoped(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	scope := Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo", ProjectUID: "project-a"}
	otherScope := Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo", ProjectUID: "project-b"}
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	for _, candidate := range []Scope{scope, otherScope} {
		if err := s.SaveAssistantRun(ctx, candidate, AssistantRun{ID: "run-1", EngineVersion: AssistantEngineVersionV2, Status: AssistantRunStatusRunning, CreatedAt: now, UpdatedAt: now}); err != nil {
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
	if err := s.SaveAssistantRun(ctx, scope, AssistantRun{ID: "run-1", Status: AssistantRunStatusRunning}); err != nil {
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

func TestMemoryStoreAssistantRunEngineVersionAndEventRetention(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	scope := Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo", ProjectUID: "project-a"}
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	run := AssistantRun{
		ID:              "run-1",
		Mode:            AssistantRunModeDefault,
		EngineVersion:   AssistantEngineVersionV2,
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
	if created.EngineVersion != AssistantEngineVersionV2 {
		t.Fatalf("engine version = %q, want %q", created.EngineVersion, AssistantEngineVersionV2)
	}
	changed := created
	changed.Mode = AssistantRunModeDiscussion
	changed.EngineVersion = "v3"
	changed.Revision++
	changed.UpdatedAt = now.Add(time.Second)
	if err := s.SaveAssistantRunSnapshot(ctx, scope, changed, nil, created.Revision); !errors.Is(err, ErrAssistantRunConflict) {
		t.Fatalf("changed engine contract snapshot error = %v, want ErrAssistantRunConflict", err)
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
	if err := encrypted.SaveAssistantRun(ctx, scope, AssistantRun{ID: "run-1", EngineVersion: AssistantEngineVersionV2, Status: AssistantRunStatusRunning}); err != nil {
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

func TestAssistantRunEventSchemaMigrationIsAdditive(t *testing.T) {
	statements := assistantRunEventSchemaStatements()
	if assistantRunEventSchemaVersion == messageSchemaVersion || len(statements) == 0 {
		t.Fatalf("assistant event migration version=%q statements=%#v", assistantRunEventSchemaVersion, statements)
	}
	for _, statement := range statements {
		upper := strings.ToUpper(strings.TrimSpace(statement))
		if strings.HasPrefix(upper, "DROP ") || strings.HasPrefix(upper, "DELETE ") || strings.HasPrefix(upper, "TRUNCATE ") {
			t.Fatalf("assistant event migration is destructive: %q", statement)
		}
	}
	if !schemaStatementsContain(statements, "ADD COLUMN IF NOT EXISTS engine_version") ||
		!schemaStatementsContain(statements, "CREATE TABLE IF NOT EXISTS app_studio_assistant_run_events") ||
		!schemaStatementsContain(statements, "ON DELETE CASCADE") {
		t.Fatalf("assistant event migration does not contain engine version, event table, and run-owned retention: %#v", statements)
	}
	if !schemaStatementsContain(workItemSchemaStatements(), "engine_version text NOT NULL DEFAULT ''") ||
		!schemaStatementsContain(workItemSchemaUpgradeStatements(), "ADD COLUMN IF NOT EXISTS engine_version") {
		t.Fatal("fresh and legacy work-item schemas do not include the engine version column")
	}
}

func TestPostgresStoreAssistantRunEventMigrationAndRoundTripExternalDSN(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("APP_STUDIO_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("APP_STUDIO_TEST_POSTGRES_DSN is not set")
	}
	ctx := context.Background()
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer admin.Close()
	schemaName := "app_studio_events_" + time.Now().UTC().Format("20060102150405")
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+pq.QuoteIdentifier(schemaName)); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), "DROP SCHEMA "+pq.QuoteIdentifier(schemaName)+" CASCADE")
	})
	scopedDSN := postgresDSNWithSearchPath(t, dsn, schemaName)
	legacy, err := sql.Open("postgres", scopedDSN)
	if err != nil {
		t.Fatalf("open legacy schema: %v", err)
	}
	if _, err := legacy.ExecContext(ctx, createMessageSchemaMigrationsTable); err != nil {
		t.Fatalf("create migrations table: %v", err)
	}
	for _, statement := range workItemSchemaStatements() {
		if _, err := legacy.ExecContext(ctx, statement); err != nil {
			t.Fatalf("apply prior work-item schema statement: %v", err)
		}
	}
	if _, err := legacy.ExecContext(ctx, `ALTER TABLE app_studio_assistant_runs DROP COLUMN engine_version`); err != nil {
		t.Fatalf("shape prior run schema: %v", err)
	}
	if _, err := legacy.ExecContext(ctx, `INSERT INTO app_studio_message_schema_migrations(version) VALUES ($1)`, messageSchemaVersion); err != nil {
		t.Fatalf("record prior schema version: %v", err)
	}
	if _, err := legacy.ExecContext(ctx, `INSERT INTO app_studio_assistant_work_items (
		org_uuid,workspace_uuid,project_name,project_uid,work_item_id,root_message_id,created_by,status,revision,created_at,updated_at
	) VALUES ('org-a','workspace-a','demo','project-a','item-1','user-1','alice','suspended',1,now(),now())`); err != nil {
		t.Fatalf("insert legacy work item: %v", err)
	}
	if _, err := legacy.ExecContext(ctx, `INSERT INTO app_studio_assistant_runs (
		org_uuid,workspace_uuid,project_name,project_uid,run_id,work_item_id,mode,approval_mode,status,revision,created_at,updated_at
	) VALUES ('org-a','workspace-a','demo','project-a','run-1','item-1','continue','auto_approve','completed',1,now(),now())`); err != nil {
		t.Fatalf("insert legacy assistant run: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy schema: %v", err)
	}

	s, err := OpenPostgres(ctx, scopedDSN)
	if err != nil {
		t.Fatalf("OpenPostgres after prior schema: %v", err)
	}
	defer s.Close()
	scope := Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo", ProjectUID: "project-a"}
	run, err := s.GetAssistantRun(ctx, scope, "run-1")
	if err != nil {
		t.Fatalf("GetAssistantRun after migration: %v", err)
	}
	if run.EngineVersion != "" {
		t.Fatalf("legacy engine version = %q, want empty", run.EngineVersion)
	}
	if _, err := s.GetAssistantWorkItem(ctx, scope, "item-1"); err != nil {
		t.Fatalf("GetAssistantWorkItem after migration: %v", err)
	}
	first, err := s.AppendAssistantRunEvent(ctx, scope, AssistantRunEvent{RunID: run.ID, Type: "legacy_replayed", Payload: json.RawMessage(`{"ok":true}`)}, 0)
	if err != nil {
		t.Fatalf("AppendAssistantRunEvent after migration: %v", err)
	}
	if first.Sequence != 1 {
		t.Fatalf("first sequence = %d, want 1", first.Sequence)
	}
	if _, err := s.AppendAssistantRunEvent(ctx, scope, AssistantRunEvent{RunID: run.ID, Type: "conflict"}, 0); !errors.Is(err, ErrAssistantRunEventConflict) {
		t.Fatalf("stale event CAS error = %v, want ErrAssistantRunEventConflict", err)
	}
	events, err := s.ListAssistantRunEvents(ctx, scope, run.ID, 0, 10)
	if err != nil {
		t.Fatalf("ListAssistantRunEvents: %v", err)
	}
	if len(events) != 1 || events[0].Sequence != 1 || !jsonSemanticallyEqual(events[0].Payload, first.Payload) {
		t.Fatalf("events = %#v, want persisted first event", events)
	}

	old := time.Date(2026, 7, 31, 11, 0, 0, 0, time.UTC)
	if err := s.SaveAssistantRun(ctx, scope, AssistantRun{
		ID: "run-retained-event", EngineVersion: AssistantEngineVersionV2,
		Status: AssistantRunStatusCompleted, CreatedAt: old, UpdatedAt: old,
	}); err != nil {
		t.Fatalf("SaveAssistantRun retention candidate: %v", err)
	}
	versioned, err := s.GetAssistantRun(ctx, scope, "run-retained-event")
	if err != nil {
		t.Fatalf("GetAssistantRun versioned run: %v", err)
	}
	if versioned.EngineVersion != AssistantEngineVersionV2 {
		t.Fatalf("versioned engine = %q, want %q", versioned.EngineVersion, AssistantEngineVersionV2)
	}
	versioned.EngineVersion = "v3"
	if err := s.SaveAssistantRun(ctx, scope, versioned); !errors.Is(err, ErrAssistantRunConflict) {
		t.Fatalf("changed engine version error = %v, want ErrAssistantRunConflict", err)
	}
	if _, err := s.AppendAssistantRunEvent(ctx, scope, AssistantRunEvent{RunID: "run-retained-event", Type: "completed"}, 0); err != nil {
		t.Fatalf("AppendAssistantRunEvent retention candidate: %v", err)
	}
	deleted, err := s.DeleteMessagesOlderThan(ctx, old.Add(time.Minute))
	if err != nil {
		t.Fatalf("DeleteMessagesOlderThan: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("retention deleted = %d, want one unowned run", deleted)
	}
	if _, err := s.ListAssistantRunEvents(ctx, scope, "run-retained-event", 0, 10); !errors.Is(err, ErrAssistantRunNotFound) {
		t.Fatalf("retained event after run deletion error = %v, want ErrAssistantRunNotFound", err)
	}
	if _, err := s.ListAssistantRunEvents(ctx, scope, run.ID, 0, 10); err != nil {
		t.Fatalf("work-item run events were removed by unowned-run retention: %v", err)
	}
}
