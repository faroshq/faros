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
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// App Studio exposes only the read/discovery portion of the Agents provider's
// configuration surface, plus this deliberately narrow create operation. The
// provider's full MCP surface remains reachable only through its own boundary;
// these limits are an App Studio contract and must not be inferred from the
// provider's broader create/update schemas.
const (
	projectAssistantAgentsCreateDefaultBudgetTokens   int64 = 20_000
	projectAssistantAgentsCreateMaxBudgetTokens       int64 = 100_000
	projectAssistantAgentsCreateDefaultBudgetUSD            = "5.00"
	projectAssistantAgentsCreateMaxBudgetUSD                = 25.00
	projectAssistantAgentsCreateDefaultMaxToolTurns   int64 = 16
	projectAssistantAgentsCreateMaxMaxToolTurns       int64 = 32
	projectAssistantAgentsCreateDefaultTimeoutSeconds int64 = 900
	projectAssistantAgentsCreateMaxTimeoutSeconds     int64 = 3600
	projectAssistantAgentsCreateMaxNameBytes                = 63
	projectAssistantAgentsCreateMaxDisplayNameBytes         = 128
	projectAssistantAgentsCreateMaxDescriptionBytes         = 2048
	projectAssistantAgentsCreateMaxSystemPromptBytes        = 8192
	projectAssistantAgentsCreateMaxCredentialBytes          = 253
)

const projectAssistantAgentsCreateDescription = "Create one bounded Agents provider agent for this App Studio project. First call agents__list_model_credentials and pass an existing modelCredential. App Studio forces autonomy to ask, omits channels, delegates, tool grants, and model fallbacks, and applies server-owned budget and run limits. Project, run, and tool-call provenance is informational only. This action always requires approval, including AutoApprove, and is unavailable in Plan and Review. Agent update/delete and credential, connection, schedule, trigger, and toolset mutations are not exposed."

var projectAssistantAgentsCreateNamePattern = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$`)

var projectAssistantAgentsCreateAgentSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "name": {
      "type": "string",
      "minLength": 1,
      "maxLength": 63,
      "pattern": "^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$",
      "description": "Lowercase agent name."
    },
    "displayName": {
      "type": "string",
      "maxLength": 128,
      "description": "Optional human-readable name."
    },
    "description": {
      "type": "string",
      "maxLength": 2048,
      "description": "Optional short summary of the agent's purpose."
    },
    "systemPrompt": {
      "type": "string",
      "maxLength": 8192,
      "description": "Optional standing instructions for the agent."
    },
    "modelCredential": {
      "type": "string",
      "minLength": 1,
      "maxLength": 253,
      "description": "Required existing credential name from agents__list_model_credentials."
    },
    "budgetTokens": {
      "type": "integer",
      "minimum": 1,
      "maximum": 100000,
      "description": "Optional monthly token cap; App Studio supplies a bounded default."
    },
    "budgetUSD": {
      "type": "string",
      "pattern": "^[0-9]+(?:\\.[0-9]{1,2})?$",
      "description": "Optional monthly USD cap; App Studio supplies a bounded default."
    },
    "maxToolTurns": {
      "type": "integer",
      "minimum": 1,
      "maximum": 32,
      "description": "Optional per-run tool-call limit within the App Studio bound."
    },
    "timeoutSeconds": {
      "type": "integer",
      "minimum": 1,
      "maximum": 3600,
      "description": "Optional per-run wall-clock limit within the App Studio bound."
    }
  },
  "required": ["name", "modelCredential"]
}`)

func projectAssistantAgentsCreateTool(name string) bool {
	return projectAssistantToolKey(name) == projectAssistantToolKey(projectToolAgentsCreateAgent)
}

func projectAssistantAgentsCreateToolSpec(name string) projectAssistantToolSpec {
	return projectAssistantToolSpec{
		Name:        strings.TrimSpace(name),
		Description: projectAssistantAgentsCreateDescription,
		Parameters:  append(json.RawMessage(nil), projectAssistantAgentsCreateAgentSchema...),
		Risk:        projectAssistantToolRiskRuntime,
	}
}

