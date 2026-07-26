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
	"testing"

	"github.com/faroshq/provider-app-studio/store"
)

func TestProjectAssistantInitialCreationPlanCannotPersistAcrossTurns(t *testing.T) {
	server := &Server{store: store.NewMemoryStore()}
	scope := store.Scope{OrgUUID: "org-a", WorkspaceUUID: "ws-a", ProjectName: "demo"}
	initial := projectAssistantInitialCreationPlan()
	merged := mergeProjectAssistantApprovedPlans(initial, projectAssistantApprovedPlan{
		Summary:    "Model restated the initial plan",
		Operations: []string{projectToolWriteFile},
	})

	if err := server.saveProjectAssistantApprovedPlan(context.Background(), scope, &merged); err != nil {
		t.Fatalf("saveProjectAssistantApprovedPlan returned error: %v", err)
	}
	if got := server.loadProjectAssistantApprovedPlan(context.Background(), scope); got != nil {
		t.Fatalf("persisted run-local initial plan = %#v, want nil", got)
	}
}
