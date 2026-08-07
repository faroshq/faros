// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// Package actions exposes Databricks' provider-action HTTP contract. It is
// deliberately independent of the MCP server: the hub forwards an
// authorized, versioned action directly to this package's endpoint.
package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"

	"github.com/faroshq/provider-databricks/queryapi"
)

const (
	PathQueryTableV1 = "/actions/query_table/v1"

	resourceAPIVersion = "databricks.kedge.faros.sh/v1alpha1"
	resourceKind       = "Table"
	resourceName       = "tables"
	maxRequestBytes    = 1 << 20
	defaultActionLimit = 100
	maxActionDeadline  = 90 * time.Second
)

const (
	// Provider action error codes are a deliberately small, provider-neutral
	// surface. The hub allowlists the same values before it exposes a failure
	// to callers; provider-specific internals must never become wire codes.
	ActionErrorCodeUnauthenticated         = "unauthenticated"
	ActionErrorCodeTenantRequired          = "tenant_required"
	ActionErrorCodeActionNotFound          = "action_not_found"
	ActionErrorCodeInvalidRequest          = "invalid_request"
	ActionErrorCodeInvalidDeadline         = "invalid_deadline"
	ActionErrorCodeActionUnavailable       = "action_unavailable"
	ActionErrorCodeActionTimeout           = "action_timeout"
	ActionErrorCodeActionFailed            = "action_failed"
	ActionErrorCodeResourceNotFound        = "resource_not_found"
	ActionErrorCodeResourceForbidden       = "resource_forbidden"
	ActionErrorCodeResourceNotReady        = "resource_not_ready"
	ActionErrorCodeSchemaProjectionInvalid = "schema_projection_invalid"
	ActionErrorCodeBackendFailure          = "backend_failure"
)

const (
	defaultActionFailureMessage = "databricks action failed"
	maxActionErrorMessageBytes  = 512
)

// ActionError is the typed, provider-to-hub failure contract. Status is an
// HTTP transport detail and is intentionally not serialized in the body; the
// hub preserves it only for an allowlisted typed error. Retryable is explicit
// and must never be inferred from Status by either side of the boundary.
//
// Cause is for provider-side unwrapping/logging only. It is never sent to a
// caller and must not be used as a public message without sanitization.
type ActionError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Status    int    `json:"-"`
	Cause     error  `json:"-"`
}

// ProviderActionError is retained as a descriptive alias for consumers that
// name the wire contract directly.
type ProviderActionError = ActionError

func (e *ActionError) Error() string {
	if e == nil {
		return defaultActionFailureMessage
	}
	if message := strings.TrimSpace(e.Message); message != "" {
		return message
	}
	if code := strings.TrimSpace(e.Code); code != "" {
		return code
	}
	return defaultActionFailureMessage
}

func (e *ActionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// NewActionError creates an explicit typed failure. Callers should use one of
// the ActionErrorCode constants and a safe, bounded message.
func NewActionError(code, message string, status int, retryable bool) *ActionError {
	return &ActionError{Code: code, Message: message, Status: status, Retryable: retryable}
}

// NewProviderActionError is an explicit-name alias for NewActionError.
func NewProviderActionError(code, message string, status int, retryable bool) *ProviderActionError {
	return NewActionError(code, message, status, retryable)
}

// ResourceRef is the provider-neutral resource identity selected by the hub
// catalog and carried to the provider action endpoint.
type ResourceRef struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Resource   string `json:"resource"`
	Name       string `json:"name"`
}

// QueryInput is the only caller-controlled portion of query_table/v1. The
// tableRef and action version are injected from the hub route and resource.
type QueryInput struct {
	Columns []string `json:"columns,omitempty"`
	Limit   int      `json:"limit,omitempty"`
}

// QueryExecutor is implemented by the tenant-scoped direct executor. It
// resolves the bound Table and its provider-owned dependencies as the caller,
// then invokes the provider backend without creating a query resource.
type QueryExecutor interface {
	QueryTable(context.Context, ResourceRef, QueryInput) (queryapi.QueryTableResult, error)
}

// Deps configures the published action endpoint.
type Deps struct {
	QueryExecutorFromRequest func(*http.Request) QueryExecutor
	Logger                   logr.Logger
}

