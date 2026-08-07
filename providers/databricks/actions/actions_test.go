// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package actions

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"

	"github.com/faroshq/provider-databricks/queryapi"
)

type fakeExecutor struct {
	ref   ResourceRef
	input QueryInput
	ctx   context.Context
	err   error
}

func (f *fakeExecutor) QueryTable(ctx context.Context, ref ResourceRef, input QueryInput) (queryapi.QueryTableResult, error) {
	f.ctx, f.ref, f.input = ctx, ref, input
	if f.err != nil {
		return queryapi.QueryTableResult{}, f.err
	}
	return queryapi.QueryTableResult{
		ActionVersion: queryapi.ActionVersionV1,
		TableRef:      ref.Name,
		Columns:       []queryapi.QueryColumn{{Name: "trip_id", Type: "BIGINT"}},
		Rows:          []map[string]any{{"trip_id": 1}},
	}, nil
}

func TestQueryTableActionPreservesTypedFailure(t *testing.T) {
	executor := &fakeExecutor{err: &ActionError{
		Code: ActionErrorCodeResourceNotReady, Message: "bound Databricks resource is not ready",
		Status: http.StatusConflict, Retryable: false,
	}}
	h := NewHandler(Deps{QueryExecutorFromRequest: func(*http.Request) QueryExecutor { return executor }})
	r := httptest.NewRequest(http.MethodPost, PathQueryTableV1, strings.NewReader(`{"resourceRef":{"apiVersion":"databricks.kedge.faros.sh/v1alpha1","kind":"Table","resource":"tables","name":"taxi-trips"},"input":{}}`))
	r.Header.Set("Authorization", "Bearer caller")
	r.Header.Set("X-Kedge-Tenant", "root:kedge:tenants:org:ws")
	r.Header.Set("X-Kedge-Cluster", "cluster-a")
	r.Header.Set("X-Request-ID", "request-typed")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, r)
	if rw.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rw.Code, rw.Body.String())
	}
	var body struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode typed failure: %v", err)
	}
	if body.Error.Code != ActionErrorCodeResourceNotReady || body.Error.Message != "bound Databricks resource is not ready" || body.Error.Retryable {
		t.Fatalf("typed failure = %#v", body.Error)
	}
}

