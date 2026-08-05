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
				writeError(w, http.StatusGatewayTimeout, "action_timeout", "databricks action timed out")
				return
			}
			logActionFailure(deps.Logger, requestID, started, "action_failed", classifyActionError(err))
			writeError(w, http.StatusBadGateway, "action_failed", safeActionError(err))
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
	message := strings.TrimSpace(err.Error())
	lower := strings.ToLower(message)
	for _, marker := range []string{"bearer", "token", "secret", "password", "authorization", "credential"} {
		if strings.Contains(lower, marker) {
			return "databricks action failed"
		}
	}
	if message == "" {
		return "databricks action failed"
	}
	if len(message) > 512 {
		message = message[:512]
	}
	return message
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
