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
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// The SQL implementation is intentionally kept beside the legacy run queries
// while callers migrate to the WorkItem operations. The work-item table itself
// has the immutable Project UID in every key and index.
func (s *PostgresStore) CreateWorkItemAndAssistantRun(ctx context.Context, scope Scope, item AssistantWorkItem, user Message, assistant Message, run AssistantRun) (AssistantWorkItem, error) {
	if err := scope.validate(); err != nil {
		return AssistantWorkItem{}, err
	}
	if err := validateWorkItemCreate(item, user, assistant, run); err != nil {
		return AssistantWorkItem{}, err
	}
	item, user, assistant, run = prepareAssistantWorkItem(scope, item), prepareMessage(scope, user), prepareMessage(scope, assistant), prepareAssistantRun(scope, run)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AssistantWorkItem{}, fmt.Errorf("begin create work item: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	rootAttached := false
	row := tx.QueryRowContext(ctx, `SELECT message_id, actor_id, work_item_id, role, content, content_encrypted, content_key_id, metadata, created_at, updated_at
		FROM app_studio_messages
		WHERE org_uuid=$1 AND workspace_uuid=$2 AND project_name=$3 AND project_uid=$4 AND message_id=$5`,
		scope.OrgUUID, scope.WorkspaceUUID, scope.ProjectName, scope.ProjectUID, item.RootMessageID)
	existingRoot, rootErr := scanMessage(row, scope)
	if rootErr != nil && !errors.Is(rootErr, sql.ErrNoRows) {
		return AssistantWorkItem{}, fmt.Errorf("read work item root message: %w", rootErr)
	}
	if rootErr == nil {
		if existingRoot.WorkItemID != "" || existingRoot.Role != user.Role || existingRoot.ActorID != user.ActorID || existingRoot.Content != user.Content {
			return AssistantWorkItem{}, fmt.Errorf("%w: root message %q cannot be attached", ErrAssistantWorkItemConflict, item.RootMessageID)
		}
		rootAttached = true
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO app_studio_assistant_work_items (
		org_uuid, workspace_uuid, project_name, project_uid, work_item_id, root_message_id, created_by,
		status, status_reason, revision, active_run_id, plan_grant, grant_revision, created_at, updated_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		scope.OrgUUID, scope.WorkspaceUUID, scope.ProjectName, scope.ProjectUID, item.ID, item.RootMessageID, item.CreatedBy,
		item.Status, item.StatusReason, item.Revision, run.ID, `{}`, "", item.CreatedAt, item.UpdatedAt)
	if err != nil {
		return AssistantWorkItem{}, fmt.Errorf("%w: create work item: %v", ErrAssistantWorkItemConflict, err)
	}
	checkpoint, audit, err := normalizeAssistantRunJSON(run)
	if err != nil {
		return AssistantWorkItem{}, err
	}
	row = tx.QueryRowContext(ctx, `INSERT INTO app_studio_assistant_runs (
		org_uuid, workspace_uuid, project_name, project_uid, run_id, work_item_id, mode, expected_grant_revision, status,
		client_request_id, user_message_id, active_message_id, revision, request_id, checkpoint, audit, created_at, updated_at
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)
	RETURNING run_id, work_item_id, mode, expected_grant_revision, status, client_request_id, user_message_id, active_message_id, revision,
		request_id, checkpoint, audit, created_at, updated_at`,
		scope.OrgUUID, scope.WorkspaceUUID, scope.ProjectName, scope.ProjectUID, run.ID, run.WorkItemID, run.Mode, run.ExpectedGrantRevision, run.Status,
		run.ClientRequestID, run.UserMessageID, run.ActiveMessageID, run.Revision, run.RequestID, string(checkpoint), string(audit), run.CreatedAt.UTC(), run.UpdatedAt.UTC())
	if _, err := scanAssistantRun(row, scope); err != nil {
		return AssistantWorkItem{}, fmt.Errorf("create work item run: %w", err)
	}
	if rootAttached {
		result, err := tx.ExecContext(ctx, `UPDATE app_studio_messages SET work_item_id=$6
			WHERE org_uuid=$1 AND workspace_uuid=$2 AND project_name=$3 AND project_uid=$4 AND message_id=$5
				AND actor_id=$7 AND work_item_id='' AND role=$8 AND content=$9`,
			scope.OrgUUID, scope.WorkspaceUUID, scope.ProjectName, scope.ProjectUID, user.ID, item.ID, user.ActorID, user.Role, user.Content)
		if err != nil {
			return AssistantWorkItem{}, fmt.Errorf("attach work item root message: %w", err)
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return AssistantWorkItem{}, fmt.Errorf("%w: root message %q cannot be attached", ErrAssistantWorkItemConflict, item.RootMessageID)
		}
	} else {
		user.WorkItemID = item.ID
		if err := appendMessageTx(ctx, tx, scope, user); err != nil {
			return AssistantWorkItem{}, err
		}
	}
	assistant.WorkItemID = item.ID
	if err := appendMessageTx(ctx, tx, scope, assistant); err != nil {
		return AssistantWorkItem{}, err
	}
	if err := tx.Commit(); err != nil {
		return AssistantWorkItem{}, fmt.Errorf("commit create work item: %w", err)
	}
	item.ActiveRunID = run.ID
	return item, nil
}

// ResumeWorkItemAndCreateAssistantRun is the sole durable boundary for
// continuing a suspended mutation task. It locks the selected item, checks
// actor and revision, activates it, and inserts the next messages/run in one
// transaction.
func (s *PostgresStore) ResumeWorkItemAndCreateAssistantRun(ctx context.Context, scope Scope, workItemID, actor string, expectedRevision int64, user Message, assistant Message, run AssistantRun) (AssistantWorkItem, error) {
	if err := scope.validate(); err != nil {
		return AssistantWorkItem{}, err
	}
	if workItemID == "" || actor == "" || expectedRevision < 1 || user.ActorID != actor || user.WorkItemID != workItemID || assistant.WorkItemID != workItemID || run.WorkItemID != workItemID || run.Mode != AssistantRunModeContinue {
		return AssistantWorkItem{}, fmt.Errorf("%w: invalid work item continuation", ErrAssistantWorkItemConflict)
	}
	if err := validateNewAssistantRun(user, assistant, run); err != nil {
		return AssistantWorkItem{}, err
	}
	user, assistant, run = prepareMessage(scope, user), prepareMessage(scope, assistant), prepareAssistantRun(scope, run)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AssistantWorkItem{}, fmt.Errorf("begin resume work item: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	row := tx.QueryRowContext(ctx, `SELECT work_item_id, root_message_id, created_by, status, status_reason, revision, active_run_id, plan_grant, grant_revision, created_at, updated_at
		FROM app_studio_assistant_work_items
		WHERE org_uuid=$1 AND workspace_uuid=$2 AND project_name=$3 AND project_uid=$4 AND work_item_id=$5 FOR UPDATE`, scope.OrgUUID, scope.WorkspaceUUID, scope.ProjectName, scope.ProjectUID, workItemID)
	item, err := scanAssistantWorkItem(row, scope)
	if err != nil {
		return AssistantWorkItem{}, fmt.Errorf("%w: %v", ErrAssistantWorkItemConflict, err)
	}
	if item.CreatedBy != actor || item.Status != AssistantWorkItemStatusSuspended || item.ActiveRunID != "" || item.Revision != expectedRevision || run.ExpectedGrantRevision != item.GrantRevision {
		return AssistantWorkItem{}, fmt.Errorf("%w: work item %q is not resumable", ErrAssistantWorkItemConflict, workItemID)
	}
	var activeWorkItem string
	err = tx.QueryRowContext(ctx, `SELECT work_item_id FROM app_studio_assistant_work_items WHERE org_uuid=$1 AND workspace_uuid=$2 AND project_name=$3 AND project_uid=$4 AND status=$5 AND work_item_id<>$6 LIMIT 1`, scope.OrgUUID, scope.WorkspaceUUID, scope.ProjectName, scope.ProjectUID, AssistantWorkItemStatusActive, workItemID).Scan(&activeWorkItem)
	if err == nil {
		return AssistantWorkItem{}, fmt.Errorf("%w: project already has active work item %q", ErrAssistantWorkItemConflict, activeWorkItem)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return AssistantWorkItem{}, fmt.Errorf("check active work item: %w", err)
	}
	var activeRun string
	err = tx.QueryRowContext(ctx, `SELECT run_id FROM app_studio_assistant_runs WHERE org_uuid=$1 AND workspace_uuid=$2 AND project_name=$3 AND project_uid=$4 AND status IN ('pending_permission','pending_input','running','stopping') LIMIT 1`, scope.OrgUUID, scope.WorkspaceUUID, scope.ProjectName, scope.ProjectUID).Scan(&activeRun)
	if err == nil {
		return AssistantWorkItem{}, fmt.Errorf("%w: project already has active assistant run %q", ErrAssistantRunConflict, activeRun)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return AssistantWorkItem{}, fmt.Errorf("check active assistant run: %w", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE app_studio_assistant_work_items SET status=$6, status_reason='', active_run_id=$7, revision=revision+1, updated_at=$8
		WHERE org_uuid=$1 AND workspace_uuid=$2 AND project_name=$3 AND project_uid=$4 AND work_item_id=$5 AND revision=$9 AND status=$10 AND created_by=$11 AND active_run_id=''`,
		scope.OrgUUID, scope.WorkspaceUUID, scope.ProjectName, scope.ProjectUID, workItemID, AssistantWorkItemStatusActive, run.ID, run.UpdatedAt.UTC(), expectedRevision, AssistantWorkItemStatusSuspended, actor)
	if err != nil {
		return AssistantWorkItem{}, fmt.Errorf("activate work item: %w", err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return AssistantWorkItem{}, fmt.Errorf("%w: work item %q changed", ErrAssistantWorkItemConflict, workItemID)
	}
	checkpoint, audit, err := normalizeAssistantRunJSON(run)
	if err != nil {
		return AssistantWorkItem{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO app_studio_assistant_runs (org_uuid, workspace_uuid, project_name, project_uid, run_id, work_item_id, mode, expected_grant_revision, status, client_request_id, user_message_id, active_message_id, revision, request_id, checkpoint, audit, created_at, updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`, scope.OrgUUID, scope.WorkspaceUUID, scope.ProjectName, scope.ProjectUID, run.ID, run.WorkItemID, run.Mode, run.ExpectedGrantRevision, run.Status, run.ClientRequestID, run.UserMessageID, run.ActiveMessageID, run.Revision, run.RequestID, string(checkpoint), string(audit), run.CreatedAt.UTC(), run.UpdatedAt.UTC()); err != nil {
		return AssistantWorkItem{}, fmt.Errorf("%w: create continuation run: %v", ErrAssistantRunConflict, err)
	}
	if err := appendMessageTx(ctx, tx, scope, user); err != nil {
		return AssistantWorkItem{}, err
	}
	if err := appendMessageTx(ctx, tx, scope, assistant); err != nil {
		return AssistantWorkItem{}, err
	}
	if err := tx.Commit(); err != nil {
		return AssistantWorkItem{}, fmt.Errorf("commit resume work item: %w", err)
	}
	item.Status, item.StatusReason, item.ActiveRunID, item.Revision, item.UpdatedAt = AssistantWorkItemStatusActive, "", run.ID, item.Revision+1, run.UpdatedAt
	return item, nil
}

func (s *PostgresStore) GetAssistantWorkItem(ctx context.Context, scope Scope, id string) (AssistantWorkItem, error) {
	if err := scope.validate(); err != nil {
		return AssistantWorkItem{}, err
	}
	row := s.db.QueryRowContext(ctx, `SELECT work_item_id, root_message_id, created_by, status, status_reason, revision, active_run_id, plan_grant, grant_revision, created_at, updated_at
		FROM app_studio_assistant_work_items WHERE org_uuid=$1 AND workspace_uuid=$2 AND project_name=$3 AND project_uid=$4 AND work_item_id=$5`, scope.OrgUUID, scope.WorkspaceUUID, scope.ProjectName, scope.ProjectUID, id)
	item, err := scanAssistantWorkItem(row, scope)
	if err == sql.ErrNoRows {
		return AssistantWorkItem{}, fmt.Errorf("%w: %q", ErrAssistantWorkItemNotFound, id)
	}
	if err != nil {
		return AssistantWorkItem{}, fmt.Errorf("get assistant work item: %w", err)
	}
	return item, nil
}

func (s *PostgresStore) ListAssistantWorkItems(ctx context.Context, scope Scope) ([]AssistantWorkItem, error) {
	if err := scope.validate(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT work_item_id, root_message_id, created_by, status, status_reason, revision, active_run_id, plan_grant, grant_revision, created_at, updated_at
		FROM app_studio_assistant_work_items WHERE org_uuid=$1 AND workspace_uuid=$2 AND project_name=$3 AND project_uid=$4 ORDER BY created_at, work_item_id`, scope.OrgUUID, scope.WorkspaceUUID, scope.ProjectName, scope.ProjectUID)
	if err != nil {
		return nil, fmt.Errorf("list assistant work items: %w", err)
	}
	defer rows.Close()
	var items []AssistantWorkItem
	for rows.Next() {
		item, err := scanAssistantWorkItem(rows, scope)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) CompareAndSwapAssistantWorkItem(ctx context.Context, scope Scope, item AssistantWorkItem, expectedRevision int64) error {
	if err := scope.validate(); err != nil {
		return err
	}
	if item.ID == "" || item.Status == "" || item.Revision != expectedRevision+1 {
		return fmt.Errorf("%w: work item revision", ErrAssistantWorkItemConflict)
	}
	item = prepareAssistantWorkItem(scope, item)
	res, err := s.db.ExecContext(ctx, `UPDATE app_studio_assistant_work_items SET status=$6,status_reason=$7,revision=$8,active_run_id=$9,plan_grant=$10,grant_revision=$11,updated_at=$12
		WHERE org_uuid=$1 AND workspace_uuid=$2 AND project_name=$3 AND project_uid=$4 AND work_item_id=$5 AND revision=$13
			AND root_message_id=$14 AND created_by=$15`,
		scope.OrgUUID, scope.WorkspaceUUID, scope.ProjectName, scope.ProjectUID, item.ID, item.Status, item.StatusReason, item.Revision, item.ActiveRunID, string(item.PlanGrant), item.GrantRevision, item.UpdatedAt, expectedRevision, item.RootMessageID, item.CreatedBy)
	if err != nil {
		return fmt.Errorf("update assistant work item: %w", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return fmt.Errorf("%w: work item %q", ErrAssistantWorkItemConflict, item.ID)
	}
	return nil
}

func (s *PostgresStore) ApproveWorkItemPlan(ctx context.Context, scope Scope, workItemID, runID string, expectedRevision int64, grantRevision string, planGrant json.RawMessage, now time.Time) (AssistantWorkItem, error) {
	if err := scope.validate(); err != nil {
		return AssistantWorkItem{}, err
	}
	if workItemID == "" || runID == "" || grantRevision == "" || len(planGrant) == 0 {
		return AssistantWorkItem{}, fmt.Errorf("work item, run, grant revision, and grant are required")
	}
	if !json.Valid(planGrant) {
		return AssistantWorkItem{}, fmt.Errorf("work item plan grant is not valid json")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AssistantWorkItem{}, fmt.Errorf("begin approve work item plan: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	updatedAt := now.UTC()
	row := tx.QueryRowContext(ctx, `UPDATE app_studio_assistant_work_items
		SET plan_grant=$6, grant_revision=$7, revision=revision+1, updated_at=$8
		WHERE org_uuid=$1 AND workspace_uuid=$2 AND project_name=$3 AND project_uid=$4 AND work_item_id=$5
			AND revision=$9 AND status=$10 AND active_run_id=$11
		RETURNING work_item_id, root_message_id, created_by, status, status_reason, revision, active_run_id, plan_grant, grant_revision, created_at, updated_at`,
		scope.OrgUUID, scope.WorkspaceUUID, scope.ProjectName, scope.ProjectUID, workItemID, string(planGrant), grantRevision, updatedAt,
		expectedRevision, AssistantWorkItemStatusActive, runID)
	item, err := scanAssistantWorkItem(row, scope)
	if err == sql.ErrNoRows {
		return AssistantWorkItem{}, fmt.Errorf("%w: work item %q", ErrAssistantWorkItemConflict, workItemID)
	}
	if err != nil {
		return AssistantWorkItem{}, fmt.Errorf("approve work item plan: %w", err)
	}
	res, err := tx.ExecContext(ctx, `UPDATE app_studio_assistant_runs SET expected_grant_revision=$6
		WHERE org_uuid=$1 AND workspace_uuid=$2 AND project_name=$3 AND project_uid=$4 AND run_id=$5
			AND work_item_id=$7 AND status=$8`,
		scope.OrgUUID, scope.WorkspaceUUID, scope.ProjectName, scope.ProjectUID, runID, grantRevision, workItemID, AssistantRunStatusRunning)
	if err != nil {
		return AssistantWorkItem{}, fmt.Errorf("stamp work item grant on run: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return AssistantWorkItem{}, fmt.Errorf("%w: run %q", ErrAssistantWorkItemConflict, runID)
	}
	if err := tx.Commit(); err != nil {
		return AssistantWorkItem{}, fmt.Errorf("commit approve work item plan: %w", err)
	}
	return item, nil
}

func (s *PostgresStore) TransitionWorkItemAndRun(ctx context.Context, scope Scope, workItemID string, expectedWorkItemRevision int64, run AssistantRun, status AssistantWorkItemStatus, reason string, now time.Time) error {
	if err := scope.validate(); err != nil {
		return err
	}
	if workItemID == "" || run.ID == "" || !assistantRunStatusTerminal(run.Status) || !assistantWorkItemTerminalTransitionValid(status, run.Status) {
		return fmt.Errorf("terminal run required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	run = prepareAssistantRun(scope, run)
	run.UpdatedAt = now.UTC()
	_, audit, err := normalizeAssistantRunJSON(run)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transition work item and run: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE app_studio_assistant_work_items
		SET status=$6, status_reason=$7, active_run_id='', plan_grant='{}'::jsonb, grant_revision='', revision=revision+1, updated_at=$8
		WHERE org_uuid=$1 AND workspace_uuid=$2 AND project_name=$3 AND project_uid=$4 AND work_item_id=$5
			AND revision=$9 AND active_run_id=$10`,
		scope.OrgUUID, scope.WorkspaceUUID, scope.ProjectName, scope.ProjectUID, workItemID, status, reason, now.UTC(), expectedWorkItemRevision, run.ID)
	if err != nil {
		return fmt.Errorf("transition work item: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("%w: work item %q", ErrAssistantWorkItemConflict, workItemID)
	}
	res, err = tx.ExecContext(ctx, `UPDATE app_studio_assistant_runs
		SET status=$6, active_message_id=$7, revision=$8, request_id=$9, checkpoint='{}'::jsonb, audit=$10, updated_at=$11
		WHERE org_uuid=$1 AND workspace_uuid=$2 AND project_name=$3 AND project_uid=$4 AND run_id=$5
			AND revision=$12 AND work_item_id=$13 AND mode=$14`,
		scope.OrgUUID, scope.WorkspaceUUID, scope.ProjectName, scope.ProjectUID, run.ID,
		run.Status, run.ActiveMessageID, run.Revision, run.RequestID, string(audit), run.UpdatedAt.UTC(), run.Revision-1, run.WorkItemID, run.Mode)
	if err != nil {
		return fmt.Errorf("transition work item run: %w", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("%w: assistant run %q", ErrAssistantRunConflict, run.ID)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transition work item and run: %w", err)
	}
	return nil
}

func (s *PostgresStore) RequestAssistantRunStop(ctx context.Context, scope Scope, workItemID, runID string, expectedWorkItemRevision, expectedRunRevision int64, now time.Time) (AssistantRun, error) {
	if err := scope.validate(); err != nil {
		return AssistantRun{}, err
	}
	if runID == "" || expectedRunRevision < 1 {
		return AssistantRun{}, fmt.Errorf("assistant run and revision are required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AssistantRun{}, fmt.Errorf("begin request assistant stop: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if workItemID != "" {
		result, err := tx.ExecContext(ctx, `UPDATE app_studio_assistant_work_items
			SET plan_grant='{}'::jsonb, grant_revision='', revision=revision+1, updated_at=$8
			WHERE org_uuid=$1 AND workspace_uuid=$2 AND project_name=$3 AND project_uid=$4
				AND work_item_id=$5 AND revision=$6 AND active_run_id=$7 AND status=$9`,
			scope.OrgUUID, scope.WorkspaceUUID, scope.ProjectName, scope.ProjectUID,
			workItemID, expectedWorkItemRevision, runID, now.UTC(), AssistantWorkItemStatusActive)
		if err != nil {
			return AssistantRun{}, fmt.Errorf("revoke work item grant for stop: %w", err)
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return AssistantRun{}, fmt.Errorf("%w: work item %q", ErrAssistantWorkItemConflict, workItemID)
		}
	}
	row := tx.QueryRowContext(ctx, `UPDATE app_studio_assistant_runs
		SET status=$8, revision=revision+1, updated_at=$9
		WHERE org_uuid=$1 AND workspace_uuid=$2 AND project_name=$3 AND project_uid=$4
			AND run_id=$5 AND work_item_id=$6 AND revision=$7 AND status=$10
		RETURNING run_id,work_item_id,mode,expected_grant_revision,status,client_request_id,user_message_id,active_message_id,revision,request_id,checkpoint,audit,created_at,updated_at`,
		scope.OrgUUID, scope.WorkspaceUUID, scope.ProjectName, scope.ProjectUID,
		runID, workItemID, expectedRunRevision, AssistantRunStatusStopping, now.UTC(), AssistantRunStatusRunning)
	run, err := scanAssistantRun(row, scope)
	if errors.Is(err, sql.ErrNoRows) {
		return AssistantRun{}, fmt.Errorf("%w: assistant run %q", ErrAssistantRunConflict, runID)
	}
	if err != nil {
		return AssistantRun{}, fmt.Errorf("request assistant run stop: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return AssistantRun{}, fmt.Errorf("commit request assistant stop: %w", err)
	}
	return run, nil
}

func (s *PostgresStore) LoadMessagesForWorkItem(ctx context.Context, scope Scope, workItemID string, limit int) ([]Message, error) {
	if err := scope.validate(); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT message_id, actor_id, work_item_id, role, content, content_encrypted, content_key_id, metadata, created_at, updated_at FROM app_studio_messages
		WHERE org_uuid=$1 AND workspace_uuid=$2 AND project_name=$3 AND project_uid=$4 AND work_item_id=$5 ORDER BY created_at, message_id LIMIT $6`, scope.OrgUUID, scope.WorkspaceUUID, scope.ProjectName, scope.ProjectUID, workItemID, normalizeLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("load work item messages: %w", err)
	}
	defer rows.Close()
	var messages []Message
	for rows.Next() {
		msg, err := scanMessage(rows, scope)
		if err != nil {
			return nil, err
		}
		messages = append(messages, msg)
	}
	return messages, rows.Err()
}

func (s *PostgresStore) LatestAssistantRunForWorkItem(ctx context.Context, scope Scope, workItemID string) (AssistantRun, error) {
	row := s.db.QueryRowContext(ctx, `SELECT run_id,work_item_id,mode,expected_grant_revision,status,client_request_id,user_message_id,active_message_id,revision,request_id,checkpoint,audit,created_at,updated_at FROM app_studio_assistant_runs
		WHERE org_uuid=$1 AND workspace_uuid=$2 AND project_name=$3 AND project_uid=$4 AND work_item_id=$5 ORDER BY updated_at DESC, run_id DESC LIMIT 1`, scope.OrgUUID, scope.WorkspaceUUID, scope.ProjectName, scope.ProjectUID, workItemID)
	run, err := scanAssistantRun(row, scope)
	if err == sql.ErrNoRows {
		return AssistantRun{}, fmt.Errorf("%w: latest work item run", ErrAssistantRunNotFound)
	}
	return run, err
}

func scanAssistantWorkItem(row interface{ Scan(...any) error }, scope Scope) (AssistantWorkItem, error) {
	var item AssistantWorkItem
	var status string
	err := row.Scan(&item.ID, &item.RootMessageID, &item.CreatedBy, &status, &item.StatusReason, &item.Revision, &item.ActiveRunID, &item.PlanGrant, &item.GrantRevision, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return AssistantWorkItem{}, err
	}
	item.ProjectName, item.ProjectUID, item.Status, item.CreatedAt, item.UpdatedAt = scope.ProjectName, scope.ProjectUID, AssistantWorkItemStatus(status), item.CreatedAt.UTC(), item.UpdatedAt.UTC()
	return item, nil
}
