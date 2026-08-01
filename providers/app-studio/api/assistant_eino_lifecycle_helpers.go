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
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
)

const (
	projectEinoAssistantWriteTodosTool = "write_todos"
	projectEinoAssistantToolSearchTool = "tool_search"

	projectEinoAssistantTodoProgressMaxItems      = 50
	projectEinoAssistantTodoProgressMaxInputBytes = 64 * 1024
	projectEinoAssistantTodoProgressMaxLabelBytes = 120

	projectEinoAssistantRepeatedActionWarnAt = 2
	projectEinoAssistantRepeatedActionLimit  = maxAssistantDeepIterations
)

type projectEinoAssistantNoProgressError struct {
	ToolName         string
	Calls            int
	Limit            int
	SourceRevision   uint64
	VerifiedRevision uint64
}

func (e *projectEinoAssistantNoProgressError) Error() string {
	if e == nil {
		return errProjectAssistantNoProgress.Error()
	}
	if toolName := projectToolBaseName(e.ToolName); toolName != "" {
		return fmt.Sprintf("%s: repeated %s %d times", errProjectAssistantNoProgress, toolName, e.Limit)
	}
	return fmt.Sprintf("%s: completed %d consecutive model turns without new progress", errProjectAssistantNoProgress, e.Limit)
}

func (e *projectEinoAssistantNoProgressError) Unwrap() error {
	return errProjectAssistantNoProgress
}

func projectEinoAssistantProgressApplies(req projectAssistantRunRequest, runState *projectEinoAssistantRunState) bool {
	if runState != nil {
		return projectAssistantTurnProfileAllowsMutation(runState.TurnPolicy().profile)
	}
	profile := req.TurnPolicy.profile
	if strings.TrimSpace(string(profile)) == "" {
		profile = req.TurnProfile
	}
	return projectAssistantTurnProfileAllowsMutation(profile)
}

func projectEinoAssistantSuccessfulToolContent(content string) bool {
	content = strings.ToLower(strings.TrimSpace(content))
	for _, prefix := range []string{
		"tool call failed:",
		"tool call denied:",
		"tool call skipped: waiting for approval",
		"permission denied:",
	} {
		if strings.HasPrefix(content, prefix) {
			return false
		}
	}
	decoded := map[string]any{}
	if err := json.Unmarshal([]byte(content), &decoded); err == nil {
		switch strings.ToLower(projectToolString(decoded["status"])) {
		case "failed", "partial_failure", "error":
			return false
		}
	}
	return true
}

func projectEinoAssistantVerificationContentReady(content string) bool {
	var result projectAssistantRuntimeVerificationResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &result); err != nil {
		return false
	}
	return projectEinoAssistantRuntimeVerificationDisposition(result) == projectEinoAssistantVerificationReadyDisposition
}

func projectEinoAssistantTemplateBootstrapAllowed(project *aiv1alpha1.Project) bool {
	return project != nil && (project.Spec.Template == nil || strings.TrimSpace(project.Spec.Template.Name) == "")
}

func projectEinoAssistantTodoProgressLabel(value string) string {
	value = projectEinoAssistantSafeText(strings.Join(strings.Fields(value), " "))
	if len(value) <= projectEinoAssistantTodoProgressMaxLabelBytes {
		return value
	}
	end := projectEinoAssistantTodoProgressMaxLabelBytes - 3
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return strings.TrimSpace(value[:end]) + "..."
}
