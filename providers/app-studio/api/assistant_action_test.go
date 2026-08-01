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
	"testing"
)

func TestCreateProjectMessageCollaborationModeDefaultsToDefault(t *testing.T) {
	mode, err := (CreateProjectMessageRequest{}).collaborationMode()
	if err != nil {
		t.Fatalf("collaborationMode: %v", err)
	}
	if mode != projectAssistantCollaborationModeDefault {
		t.Fatalf("mode = %q, want default", mode)
	}
}

func TestCreateProjectMessageCollaborationModeAcceptsOnlyV2Modes(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  CreateProjectMessageRequest
		want projectAssistantCollaborationMode
		ok   bool
	}{
		{name: "default", req: CreateProjectMessageRequest{CollaborationMode: "default"}, want: projectAssistantCollaborationModeDefault, ok: true},
		{name: "plan", req: CreateProjectMessageRequest{CollaborationMode: "plan"}, want: projectAssistantCollaborationModePlan, ok: true},
		{name: "unknown", req: CreateProjectMessageRequest{CollaborationMode: "adaptive"}},
		{name: "legacy action", req: CreateProjectMessageRequest{AssistantAction: "auto"}},
		{name: "legacy work item", req: CreateProjectMessageRequest{WorkItemID: "wi-1"}},
		{name: "legacy work item revision", req: CreateProjectMessageRequest{WorkItemRevision: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.req.collaborationMode()
			if tc.ok {
				if err != nil || got != tc.want {
					t.Fatalf("collaborationMode = %q, %v; want %q, nil", got, err, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("collaborationMode = %q, nil; want validation error", got)
			}
		})
	}
}
