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
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
)

func (s *PostgresStore) AppendAssistantConversationItem(ctx context.Context, scope Scope, item AssistantConversationItem) (AssistantConversationItem, error) {
	if s == nil || s.db == nil {
		return AssistantConversationItem{}, fmt.Errorf("postgres store is nil")
	}
	prepared, err := prepareAssistantConversationItem(scope, item)
	if err != nil {
		return AssistantConversationItem{}, err
	}
	payload, err := normalizePostgresJSONB(prepared.Payload)
	if err != nil {
		return AssistantConversationItem{}, fmt.Errorf("assistant conversation payload is not valid json: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AssistantConversationItem{}, fmt.Errorf("begin append assistant conversation item: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	lockKey := assistantConversationLockKey(scope)
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return AssistantConversationItem{}, fmt.Errorf("lock assistant conversation stream: %w", err)
	}
	row := tx.QueryRowContext(ctx, `INSERT INTO app_studio_assistant_conversation_items (
		org_uuid, workspace_uuid, project_name, project_uid, item_id, run_id, sequence, item_type, payload, created_at
	) SELECT $1,$2,$3,$4,$5,$6,COALESCE(MAX(sequence),0)+1,$7,$8,$9
	  FROM app_studio_assistant_conversation_items
	 WHERE org_uuid=$1 AND workspace_uuid=$2 AND project_name=$3 AND project_uid=$4
	 ON CONFLICT (org_uuid, workspace_uuid, project_name, project_uid, run_id, item_id) DO NOTHING
	 RETURNING sequence`, scope.OrgUUID, scope.WorkspaceUUID, scope.ProjectName, scope.ProjectUID,
		prepared.ID, prepared.RunID, prepared.Type, string(payload), prepared.CreatedAt)
	if err := row.Scan(&prepared.Sequence); err == sql.ErrNoRows {
		var existing AssistantConversationItem
		err = tx.QueryRowContext(ctx, `SELECT sequence, run_id, item_type, payload, created_at
			FROM app_studio_assistant_conversation_items
			WHERE org_uuid=$1 AND workspace_uuid=$2 AND project_name=$3 AND project_uid=$4 AND run_id=$5 AND item_id=$6`,
			scope.OrgUUID, scope.WorkspaceUUID, scope.ProjectName, scope.ProjectUID, prepared.RunID, prepared.ID,
		).Scan(&existing.Sequence, &existing.RunID, &existing.Type, &existing.Payload, &existing.CreatedAt)
		if err == nil {
			existing.ID = prepared.ID
			existing.ProjectName, existing.ProjectUID = scope.ProjectName, scope.ProjectUID
			existing.CreatedAt = existing.CreatedAt.UTC()
			if !assistantConversationItemsMatch(existing, prepared) {
				return AssistantConversationItem{}, ErrAssistantConversationItemConflict
			}
			prepared = existing
		}
	}
	if err != nil {
		return AssistantConversationItem{}, fmt.Errorf("append assistant conversation item: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return AssistantConversationItem{}, fmt.Errorf("commit assistant conversation item: %w", err)
	}
	prepared.Payload = cloneRawMessage(prepared.Payload)
	return prepared, nil
}

func assistantConversationLockKey(scope Scope) string {
	digest := sha256.Sum256([]byte(scope.OrgUUID + "\x00" + scope.WorkspaceUUID + "\x00" + scope.ProjectName + "\x00" + scope.ProjectUID))
	return hex.EncodeToString(digest[:])
}

func (s *PostgresStore) ListAssistantConversationItems(ctx context.Context, scope Scope, afterSequence int64, limit int) ([]AssistantConversationItem, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("postgres store is nil")
	}
	if err := scope.validate(); err != nil {
		return nil, err
	}
	limit = normalizeLimit(limit)
	rows, err := s.db.QueryContext(ctx, `SELECT item_id, run_id, sequence, item_type, payload, created_at
		FROM app_studio_assistant_conversation_items
		WHERE org_uuid=$1 AND workspace_uuid=$2 AND project_name=$3 AND project_uid=$4 AND sequence>$5
		ORDER BY sequence ASC LIMIT $6`, scope.OrgUUID, scope.WorkspaceUUID, scope.ProjectName, scope.ProjectUID, afterSequence, limit)
	if err != nil {
		return nil, fmt.Errorf("list assistant conversation items: %w", err)
	}
	defer rows.Close()
	items := make([]AssistantConversationItem, 0, limit)
	for rows.Next() {
		var item AssistantConversationItem
		if err := rows.Scan(&item.ID, &item.RunID, &item.Sequence, &item.Type, &item.Payload, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan assistant conversation item: %w", err)
		}
		item.ProjectName, item.ProjectUID = scope.ProjectName, scope.ProjectUID
		item.CreatedAt = item.CreatedAt.UTC()
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate assistant conversation items: %w", err)
	}
	return items, nil
}
