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
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The ProjectUID scope was added after the initial workspace store shipped.
// Legacy data lived directly below the org/workspace/project directory, while
// current data lives below an additional UID directory. Keep the marker out
// of either tree so it can never become a source file or assistant snapshot.
const (
	workspaceMigrationMarkerRoot = ".workspace-migrations"
	workspaceMigrationVersion    = 1
	workspaceMigrationStage      = ".legacy-migrating"
)

type workspaceMigrationMarker struct {
	Version int `json:"version"`
	// ProjectUID binds both halves of the legacy transition to one project
	// incarnation. The per-half fields remain for explaining/completing a
	// partially migrated marker written by an older build.
	ProjectUID          string `json:"projectUID,omitempty"`
	WorkspaceProjectUID string `json:"workspaceProjectUID,omitempty"`
	SnapshotsProjectUID string `json:"snapshotsProjectUID,omitempty"`
}

// migrateLegacyWorkspace performs the source-tree half of the compatibility
// transition. The first ProjectUID observed for a legacy project receives the
// old files; subsequent UIDs never read that legacy tree. A staging rename
// keeps an interrupted migration resumable without exposing a mixed tree.
func (s *FileStore) migrateLegacyWorkspace(scope Scope) error {
	return s.migrateLegacyTree(scope, false)
}

// migrateLegacySnapshots performs the snapshot/source-state half of the
// compatibility transition. It is independent from source-tree migration so
// a read-only snapshot operation after a source operation still sees legacy
// assistant snapshots.
func (s *FileStore) migrateLegacySnapshots(scope Scope) error {
	return s.migrateLegacyTree(scope, true)
}

func (s *FileStore) migrateLegacyTree(scope Scope, snapshots bool) error {
	if s == nil || strings.TrimSpace(s.root) == "" {
		return errors.New("project workspace store is not configured")
	}
	if err := validateScopeSegment(scope.OrgUUID); err != nil {
		return err
	}
	if err := validateScopeSegment(scope.WorkspaceUUID); err != nil {
		return err
	}
	if err := validateScopeSegment(scope.ProjectName); err != nil {
		return err
	}
	if err := validateScopeSegment(scope.ProjectUID); err != nil {
		return err
	}

	s.migrationMu.Lock()
	defer s.migrationMu.Unlock()

	markerPath, err := s.migrationMarkerPath(scope)
	if err != nil {
		return err
	}
	marker, err := readWorkspaceMigrationMarker(markerPath)
	if err != nil {
		return err
	}
	if marker.Version == workspaceMigrationVersion && marker.done(snapshots) {
		return nil
	}
	// Whichever half is observed first establishes the legacy project's UID.
	// If the other half is requested later with a recreated project's UID, it
	// must still migrate into the original incarnation rather than inheriting
	// stale data. Markers from the first implementation have no ProjectUID;
	// derive it deterministically from the first populated half.
	boundUID := marker.boundUID()
	if boundUID == "" {
		boundUID = scope.ProjectUID
	}
	if err := validateScopeSegment(boundUID); err != nil {
		return fmt.Errorf("invalid workspace migration project UID: %w", err)
	}
	migrationScope := scope
	migrationScope.ProjectUID = boundUID

	legacy, target, stage := s.legacyAndCurrentPaths(migrationScope, snapshots)
	legacyInfo, legacyErr := os.Lstat(legacy)
	if legacyErr != nil && !errors.Is(legacyErr, fs.ErrNotExist) {
		return fmt.Errorf("stat legacy workspace path %q: %w", legacy, legacyErr)
	}
	targetInfo, targetErr := os.Lstat(target)
	if targetErr != nil && !errors.Is(targetErr, fs.ErrNotExist) {
		return fmt.Errorf("stat current workspace path %q: %w", target, targetErr)
	}
	stageInfo, stageErr := os.Lstat(stage)
	if stageErr != nil && !errors.Is(stageErr, fs.ErrNotExist) {
		return fmt.Errorf("stat workspace migration staging path %q: %w", stage, stageErr)
	}

	switch {
	case stageErr == nil:
		// A previous process renamed the legacy tree but did not finish moving
		// its entries. Resume that operation into the same deterministic UID.
		if !stageInfo.IsDir() {
			return fmt.Errorf("workspace migration staging path %q is not a directory", stage)
		}
		if targetErr != nil {
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return fmt.Errorf("create workspace migration target: %w", err)
			}
			if err := os.Mkdir(target, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("create workspace migration target %q: %w", target, err)
			}
		}
		if err := moveWorkspaceEntries(stage, target); err != nil {
			return err
		}
		if err := os.Remove(stage); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove completed workspace migration staging path: %w", err)
		}

	case targetErr == nil:
		// A current UID directory already exists. Treat it as an explicit new
		// incarnation and never merge legacy files into it; this is the key
		// stale-data isolation rule during upgrades/recreation.
		if !targetInfo.IsDir() {
			return fmt.Errorf("current workspace path %q is not a directory", target)
		}

	case legacyErr == nil:
		if !legacyInfo.IsDir() {
			return fmt.Errorf("legacy workspace path %q is not a directory", legacy)
		}
		// Rename the whole legacy tree out of the way before creating the
		// UID directory. This is atomic on one filesystem and leaves a
		// resumable stage if the process stops while moving entries.
		if err := os.Rename(legacy, stage); err != nil {
			return fmt.Errorf("stage legacy workspace path %q: %w", legacy, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return fmt.Errorf("create workspace migration parent: %w", err)
		}
		if err := os.Mkdir(target, 0o700); err != nil {
			return fmt.Errorf("create workspace migration target %q: %w", target, err)
		}
		if err := moveWorkspaceEntries(stage, target); err != nil {
			return err
		}
		if err := os.Remove(stage); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove completed workspace migration staging path: %w", err)
		}

	default:
		// No legacy data exists. Still record the transition so legacy files
		// written later by an old process cannot leak into a recreated UID.
	}

	marker.Version = workspaceMigrationVersion
	marker.ProjectUID = boundUID
	if snapshots {
		marker.SnapshotsProjectUID = boundUID
	} else {
		marker.WorkspaceProjectUID = boundUID
	}
	if err := writeWorkspaceMigrationMarker(markerPath, marker); err != nil {
		return err
	}
	return nil
}

