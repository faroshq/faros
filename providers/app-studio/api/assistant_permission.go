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
	"fmt"
	"strings"

	"github.com/faroshq/provider-app-studio/store"
	"github.com/faroshq/provider-app-studio/workspace"
)

type projectAssistantPermissionDecision string

const (
	projectAssistantPermissionAllow projectAssistantPermissionDecision = "allow"
	projectAssistantPermissionAsk   projectAssistantPermissionDecision = "ask"
	projectAssistantPermissionDeny  projectAssistantPermissionDecision = "deny"
)

func projectAssistantToolHasEffect(spec projectAssistantToolSpec) bool {
	switch spec.Risk {
	case projectAssistantToolRiskPlan, projectAssistantToolRiskWrite, projectAssistantToolRiskRuntime, projectAssistantToolRiskCommit:
		return true
	default:
		return false
	}
}

// projectAssistantPermissionForV2 keeps collaboration mode, approval policy,
// and tool risk independent. Default mode supplies mutation intent; the
// approval preference decides whether an individual effect pauses. Workspace
// and lifecycle validation still run at invocation time.
func projectAssistantPermissionForV2(
	spec projectAssistantToolSpec,
	mode store.AssistantApprovalMode,
	runState *projectEinoAssistantRunState,
	args map[string]any,
	templateBootstrapAllowed bool,
) projectAssistantPermissionDecision {
	switch spec.Risk {
	case projectAssistantToolRiskRead, projectAssistantToolRiskInput:
		return projectAssistantPermissionAllow
	case projectAssistantToolRiskPlan:
		if projectToolBaseName(spec.Name) == projectToolDefineInitialProjectPlan {
			if runState != nil {
				active := runState.ApprovedPlan()
				if active != nil && active.RunLocal {
					return projectAssistantPermissionAllow
				}
			}
		}
		return projectAssistantPermissionDeny
	case projectAssistantToolRiskWrite:
		if runState != nil {
			if active := runState.ApprovedPlan(); active != nil && active.RunLocal {
				if projectAssistantApprovedPlanAllowsWrite(active, spec.Name, args) ||
					(projectToolBaseName(spec.Name) == projectToolSelectTemplate && templateBootstrapAllowed) {
					return projectAssistantPermissionAllow
				}
				return projectAssistantPermissionDeny
			}
		}
		if mode == store.AssistantApprovalModeAutoApprove {
			return projectAssistantPermissionAllow
		}
		return projectAssistantPermissionAsk
	case projectAssistantToolRiskRuntime, projectAssistantToolRiskCommit:
		if mode == store.AssistantApprovalModeAutoApprove {
			return projectAssistantPermissionAllow
		}
		return projectAssistantPermissionAsk
	default:
		return projectAssistantPermissionDeny
	}
}

func projectAssistantApprovedPlanActive(plan *projectAssistantApprovedPlan) bool {
	return plan != nil &&
		plan.Version == projectAssistantApprovedPlanVersionWorkspaceMutation &&
		projectAssistantApprovedPlanHasCapability(plan, projectAssistantCapabilityWorkspaceMutate) &&
		(!plan.AllowAllWrites || plan.RunLocal) &&
		(plan.RunLocal || len(plan.TargetPaths) > 0)
}

