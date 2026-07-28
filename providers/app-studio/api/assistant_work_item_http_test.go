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

package api

import (
	"context"
	"errors"
	"testing"

	"github.com/faroshq/provider-app-studio/store"
)

func TestDurableAskIsActorBoundDiscussionWithoutWorkItem(t *testing.T) {
	messages := store.NewMemoryStore()
	server := NewWithWorkspace(nil, messages, nil, "", false)
	scope := testProjectMessageScope("org-a", "workspace-a", "demo")

	started, err := server.startProjectAssistantRunDurably(
		context.Background(), scope, "alice", "What theme is active?", "ask-1",
		func(store.AssistantRun, store.Message, bool) error { return nil },
	)
	if err != nil {
		t.Fatalf("start Ask: %v", err)
	}
	if started.Run.Mode != store.AssistantRunModeDiscussion || started.Run.WorkItemID != "" {
		t.Fatalf("Ask run = %#v, want unlinked discussion", started.Run)
	}
	if started.User.ActorID != "alice" || started.User.WorkItemID != "" {
		t.Fatalf("Ask user = %#v, want actor-bound unlinked message", started.User)
	}
	items, err := messages.ListAssistantWorkItems(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("Ask created WorkItems: %#v", items)
	}
}

func TestDurableBuildCreatesRootedActorBoundWorkItem(t *testing.T) {
	messages := store.NewMemoryStore()
	server := NewWithWorkspace(nil, messages, nil, "", false)
	scope := testProjectMessageScope("org-a", "workspace-a", "demo")

	started, err := server.startProjectAssistantBuildRunDurably(
		context.Background(), scope, "alice", "Implement dark mode", "build-1",
		func(store.AssistantRun, store.Message, bool) error { return nil },
	)
	if err != nil {
		t.Fatalf("start Build: %v", err)
	}
	if started.Run.Mode != store.AssistantRunModeNew || started.Run.WorkItemID == "" {
		t.Fatalf("Build run = %#v, want new WorkItem run", started.Run)
	}
	item, err := messages.GetAssistantWorkItem(context.Background(), scope, started.Run.WorkItemID)
	if err != nil {
		t.Fatal(err)
	}
	if item.CreatedBy != "alice" || item.RootMessageID != started.User.ID || item.ActiveRunID != started.Run.ID {
		t.Fatalf("WorkItem = %#v, want rooted actor-bound active run", item)
	}
	if started.User.WorkItemID != item.ID || started.Assistant.WorkItemID != item.ID {
		t.Fatalf("messages are not linked to WorkItem %q: user=%#v assistant=%#v", item.ID, started.User, started.Assistant)
	}
}

func TestRecreatedProjectNameCannotSeeOldProjectUIDWorkItem(t *testing.T) {
	messages := store.NewMemoryStore()
	server := NewWithWorkspace(nil, messages, nil, "", false)
	oldScope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo", ProjectUID: "uid-old"}
	newScope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "workspace-a", ProjectName: "demo", ProjectUID: "uid-new"}

	started, err := server.startProjectAssistantBuildRunDurably(
		context.Background(), oldScope, "alice", "Implement quote submission", "build-old",
		func(store.AssistantRun, store.Message, bool) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := messages.GetAssistantWorkItem(context.Background(), newScope, started.Run.WorkItemID); !errors.Is(err, store.ErrAssistantWorkItemNotFound) {
		t.Fatalf("new Project UID loaded old WorkItem: %v", err)
	}
}