func (m workspaceMigrationMarker) done(snapshots bool) bool {
	if m.Version != workspaceMigrationVersion {
		return false
	}
	boundUID := m.boundUID()
	if boundUID == "" {
		return false
	}
	if snapshots {
		return strings.TrimSpace(m.SnapshotsProjectUID) == boundUID
	}
	return strings.TrimSpace(m.WorkspaceProjectUID) == boundUID
}

func (m workspaceMigrationMarker) boundUID() string {
	for _, candidate := range []string{m.ProjectUID, m.WorkspaceProjectUID, m.SnapshotsProjectUID} {
		if candidate = strings.TrimSpace(candidate); candidate != "" {
			return candidate
		}
	}
	return ""
}

func (s *FileStore) legacyAndCurrentPaths(scope Scope, snapshots bool) (legacy, target, stage string) {
	base := filepath.Join(s.root, scope.OrgUUID, scope.WorkspaceUUID, scope.ProjectName)
	if snapshots {
		base = filepath.Join(s.root, workspaceSnapshotDirectory, scope.OrgUUID, scope.WorkspaceUUID, scope.ProjectName)
	}
	return base, filepath.Join(base, scope.ProjectUID), filepath.Join(filepath.Dir(base), scope.ProjectName+workspaceMigrationStage)
}

func (s *FileStore) migrationMarkerPath(scope Scope) (string, error) {
	for _, part := range []string{scope.OrgUUID, scope.WorkspaceUUID, scope.ProjectName} {
		if err := validateScopeSegment(part); err != nil {
			return "", err
		}
	}
	return filepath.Join(s.root, workspaceMigrationMarkerRoot, scope.OrgUUID, scope.WorkspaceUUID, scope.ProjectName+".json"), nil
}

func readWorkspaceMigrationMarker(path string) (workspaceMigrationMarker, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return workspaceMigrationMarker{}, nil
	}
	if err != nil {
		return workspaceMigrationMarker{}, fmt.Errorf("read workspace migration marker: %w", err)
	}
	var marker workspaceMigrationMarker
	if err := json.Unmarshal(raw, &marker); err != nil {
		return workspaceMigrationMarker{}, fmt.Errorf("decode workspace migration marker: %w", err)
	}
	if marker.Version != workspaceMigrationVersion {
		return workspaceMigrationMarker{}, fmt.Errorf("unsupported workspace migration marker version %d", marker.Version)
	}
	return marker, nil
}

func writeWorkspaceMigrationMarker(path string, marker workspaceMigrationMarker) error {
	raw, err := json.Marshal(marker)
	if err != nil {
		return fmt.Errorf("encode workspace migration marker: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create workspace migration marker directory: %w", err)
	}
	if err := writeFileAtomically(dir, path, raw, 0o600, false); err != nil {
		return fmt.Errorf("persist workspace migration marker: %w", err)
	}
	return nil
}

func moveWorkspaceEntries(stage, target string) error {
	entries, err := os.ReadDir(stage)
	if err != nil {
		return fmt.Errorf("read workspace migration staging path: %w", err)
	}
	for _, entry := range entries {
		from := filepath.Join(stage, entry.Name())
		to := filepath.Join(target, entry.Name())
		if _, err := os.Lstat(to); err == nil {
			return fmt.Errorf("workspace migration target already contains %q", entry.Name())
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("stat workspace migration target entry %q: %w", entry.Name(), err)
		}
		if err := os.Rename(from, to); err != nil {
			return fmt.Errorf("move workspace migration entry %q: %w", entry.Name(), err)
		}
	}
	return nil
}
