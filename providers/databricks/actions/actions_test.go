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
}

func (f *fakeExecutor) QueryTable(ctx context.Context, ref ResourceRef, input QueryInput) (queryapi.QueryTableResult, error) {
	f.ctx, f.ref, f.input = ctx, ref, input
	return queryapi.QueryTableResult{
		ActionVersion: queryapi.ActionVersionV1,
		TableRef:      ref.Name,
		Columns:       []queryapi.QueryColumn{{Name: "trip_id", Type: "BIGINT"}},
		Rows:          []map[string]any{{"trip_id": 1}},
	}, nil
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
