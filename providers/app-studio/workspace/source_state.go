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
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

const workspaceSourceStateFile = "source-state.json"

type workspaceSourceState struct {
	UncommittedPaths []string `json:"uncommittedPaths"`
}

// UncommittedPaths returns the project source paths changed by App Studio
// since the last successful repository commit. The state follows the
// ProjectUID-scoped workspace rather than an individual assistant run.
func (s *FileStore) UncommittedPaths(ctx context.Context, scope Scope) ([]string, error) {
	if s == nil {
		return nil, errors.New("project workspace store is not configured")
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	return s.uncommittedPaths(ctx, scope)
}

// AddUncommittedPaths durably unions changed source paths into the current
// project incarnation's pending repository commit set.
func (s *FileStore) AddUncommittedPaths(ctx context.Context, scope Scope, paths []string) ([]string, error) {
	if s == nil {
		return nil, errors.New("project workspace store is not configured")
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	current, err := s.uncommittedPaths(ctx, scope)
	if err != nil {
		return nil, err
	}
	pathSet := make(map[string]struct{}, len(current)+len(paths))
	for _, path := range current {
		pathSet[path] = struct{}{}
	}
	for _, raw := range paths {
		clean, err := cleanProjectPath(raw)
		if err != nil {
			return nil, err
		}
		pathSet[clean] = struct{}{}
	}
	merged := sortedWorkspaceSourcePaths(pathSet)
	if len(merged) == 0 {
		return nil, nil
	}
	dir, statePath, err := s.sourceStatePath(scope)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create workspace source state directory: %w", err)
	}
	raw, err := json.Marshal(workspaceSourceState{UncommittedPaths: merged})
	if err != nil {
		return nil, fmt.Errorf("encode workspace source state: %w", err)
	}
	if err := writeFileAtomically(dir, statePath, raw, 0o600, false); err != nil {
		return nil, fmt.Errorf("persist workspace source state: %w", err)
	}
	return merged, nil
}

// ClearUncommittedPaths removes the pending source set after the complete set
// has been committed successfully through the repository bridge.
func (s *FileStore) ClearUncommittedPaths(ctx context.Context, scope Scope) error {
	if s == nil {
		return errors.New("project workspace store is not configured")
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	_, statePath, err := s.sourceStatePath(scope)
	if err != nil {
		return err
	}
	if err := os.Remove(statePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("clear workspace source state: %w", err)
	}
	return nil
}

// RemoveUncommittedPaths removes only the paths successfully persisted by a
// repository commit. Other durable dirty paths remain available to later turns.
func (s *FileStore) RemoveUncommittedPaths(ctx context.Context, scope Scope, paths []string) error {
	if s == nil {
		return errors.New("project workspace store is not configured")
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	current, err := s.uncommittedPaths(ctx, scope)
	if err != nil {
		return err
	}
	remove := make(map[string]struct{}, len(paths))
	for _, raw := range paths {
		clean, err := cleanProjectPath(raw)
		if err != nil {
			return err
		}
		remove[clean] = struct{}{}
	}
	remaining := make(map[string]struct{}, len(current))
	for _, path := range current {
		if _, ok := remove[path]; !ok {
			remaining[path] = struct{}{}
		}
	}
	if len(remaining) == 0 {
		_, statePath, err := s.sourceStatePath(scope)
		if err != nil {
			return err
		}
		if err := os.Remove(statePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("clear workspace source state: %w", err)
		}
		return nil
	}
	dir, statePath, err := s.sourceStatePath(scope)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(workspaceSourceState{UncommittedPaths: sortedWorkspaceSourcePaths(remaining)})
	if err != nil {
		return fmt.Errorf("encode workspace source state: %w", err)
	}
	if err := writeFileAtomically(dir, statePath, raw, 0o600, false); err != nil {
		return fmt.Errorf("persist workspace source state: %w", err)
	}
	return nil
}

func (s *FileStore) uncommittedPaths(ctx context.Context, scope Scope) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	_, statePath, err := s.sourceStatePath(scope)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(statePath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read workspace source state: %w", err)
	}
	var state workspaceSourceState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("decode workspace source state: %w", err)
	}
	pathSet := make(map[string]struct{}, len(state.UncommittedPaths))
	for _, rawPath := range state.UncommittedPaths {
		clean, err := cleanProjectPath(rawPath)
		if err != nil {
			return nil, fmt.Errorf("invalid workspace source state: %w", err)
		}
		pathSet[clean] = struct{}{}
	}
	return sortedWorkspaceSourcePaths(pathSet), nil
}

func (s *FileStore) sourceStatePath(scope Scope) (string, string, error) {
	dir, err := s.snapshotProjectDir(scope)
	if err != nil {
		return "", "", err
	}
	return dir, filepath.Join(dir, workspaceSourceStateFile), nil
}

func sortedWorkspaceSourcePaths(pathSet map[string]struct{}) []string {
	paths := make([]string, 0, len(pathSet))
	for path := range pathSet {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}
