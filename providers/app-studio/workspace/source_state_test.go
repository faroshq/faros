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
	"reflect"
	"testing"
)

func TestFileStoreUncommittedPathsPersistUnionClearAndProjectUIDIsolation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store := NewFileStore(root)
	oldScope := Scope{
		OrgUUID:       "org-a",
		WorkspaceUUID: "ws-1",
		ProjectName:   "demo",
		ProjectUID:    "project-old",
	}
	newScope := oldScope
	newScope.ProjectUID = "project-new"

	got, err := store.AddUncommittedPaths(ctx, oldScope, []string{"src/App.tsx", "package.json"})
	if err != nil {
		t.Fatalf("AddUncommittedPaths initial: %v", err)
	}
	if want := []string{"package.json", "src/App.tsx"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("initial paths = %v, want %v", got, want)
	}
	got, err = store.AddUncommittedPaths(ctx, oldScope, []string{"src/App.tsx", "src/theme.css"})
	if err != nil {
		t.Fatalf("AddUncommittedPaths union: %v", err)
	}
	if want := []string{"package.json", "src/App.tsx", "src/theme.css"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("union paths = %v, want %v", got, want)
	}

	reopened := NewFileStore(root)
	got, err = reopened.UncommittedPaths(ctx, oldScope)
	if err != nil {
		t.Fatalf("UncommittedPaths after reopen: %v", err)
	}
	if want := []string{"package.json", "src/App.tsx", "src/theme.css"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("reopened paths = %v, want %v", got, want)
	}
	if err := reopened.RemoveUncommittedPaths(ctx, oldScope, []string{"src/App.tsx"}); err != nil {
		t.Fatalf("RemoveUncommittedPaths: %v", err)
	}
	got, err = reopened.UncommittedPaths(ctx, oldScope)
	if err != nil {
		t.Fatalf("UncommittedPaths after remove: %v", err)
	}
	if want := []string{"package.json", "src/theme.css"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("paths after remove = %v, want %v", got, want)
	}
	got, err = reopened.UncommittedPaths(ctx, newScope)
	if err != nil {
		t.Fatalf("UncommittedPaths recreated project: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("recreated project inherited paths: %v", got)
	}

	if err := reopened.ClearUncommittedPaths(ctx, oldScope); err != nil {
		t.Fatalf("ClearUncommittedPaths: %v", err)
	}
	got, err = reopened.UncommittedPaths(ctx, oldScope)
	if err != nil {
		t.Fatalf("UncommittedPaths after clear: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("paths after clear = %v, want empty", got)
	}
}