func projectAssistantApprovedPlanAllowsWrite(plan *projectAssistantApprovedPlan, toolName string, args map[string]any) bool {
	if !projectAssistantApprovedPlanActive(plan) {
		return false
	}
	toolName = strings.TrimSpace(toolName)
	switch toolName {
	case projectToolApplyPatch:
	default:
		return false
	}
	// Initial project creation is the only unbounded grant. It is derived from
	// the user's create request and remains local to that Eino run.
	if plan.AllowAllWrites {
		_, err := projectAssistantWriteTargetPaths(toolName, args)
		return plan.RunLocal && err == nil
	}
	if !projectAssistantApprovedPlanHasCapability(plan, projectAssistantCapabilityWorkspaceMutate) {
		return false
	}
	targetPaths, err := projectAssistantWriteTargetPaths(toolName, args)
	if err != nil {
		return false
	}
	for _, targetPath := range targetPaths {
		covered := false
		for _, approved := range plan.TargetPaths {
			if projectAssistantPathWithinApprovedTarget(targetPath, approved) {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return len(targetPaths) > 0
}

func projectAssistantApprovedPlanHasCapability(plan *projectAssistantApprovedPlan, capability string) bool {
	if plan == nil {
		return false
	}
	capability = strings.TrimSpace(capability)
	for _, candidate := range plan.Capabilities {
		if strings.TrimSpace(candidate) == capability {
			return true
		}
	}
	return false
}

func projectAssistantPlanCanAuthorizeWriteTool(toolName string) bool {
	switch strings.TrimSpace(toolName) {
	case projectToolApplyPatch:
		return true
	default:
		return false
	}
}

// projectAssistantWriteTargetPaths returns every workspace path a mutation can
// affect. Contextual apply_patch payloads may add, delete, update, or move more
// than one file, so authorization must cover the complete parsed set rather
// than a representative path.
func projectAssistantWriteTargetPaths(toolName string, args map[string]any) ([]string, error) {
	switch strings.TrimSpace(toolName) {
	case projectToolApplyPatch:
		if patch, ok := projectToolRawString(args["patch"]); ok && strings.TrimSpace(patch) != "" {
			paths, err := workspace.PatchPaths(patch)
			if err != nil {
				return nil, err
			}
			if len(paths) == 0 {
				return nil, fmt.Errorf("apply_patch must affect at least one workspace path")
			}
			return paths, nil
		}
		return nil, fmt.Errorf("apply_patch requires a contextual patch payload")
	default:
		return nil, fmt.Errorf("tool %q cannot use workspace mutation grants", toolName)
	}
}

func projectAssistantPathWithinApprovedTarget(candidate, approved string) bool {
	approvedDirectory := strings.HasSuffix(strings.TrimSpace(approved), "/")
	var err error
	candidate, err = projectAssistantCanonicalGrantTarget(candidate, false)
	if err != nil {
		return false
	}
	approved, err = projectAssistantCanonicalGrantTarget(approved, approvedDirectory)
	if err != nil {
		return false
	}
	if approvedDirectory {
		return candidate == strings.TrimSuffix(approved, "/") || strings.HasPrefix(candidate, approved)
	}
	return candidate == approved
}

func projectAssistantCanonicalGrantTarget(value string, directory bool) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	directory = directory || strings.HasSuffix(value, "/")
	clean, err := workspace.CleanProjectPath(value)
	if err != nil {
		return "", err
	}
	if directory {
		return clean + "/", nil
	}
	return clean, nil
}

func projectAssistantCanonicalGrantTargets(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	for _, value := range values {
		clean, err := projectAssistantCanonicalGrantTarget(value, false)
		if err != nil {
			return nil, fmt.Errorf("invalid workspace grant target %q: %w", value, err)
		}
		out = append(out, clean)
	}
	return normalizeProjectAssistantStringList(out), nil
}

func parseProjectAssistantPermissionDecision(value string) (projectAssistantPermissionDecision, error) {
	decision := projectAssistantPermissionDecision(strings.ToLower(strings.TrimSpace(value)))
	switch decision {
	case projectAssistantPermissionAllow, projectAssistantPermissionDeny:
		return decision, nil
	default:
		return "", newValidationError("decision must be allow or deny")
	}
}

type projectAssistantPermissionRequiredError struct {
	RunID     string
	RequestID string
	ToolName  string
}

func (e *projectAssistantPermissionRequiredError) Error() string {
	if e == nil {
		return "assistant tool permission required"
	}
	if e.ToolName != "" {
		return fmt.Sprintf("assistant tool %q requires permission", e.ToolName)
	}
	return "assistant tool permission required"
}

type projectAssistantInputRequiredError struct {
	RunID     string
	RequestID string
}

func (e *projectAssistantInputRequiredError) Error() string {
	return "assistant needs follow-up input"
}

func projectAssistantPermissionDeniedToolMessage(tc chatToolCall, reason string) chatMessage {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "permission denied"
	}
	return chatMessage{
		Role:       "tool",
		Name:       tc.Function.Name,
		ToolCallID: tc.ID,
		Content:    "Tool call failed: permission denied: " + reason,
	}
}