// NewHandler returns the provider action handler. A nil per-request executor
// fails closed with 503; the endpoint never falls back to MCP or another
// resource-backed query path.
func NewHandler(deps Deps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		if !hasBearer(r.Header.Get("Authorization")) {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "a bearer credential is required")
			return
		}
		if !validTenantPath(r.Header.Get("X-Kedge-Tenant")) || !validClusterID(r.Header.Get("X-Kedge-Cluster")) {
			writeError(w, http.StatusForbidden, "tenant_required", "a resolved tenant workspace is required")
			return
		}
		if r.URL.Path != PathQueryTableV1 {
			writeError(w, http.StatusNotFound, "action_not_found", "action endpoint not found")
			return
		}
		input, ref, err := decodeRequest(w, r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		executor := QueryExecutor(nil)
		if deps.QueryExecutorFromRequest != nil {
			executor = deps.QueryExecutorFromRequest(r)
		}
		if executor == nil {
			writeError(w, http.StatusServiceUnavailable, "action_unavailable", "databricks action executor is unavailable")
			return
		}

		deadline, err := actionDeadline(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_deadline", err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), deadline)
		defer cancel()
		result, err := executor.QueryTable(ctx, ref, input)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				writeError(w, http.StatusGatewayTimeout, ActionErrorCodeActionTimeout, "databricks action timed out")
				return
			}
			failure := normalizeActionError(err)
			logActionFailure(deps.Logger, requestID, started, failure.Code, classifyActionError(err))
			writeFailure(w, failure)
			return
		}
		// Keep the result identity server-owned even if an executor returns a
		// backend placeholder. The hub-injected resourceRef is authoritative.
		result.ActionVersion = queryapi.ActionVersionV1
		result.TableRef = ref.Name
		writeJSON(w, http.StatusOK, result)
	})
}

func logActionFailure(logger logr.Logger, requestID string, started time.Time, code, class string) {
	if logger.GetSink() == nil {
		return
	}
	logger.Info("databricks provider action failed", "requestID", requestID, "action", "query_table/v1", "outcome", "error", "code", code, "errorClass", class, "durationMs", time.Since(started).Milliseconds())
}

func classifyActionError(err error) string {
	if err == nil {
		return "unknown"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "not found"):
		return "not_found"
	case strings.Contains(message, "forbidden"), strings.Contains(message, "not allowed"), strings.Contains(message, "unauthorized"):
		return "forbidden"
	case strings.Contains(message, "ready"), strings.Contains(message, "validated"):
		return "dependency_not_ready"
	default:
		return "backend_failure"
	}
}

func normalizeActionError(err error) *ActionError {
	if err == nil {
		return &ActionError{Code: ActionErrorCodeActionFailed, Message: defaultActionFailureMessage, Status: http.StatusBadGateway}
	}
	var typed *ActionError
	if errors.As(err, &typed) && typed != nil {
		code := strings.TrimSpace(typed.Code)
		rawMessage := strings.TrimSpace(typed.Message)
		message := safeActionErrorMessage(rawMessage)
		if !isKnownActionErrorCode(code) || (rawMessage != "" && rawMessage != defaultActionFailureMessage && message == defaultActionFailureMessage) {
			return &ActionError{Code: ActionErrorCodeActionFailed, Message: defaultActionFailureMessage, Status: http.StatusBadGateway}
		}
		status := typed.Status
		if status == 0 {
			status = defaultActionErrorStatus(code, typed.Retryable)
		}
		if !actionErrorStatusAllowed(code, status) {
			if code == ActionErrorCodeBackendFailure || code == ActionErrorCodeActionFailed {
				status = gatewayFailureStatus(typed.Retryable)
			} else {
				return &ActionError{Code: ActionErrorCodeActionFailed, Message: defaultActionFailureMessage, Status: http.StatusBadGateway}
			}
		}
		return &ActionError{Code: code, Message: message, Retryable: typed.Retryable, Status: status, Cause: typed.Cause}
	}
	return &ActionError{
		Code:    ActionErrorCodeActionFailed,
		Message: safeActionError(err),
		Status:  http.StatusBadGateway,
	}
}

func isKnownActionErrorCode(code string) bool {
	switch code {
	case ActionErrorCodeUnauthenticated,
		ActionErrorCodeTenantRequired,
		ActionErrorCodeActionNotFound,
		ActionErrorCodeInvalidRequest,
		ActionErrorCodeInvalidDeadline,
		ActionErrorCodeActionUnavailable,
		ActionErrorCodeActionTimeout,
		ActionErrorCodeActionFailed,
		ActionErrorCodeResourceNotFound,
		ActionErrorCodeResourceForbidden,
		ActionErrorCodeResourceNotReady,
		ActionErrorCodeSchemaProjectionInvalid,
		ActionErrorCodeBackendFailure:
		return true
	default:
		return false
	}
}

