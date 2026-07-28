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

import "testing"

func TestCreateProjectMessageAssistantActionDefaultsToAsk(t *testing.T) {
	action, err := (CreateProjectMessageRequest{}).assistantAction()
	if err != nil {
		t.Fatalf("assistantAction: %v", err)
	}
	if action != projectAssistantActionAsk {
		t.Fatalf("action = %q, want ask", action)
	}
}

func TestCreateProjectMessageAssistantActionValidatesBuildAndContinue(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  CreateProjectMessageRequest
		want projectAssistantAction
		ok   bool
	}{
		{name: "build", req: CreateProjectMessageRequest{AssistantAction: "build"}, want: projectAssistantActionBuild, ok: true},
		{name: "continue", req: CreateProjectMessageRequest{AssistantAction: "continue", WorkItemID: "wi-1", WorkItemRevision: 2}, want: projectAssistantActionContinue, ok: true},
		{name: "continue requires item", req: CreateProjectMessageRequest{AssistantAction: "continue"}},
		{name: "build rejects selection", req: CreateProjectMessageRequest{AssistantAction: "build", WorkItemID: "wi-1", WorkItemRevision: 1}},
		{name: "unknown", req: CreateProjectMessageRequest{AssistantAction: "mutate"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.req.assistantAction()
			if tc.ok {
				if err != nil || got != tc.want {
					t.Fatalf("assistantAction = %q, %v; want %q, nil", got, err, tc.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("assistantAction = %q, nil; want validation error", got)
			}
		})
	}
}