func TestQueryTableActionEmitsSchemaProjectionFailure(t *testing.T) {
	const message = `requested column "missing" is not present in the imported table schema`
	executor := &fakeExecutor{err: &ActionError{
		Code: ActionErrorCodeSchemaProjectionInvalid, Message: message,
		Status: http.StatusBadRequest, Retryable: false,
	}}
	h := NewHandler(Deps{QueryExecutorFromRequest: func(*http.Request) QueryExecutor { return executor }})
	r := httptest.NewRequest(http.MethodPost, PathQueryTableV1, strings.NewReader(`{"resourceRef":{"apiVersion":"databricks.kedge.faros.sh/v1alpha1","kind":"Table","resource":"tables","name":"taxi-trips"},"input":{"columns":["missing"],"limit":1}}`))
	r.Header.Set("Authorization", "Bearer caller")
	r.Header.Set("X-Kedge-Tenant", "root:kedge:tenants:org:ws")
	r.Header.Set("X-Kedge-Cluster", "cluster-a")
	r.Header.Set("X-Request-ID", "request-projection")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, r)
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rw.Code, rw.Body.String())
	}
	var body struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Retryable bool   `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode schema projection failure: %v", err)
	}
	if body.Error.Code != ActionErrorCodeSchemaProjectionInvalid || body.Error.Message != message || body.Error.Retryable {
		t.Fatalf("schema projection failure = %#v; body=%s", body.Error, rw.Body.String())
	}
}

func TestQueryTableActionDefaultsTypedFailureStatusFromCode(t *testing.T) {
	executor := &fakeExecutor{err: &ActionError{
		Code: ActionErrorCodeResourceNotReady, Message: "bound Databricks resource is not ready",
	}}
	h := NewHandler(Deps{QueryExecutorFromRequest: func(*http.Request) QueryExecutor { return executor }})
	r := httptest.NewRequest(http.MethodPost, PathQueryTableV1, strings.NewReader(`{"resourceRef":{"apiVersion":"databricks.kedge.faros.sh/v1alpha1","kind":"Table","resource":"tables","name":"taxi-trips"},"input":{}}`))
	r.Header.Set("Authorization", "Bearer caller")
	r.Header.Set("X-Kedge-Tenant", "root:kedge:tenants:org:ws")
	r.Header.Set("X-Kedge-Cluster", "cluster-a")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, r)
	if rw.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rw.Code, rw.Body.String())
	}
}

func TestQueryTableActionUnsafeTypedFailureFallsBack(t *testing.T) {
	unsafe := "SELECT * FROM https://dbc.example.com with bearer token root:kedge:tenants:org:ws"
	executor := &fakeExecutor{err: &ActionError{
		Code: ActionErrorCodeBackendFailure, Message: unsafe,
		Status: http.StatusServiceUnavailable, Retryable: true,
	}}
	h := NewHandler(Deps{QueryExecutorFromRequest: func(*http.Request) QueryExecutor { return executor }})
	r := httptest.NewRequest(http.MethodPost, PathQueryTableV1, strings.NewReader(`{"resourceRef":{"apiVersion":"databricks.kedge.faros.sh/v1alpha1","kind":"Table","resource":"tables","name":"taxi-trips"},"input":{}}`))
	r.Header.Set("Authorization", "Bearer caller")
	r.Header.Set("X-Kedge-Tenant", "root:kedge:tenants:org:ws")
	r.Header.Set("X-Kedge-Cluster", "cluster-a")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, r)
	if rw.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rw.Code, rw.Body.String())
	}
	if strings.Contains(rw.Body.String(), "dbc.example.com") || strings.Contains(rw.Body.String(), "root:kedge:tenants") || strings.Contains(rw.Body.String(), "SELECT") || strings.Contains(rw.Body.String(), "bearer") {
		t.Fatalf("unsafe error leaked: %s", rw.Body.String())
	}
	if !strings.Contains(rw.Body.String(), `"code":"action_failed"`) || !strings.Contains(rw.Body.String(), `"retryable":false`) {
		t.Fatalf("fallback error = %s", rw.Body.String())
	}
}

func TestQueryTableActionBackendAuthFailureUsesGatewayStatus(t *testing.T) {
	executor := &fakeExecutor{err: &ActionError{
		Code: ActionErrorCodeBackendFailure, Message: "databricks statement failed",
		Status: http.StatusUnauthorized, Retryable: false,
	}}
	h := NewHandler(Deps{QueryExecutorFromRequest: func(*http.Request) QueryExecutor { return executor }})
	r := httptest.NewRequest(http.MethodPost, PathQueryTableV1, strings.NewReader(`{"resourceRef":{"apiVersion":"databricks.kedge.faros.sh/v1alpha1","kind":"Table","resource":"tables","name":"taxi-trips"},"input":{}}`))
	r.Header.Set("Authorization", "Bearer caller")
	r.Header.Set("X-Kedge-Tenant", "root:kedge:tenants:org:ws")
	r.Header.Set("X-Kedge-Cluster", "cluster-a")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, r)
	if rw.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want gateway 502; body=%s", rw.Code, rw.Body.String())
	}
	if strings.Contains(rw.Body.String(), `"retryable":true`) {
		t.Fatalf("backend auth failure retained unsafe status/retryability: %s", rw.Body.String())
	}
}

func TestQueryTableActionForwardsOnlyValidatedInput(t *testing.T) {
	executor := &fakeExecutor{}
	h := NewHandler(Deps{QueryExecutorFromRequest: func(*http.Request) QueryExecutor { return executor }, Logger: logr.Discard()})
	r := httptest.NewRequest(http.MethodPost, PathQueryTableV1, strings.NewReader(`{"resourceRef":{"apiVersion":"databricks.kedge.faros.sh/v1alpha1","kind":"Table","resource":"tables","name":"taxi-trips"},"input":{"columns":["trip_id","fare_amount"],"limit":25}}`))
	r.Header.Set("Authorization", "Bearer caller")
	r.Header.Set("X-Kedge-Tenant", "root:kedge:tenants:org:ws")
	r.Header.Set("X-Kedge-Cluster", "cluster-a")
	r.Header.Set("X-Kedge-Action-Deadline-Ms", "5000")
	r.Header.Set("X-Request-ID", "req-1")
	r.Header.Set("Idempotency-Key", "idem-1")
	r.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, r)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rw.Code, rw.Body.String())
	}
	if executor.ref.Name != "taxi-trips" || executor.input.Limit != 25 || len(executor.input.Columns) != 2 {
		t.Fatalf("executor input = %#v %#v", executor.ref, executor.input)
	}
	if executor.ctx == nil {
		t.Fatal("executor did not receive a context")
	}
	if _, ok := executor.ctx.Deadline(); !ok {
		t.Fatal("action deadline was not applied")
	}
	var result queryapi.QueryTableResult
	if err := json.Unmarshal(rw.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.TableRef != "taxi-trips" || len(result.Rows) != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func TestQueryTableActionFailsClosedForMissingTenant(t *testing.T) {
	h := NewHandler(Deps{QueryExecutorFromRequest: func(*http.Request) QueryExecutor {
		t.Fatal("executor should not run without tenant headers")
		return nil
	}})
	r := httptest.NewRequest(http.MethodPost, PathQueryTableV1, strings.NewReader(`{"resourceRef":{"apiVersion":"databricks.kedge.faros.sh/v1alpha1","kind":"Table","resource":"tables","name":"taxi-trips"},"input":{}}`))
	r.Header.Set("Authorization", "Bearer caller")
	r.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, r)
	if rw.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rw.Code)
	}
}

func TestQueryTableActionRejectsCallerResourceOverride(t *testing.T) {
	h := NewHandler(Deps{QueryExecutorFromRequest: func(*http.Request) QueryExecutor {
		t.Fatal("executor should not run for invalid input")
		return nil
	}})
	for _, input := range []string{`{"tableRef":"other"}`, `{"sql":"select 1"}`, `{"limit":101}`} {
		r := httptest.NewRequest(http.MethodPost, PathQueryTableV1, strings.NewReader(`{"resourceRef":{"apiVersion":"databricks.kedge.faros.sh/v1alpha1","kind":"Table","resource":"tables","name":"taxi-trips"},"input":`+input+`}`))
		r.Header.Set("Authorization", "Bearer caller")
		r.Header.Set("X-Kedge-Tenant", "root:kedge:tenants:org:ws")
		r.Header.Set("X-Kedge-Cluster", "cluster-a")
		rw := httptest.NewRecorder()
		h.ServeHTTP(rw, r)
		if rw.Code != http.StatusBadRequest {
			t.Fatalf("input %s status = %d, want 400", input, rw.Code)
		}
	}
}

func TestActionDeadlineDefaultIsBounded(t *testing.T) {
	deadline, err := actionDeadline(httptest.NewRequest(http.MethodPost, PathQueryTableV1, nil))
	if err != nil || deadline != maxActionDeadline {
		t.Fatalf("default deadline = %s, err=%v; want %s", deadline, err, maxActionDeadline)
	}
	if deadline <= 0 || maxActionDeadline != 90*time.Second {
		t.Fatal("action deadline must remain bounded")
	}
	invalid := httptest.NewRequest(http.MethodPost, PathQueryTableV1, nil)
	invalid.Header.Set("X-Kedge-Action-Deadline-Ms", "not-a-number")
	if _, err := actionDeadline(invalid); err == nil {
		t.Fatal("invalid action deadline must fail closed")
	}
}