func actionErrorStatusAllowed(code string, status int) bool {
	if status < http.StatusBadRequest || status > 599 {
		return false
	}
	switch code {
	case ActionErrorCodeUnauthenticated:
		return status == http.StatusUnauthorized
	case ActionErrorCodeTenantRequired, ActionErrorCodeResourceForbidden:
		return status == http.StatusForbidden
	case ActionErrorCodeActionNotFound, ActionErrorCodeResourceNotFound:
		return status == http.StatusNotFound
	case ActionErrorCodeInvalidRequest, ActionErrorCodeInvalidDeadline, ActionErrorCodeSchemaProjectionInvalid:
		return status == http.StatusBadRequest || status == http.StatusUnprocessableEntity
	case ActionErrorCodeActionUnavailable:
		return status == http.StatusServiceUnavailable
	case ActionErrorCodeActionTimeout:
		return status == http.StatusGatewayTimeout || status == http.StatusServiceUnavailable
	case ActionErrorCodeResourceNotReady:
		return status == http.StatusConflict || status == http.StatusServiceUnavailable
	case ActionErrorCodeBackendFailure, ActionErrorCodeActionFailed:
		return status == http.StatusBadGateway || status == http.StatusServiceUnavailable
	default:
		return false
	}
}

func gatewayFailureStatus(retryable bool) int {
	if retryable {
		return http.StatusServiceUnavailable
	}
	return http.StatusBadGateway
}

func defaultActionErrorStatus(code string, retryable bool) int {
	switch code {
	case ActionErrorCodeUnauthenticated:
		return http.StatusUnauthorized
	case ActionErrorCodeTenantRequired, ActionErrorCodeResourceForbidden:
		return http.StatusForbidden
	case ActionErrorCodeActionNotFound, ActionErrorCodeResourceNotFound:
		return http.StatusNotFound
	case ActionErrorCodeInvalidRequest, ActionErrorCodeInvalidDeadline, ActionErrorCodeSchemaProjectionInvalid:
		return http.StatusBadRequest
	case ActionErrorCodeActionUnavailable:
		return http.StatusServiceUnavailable
	case ActionErrorCodeActionTimeout:
		return http.StatusGatewayTimeout
	case ActionErrorCodeResourceNotReady:
		if retryable {
			return http.StatusServiceUnavailable
		}
		return http.StatusConflict
	case ActionErrorCodeBackendFailure, ActionErrorCodeActionFailed:
		return gatewayFailureStatus(retryable)
	default:
		return http.StatusBadGateway
	}
}

func validTenantPath(path string) bool {
	parts := strings.Split(strings.TrimSpace(path), ":")
	return len(parts) == 5 && parts[0] == "root" && parts[1] == "kedge" && parts[2] == "tenants" && parts[3] != "" && parts[4] != ""
}

func validClusterID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && value != "." && value != ".." && !strings.ContainsAny(value, "/\\:#?")
}