// projectAssistantSanitizeAgentsCreateArguments converts untrusted model input
// into the only create_agent request App Studio is willing to forward. It is
// intentionally an allowlist: unknown fields are dropped, server-owned fields
// are overwritten, and malformed bounded values fail closed.
func projectAssistantSanitizeAgentsCreateArguments(args map[string]any, req projectAssistantToolCallRequest) (map[string]any, error) {
	if args == nil {
		args = map[string]any{}
	}
	name, err := projectAssistantAgentsCreateString(args, "name", projectAssistantAgentsCreateMaxNameBytes, true)
	if err != nil {
		return nil, err
	}
	if !projectAssistantAgentsCreateNamePattern.MatchString(name) {
		return nil, fmt.Errorf("name must be a lowercase DNS label")
	}
	modelCredential, err := projectAssistantAgentsCreateString(args, "modelCredential", projectAssistantAgentsCreateMaxCredentialBytes, true)
	if err != nil {
		return nil, err
	}

	out := map[string]any{
		"name":            name,
		"modelCredential": modelCredential,
		// App Studio owns the safety posture of every agent it creates.
		"autonomy": "ask",
	}
	for _, field := range []struct {
		key string
		max int
	}{
		{key: "displayName", max: projectAssistantAgentsCreateMaxDisplayNameBytes},
		{key: "description", max: projectAssistantAgentsCreateMaxDescriptionBytes},
		{key: "systemPrompt", max: projectAssistantAgentsCreateMaxSystemPromptBytes},
	} {
		value, err := projectAssistantAgentsCreateString(args, field.key, field.max, false)
		if err != nil {
			return nil, err
		}
		if value != "" {
			out[field.key] = value
		}
	}

	tokens, err := projectAssistantAgentsCreateBoundedInteger(args, "budgetTokens", projectAssistantAgentsCreateDefaultBudgetTokens, projectAssistantAgentsCreateMaxBudgetTokens)
	if err != nil {
		return nil, err
	}
	out["budgetTokens"] = tokens
	budgetUSD, err := projectAssistantAgentsCreateBoundedUSD(args, "budgetUSD", projectAssistantAgentsCreateDefaultBudgetUSD, projectAssistantAgentsCreateMaxBudgetUSD)
	if err != nil {
		return nil, err
	}
	out["budgetUSD"] = budgetUSD
	maxToolTurns, err := projectAssistantAgentsCreateBoundedInteger(args, "maxToolTurns", projectAssistantAgentsCreateDefaultMaxToolTurns, projectAssistantAgentsCreateMaxMaxToolTurns)
	if err != nil {
		return nil, err
	}
	out["maxToolTurns"] = maxToolTurns
	timeoutSeconds, err := projectAssistantAgentsCreateBoundedInteger(args, "timeoutSeconds", projectAssistantAgentsCreateDefaultTimeoutSeconds, projectAssistantAgentsCreateMaxTimeoutSeconds)
	if err != nil {
		return nil, err
	}
	out["timeoutSeconds"] = timeoutSeconds
	out["provenance"] = projectAssistantAgentsCreateProvenance(req)
	return out, nil
}

func projectAssistantAgentsCreateProvenance(req projectAssistantToolCallRequest) map[string]string {
	projectName := ""
	projectUID := ""
	if req.Project != nil {
		projectName = strings.TrimSpace(req.Project.Name)
		projectUID = strings.TrimSpace(string(req.Project.UID))
	}
	return map[string]string{
		"source":      "app-studio",
		"projectName": projectName,
		"projectUID":  projectUID,
		"runID":       strings.TrimSpace(req.AssistantRunID),
		"toolCallID":  strings.TrimSpace(req.ToolCallID),
	}
}

func projectAssistantAgentsCreateString(args map[string]any, key string, maxBytes int, required bool) (string, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		if required {
			return "", fmt.Errorf("%s is required", key)
		}
		return "", nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	value = strings.TrimSpace(value)
	if required && value == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	if len([]byte(value)) > maxBytes {
		return "", fmt.Errorf("%s exceeds the %d-byte limit", key, maxBytes)
	}
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("%s must be valid UTF-8", key)
	}
	return value, nil
}

func projectAssistantAgentsCreateBoundedInteger(args map[string]any, key string, fallback, max int64) (int64, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return fallback, nil
	}
	value, ok := projectAssistantAgentsCreateNumber(raw)
	if !ok || math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value {
		return 0, fmt.Errorf("%s must be a finite integer", key)
	}
	if value < 0 {
		return 0, fmt.Errorf("%s must not be negative", key)
	}
	if value == 0 {
		return fallback, nil
	}
	if value > float64(max) {
		return max, nil
	}
	return int64(value), nil
}

func projectAssistantAgentsCreateBoundedUSD(args map[string]any, key, fallback string, max float64) (string, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return fallback, nil
	}
	var value float64
	switch typed := raw.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return fallback, nil
		}
		parsed, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return "", fmt.Errorf("%s must be a decimal USD amount", key)
		}
		value = parsed
	default:
		parsed, ok := projectAssistantAgentsCreateNumber(raw)
		if !ok {
			return "", fmt.Errorf("%s must be a decimal USD amount", key)
		}
		value = parsed
	}
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return "", fmt.Errorf("%s must be a finite non-negative USD amount", key)
	}
	if value == 0 {
		return fallback, nil
	}
	if value > max {
		value = max
	}
	return strconv.FormatFloat(value, 'f', 2, 64), nil
}

func projectAssistantAgentsCreateNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int8:
		return float64(typed), true
	case int16:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint8:
		return float64(typed), true
	case uint16:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	default:
		return 0, false
	}
}
