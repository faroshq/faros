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

package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFileStoreMigratesLegacyWorkspaceStateAndSnapshotsOnce(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	legacyScope := Scope{OrgUUID: "org-a", WorkspaceUUID: "ws-1", ProjectName: "demo"}
	legacyWorkspace := filepath.Join(root, legacyScope.OrgUUID, legacyScope.WorkspaceUUID, legacyScope.ProjectName)
	if err := os.MkdirAll(filepath.Join(legacyWorkspace, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyWorkspace, "src", "App.tsx"), []byte("legacy source\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	legacySnapshots := filepath.Join(root, workspaceSnapshotDirectory, legacyScope.OrgUUID, legacyScope.WorkspaceUUID, legacyScope.ProjectName)
	if err := os.MkdirAll(filepath.Join(legacySnapshots, "run-legacy"), 0o700); err != nil {
		t.Fatal(err)
	}
	entry := workspaceSnapshotEntry{
		Path:         "src/App.tsx",
		Existed:      true,
		Content:      []byte("legacy source\n"),
		AfterExisted: true,
		After:        []byte("changed source\n"),
		Mode:         0o600,
		AfterMode:    0o600,
	}
	entryRaw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	entryName := filepath.Join(legacySnapshots, "run-legacy", "entry.json")
	if err := os.WriteFile(entryName, entryRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	stateRaw := []byte(`{"uncommittedPaths":["src/App.tsx"]}`)
	if err := os.WriteFile(filepath.Join(legacySnapshots, workspaceSourceStateFile), stateRaw, 0o600); err != nil {
		t.Fatal(err)
	}

	store := NewFileStore(root)
	first := legacyScope
	first.ProjectUID = "project-first"
	read, err := store.ReadFile(ctx, first, ReadOptions{Path: "src/App.tsx"})
	if err != nil || read.Content != "legacy source\n" {
		t.Fatalf("migrated source = %#v, err=%v", read, err)
	}
	paths, err := store.UncommittedPaths(ctx, first)
	if err != nil || !reflect.DeepEqual(paths, []string{"src/App.tsx"}) {
		t.Fatalf("migrated source state = %v, err=%v", paths, err)
	}
	if _, err := store.WriteFile(ctx, first, WriteOptions{Path: "src/App.tsx", Content: "changed source\n"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RestoreSnapshot(ctx, first, "run-legacy"); err != nil {
		t.Fatalf("restore migrated snapshot: %v", err)
	}
	read, err = store.ReadFile(ctx, first, ReadOptions{Path: "src/App.tsx"})
	if err != nil || read.Content != "legacy source\n" {
		t.Fatalf("restored migrated source = %#v, err=%v", read, err)
	}

	second := legacyScope
	second.ProjectUID = "project-second"
	if _, err := store.ReadFile(ctx, second, ReadOptions{Path: "src/App.tsx"}); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("recreated project inherited migrated source: %v", err)
	}
	paths, err = store.UncommittedPaths(ctx, second)
	if err != nil || len(paths) != 0 {
		t.Fatalf("recreated project inherited source state = %v, err=%v", paths, err)
	}
	if _, err := store.RestoreSnapshot(ctx, second, "run-legacy"); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("recreated project inherited migrated snapshot: %v", err)
	}

	legacySourcePath := filepath.Join(root, legacyScope.OrgUUID, legacyScope.WorkspaceUUID, legacyScope.ProjectName, "src", "App.tsx")
	if _, err := os.Stat(legacySourcePath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("legacy source path still visible after migration: %v", err)
	}
}

func TestFileStoreLegacyMigrationBindsSnapshotsToWorkspaceFirstUID(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	legacyScope := seedLegacyMigrationFixture(t, root)
	first := legacyScope
	first.ProjectUID = "project-first"
	second := legacyScope
	second.ProjectUID = "project-second"
	store := NewFileStore(root)

	if _, err := store.ReadFile(ctx, first, ReadOptions{Path: "src/App.tsx"}); err != nil {
		t.Fatalf("migrate workspace with first UID: %v", err)
	}
	paths, err := store.UncommittedPaths(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 0 {
		t.Fatalf("UID2 claimed snapshots after workspace-first migration: %v", paths)
	}
	paths, err = store.UncommittedPaths(ctx, first)
	if err != nil || !reflect.DeepEqual(paths, []string{"src/App.tsx"}) {
		t.Fatalf("UID1 snapshots after workspace-first migration = %v, err=%v", paths, err)
	}
}

func TestFileStoreLegacyMigrationBindsWorkspaceToSnapshotsFirstUID(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	legacyScope := seedLegacyMigrationFixture(t, root)
	first := legacyScope
	first.ProjectUID = "project-first"
	second := legacyScope
	second.ProjectUID = "project-second"
	store := NewFileStore(root)

	paths, err := store.UncommittedPaths(ctx, first)
	if err != nil || !reflect.DeepEqual(paths, []string{"src/App.tsx"}) {
		t.Fatalf("migrate snapshots with first UID = %v, err=%v", paths, err)
	}
	if _, err := store.ReadFile(ctx, second, ReadOptions{Path: "src/App.tsx"}); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("UID2 claimed workspace after snapshots-first migration: %v", err)
	}
	read, err := store.ReadFile(ctx, first, ReadOptions{Path: "src/App.tsx"})
	if err != nil || read.Content != "legacy source\n" {
		t.Fatalf("UID1 workspace after snapshots-first migration = %#v, err=%v", read, err)
	}
}

func seedLegacyMigrationFixture(t *testing.T, root string) Scope {
	t.Helper()
	legacyScope := Scope{OrgUUID: "org-a", WorkspaceUUID: "ws-1", ProjectName: "demo"}
	legacyWorkspace := filepath.Join(root, legacyScope.OrgUUID, legacyScope.WorkspaceUUID, legacyScope.ProjectName)
	if err := os.MkdirAll(filepath.Join(legacyWorkspace, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyWorkspace, "src", "App.tsx"), []byte("legacy source\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	legacySnapshots := filepath.Join(root, workspaceSnapshotDirectory, legacyScope.OrgUUID, legacyScope.WorkspaceUUID, legacyScope.ProjectName, "run-legacy")
	if err := os.MkdirAll(legacySnapshots, 0o700); err != nil {
		t.Fatal(err)
	}
	entryRaw, err := json.Marshal(workspaceSnapshotEntry{
		Path:         "src/App.tsx",
		Existed:      true,
		Content:      []byte("legacy source\n"),
		AfterExisted: true,
		After:        []byte("changed source\n"),
		Mode:         0o600,
		AfterMode:    0o600,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacySnapshots, "entry.json"), entryRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(legacySnapshots), workspaceSourceStateFile), []byte(`{"uncommittedPaths":["src/App.tsx"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return legacyScope
}

func TestFileStoreUnifiedDeleteRejectsOversizedTarget(t *testing.T) {
	ctx := context.Background()
	store := NewFileStore(t.TempDir())
	scope := Scope{OrgUUID: "org-a", WorkspaceUUID: "ws-1", ProjectName: "demo", ProjectUID: "project-uid"}
	content := string(make([]byte, MaxWriteBytes+1))
	if err := store.ApplyFiles(ctx, scope, []File{{Path: "large.txt", Content: content}}); err != nil {
		t.Fatal(err)
	}
	_, err := store.ApplyPatch(ctx, scope, PatchOptions{Patch: "*** Begin Patch\n*** Delete File: large.txt\n*** End Patch"})
	var patchErr *PatchError
	if !errors.As(err, &patchErr) || patchErr.Code != PatchErrorInvalidPatch {
		t.Fatalf("oversized delete error = %v (%T), want invalid_patch", err, err)
	}
	if _, err := store.ReadFile(ctx, scope, ReadOptions{Path: "large.txt", MaxBytes: MaxWriteBytes}); err != nil {
		t.Fatalf("oversized target disappeared after rejected delete: %v", err)
	}
}