func decodeRequest(w http.ResponseWriter, r *http.Request) (QueryInput, ResourceRef, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	var wire struct {
		ResourceRef *ResourceRef    `json:"resourceRef"`
		Input       json.RawMessage `json:"input"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, maxRequestBytes+1))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		return QueryInput{}, ResourceRef{}, fmt.Errorf("decode request: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return QueryInput{}, ResourceRef{}, fmt.Errorf("request must contain exactly one JSON value")
		}
		return QueryInput{}, ResourceRef{}, fmt.Errorf("decode trailing request data: %w", err)
	}
	if wire.ResourceRef == nil {
		return QueryInput{}, ResourceRef{}, fmt.Errorf("resourceRef is required")
	}
	if err := validateResourceRef(*wire.ResourceRef); err != nil {
		return QueryInput{}, ResourceRef{}, err
	}
	if len(wire.Input) == 0 || string(wire.Input) == "null" {
		wire.Input = json.RawMessage(`{}`)
	}
	var input QueryInput
	decInput := json.NewDecoder(strings.NewReader(string(wire.Input)))
	decInput.DisallowUnknownFields()
	if err := decInput.Decode(&input); err != nil {
		return QueryInput{}, ResourceRef{}, fmt.Errorf("decode input: %w", err)
	}
	if err := decInput.Decode(&trailing); err != io.EOF {
		if err == nil {
			return QueryInput{}, ResourceRef{}, fmt.Errorf("input must contain exactly one JSON value")
		}
		return QueryInput{}, ResourceRef{}, fmt.Errorf("decode trailing input data: %w", err)
	}
	if input.Limit == 0 {
		input.Limit = defaultActionLimit
	}
	if input.Limit < 1 || input.Limit > queryapi.MaxQueryLimit {
		return QueryInput{}, ResourceRef{}, fmt.Errorf("input.limit must be between 1 and %d", queryapi.MaxQueryLimit)
	}
	if len(input.Columns) > queryapi.MaxQueryColumns {
		return QueryInput{}, ResourceRef{}, fmt.Errorf("input.columns must contain at most %d entries", queryapi.MaxQueryColumns)
	}
	return input, *wire.ResourceRef, nil
}

func validateResourceRef(ref ResourceRef) error {
	if strings.TrimSpace(ref.APIVersion) != resourceAPIVersion ||
		strings.TrimSpace(ref.Kind) != resourceKind ||
		strings.TrimSpace(ref.Resource) != resourceName ||
		strings.TrimSpace(ref.Name) == "" {
		return fmt.Errorf("resourceRef must identify an imported Databricks Table")
	}
	if err := queryapi.ValidateTableRef(ref.Name); err != nil {
		return err
	}
	return nil
}

func actionDeadline(r *http.Request) (time.Duration, error) {
	value := strings.TrimSpace(r.Header.Get("X-Kedge-Action-Deadline-Ms"))
	if value == "" {
		return maxActionDeadline, nil
	}
	millis, err := strconv.ParseInt(value, 10, 64)
	if err != nil || millis < 1 {
		return 0, fmt.Errorf("X-Kedge-Action-Deadline-Ms must be a positive integer")
	}
	if millis >= maxActionDeadline.Milliseconds() {
		return maxActionDeadline, nil
	}
	return time.Duration(millis) * time.Millisecond, nil
}

func hasBearer(value string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "bearer ") && strings.TrimSpace(strings.TrimSpace(value)[7:]) != ""
}

func safeActionError(err error) string {
	if err == nil {
		return defaultActionFailureMessage
	}
	message := strings.TrimSpace(err.Error())
	return safeActionErrorMessage(message)
}

func safeActionErrorMessage(message string) string {
	message = strings.TrimSpace(message)
	lower := strings.ToLower(message)
	for _, marker := range []string{
		"bearer", "token", "secret", "password", "authorization", "credential",
		"root:kedge:tenants:", "/clusters/", "http://", "https://", "://",
	} {
		if strings.Contains(lower, marker) {
			return defaultActionFailureMessage
		}
	}
	for _, r := range message {
		if r < 0x20 || r == 0x7f {
			return defaultActionFailureMessage
		}
	}
	// SQL text and provider target details are never safe to surface. Keep the
	// check intentionally conservative: all normal action diagnostics are
	// short status/readiness messages and do not contain SQL verbs.
	for _, marker := range []string{"select ", "insert ", "update ", "delete ", "drop ", "alter ", "create ", "merge "} {
		if strings.Contains(lower, marker) {
			return defaultActionFailureMessage
		}
	}
	if message == "" {
		return defaultActionFailureMessage
	}
	if len(message) > maxActionErrorMessageBytes {
		return defaultActionFailureMessage
	}
	return message
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	message = safeActionErrorMessage(message)
	writeJSON(w, status, map[string]any{"error": map[string]any{
		"code": code, "message": message, "retryable": defaultRetryable(code),
	}})
}

func writeFailure(w http.ResponseWriter, failure *ActionError) {
	if failure == nil {
		failure = &ActionError{Code: ActionErrorCodeActionFailed, Message: defaultActionFailureMessage, Status: http.StatusBadGateway}
	}
	code := strings.TrimSpace(failure.Code)
	if !isKnownActionErrorCode(code) {
		code = ActionErrorCodeActionFailed
	}
	message := safeActionErrorMessage(failure.Message)
	if message == defaultActionFailureMessage && code != ActionErrorCodeActionFailed {
		code = ActionErrorCodeActionFailed
	}
	status := failure.Status
	if status == 0 {
		status = defaultActionErrorStatus(code, failure.Retryable)
	}
	if !actionErrorStatusAllowed(code, status) {
		if code == ActionErrorCodeBackendFailure || code == ActionErrorCodeActionFailed {
			status = gatewayFailureStatus(failure.Retryable)
		} else {
			code = ActionErrorCodeActionFailed
			message = defaultActionFailureMessage
			status = http.StatusBadGateway
			failure.Retryable = false
		}
	}
	writeJSON(w, status, map[string]any{"error": map[string]any{
		"code": code, "message": message, "retryable": failure.Retryable,
	}})
}

func defaultRetryable(code string) bool {
	switch code {
	case ActionErrorCodeActionUnavailable, ActionErrorCodeActionTimeout:
		return true
	default:
		return false
	}
}
