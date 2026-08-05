// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package tablequery

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	databricksv1alpha1 "github.com/faroshq/provider-databricks/apis/databricks/v1alpha1"
	"github.com/faroshq/provider-databricks/backend"
	"github.com/faroshq/provider-databricks/queryapi"
	databricksscheme "github.com/faroshq/provider-databricks/scheme"
)

type fakeExecutor struct {
	target backend.QueryExecutionTarget
	result queryapi.QueryTableResult
	err    error
	calls  int
}

func (f *fakeExecutor) ExecuteTableQuery(_ context.Context, target backend.QueryExecutionTarget) (queryapi.QueryTableResult, error) {
	f.calls++
	f.target = target
	return f.result, f.err
}

func TestReconcileTableQueryResolvesBoundTargetAndStoresBoundedResult(t *testing.T) {
	ctx := context.Background()
	conn := &databricksv1alpha1.Connection{
		ObjectMeta: metav1.ObjectMeta{Name: "sales-connection", Generation: 1},
		Spec: databricksv1alpha1.ConnectionSpec{
			Host:     "https://dbc-example.cloud.databricks.com",
			AuthType: databricksv1alpha1.ConnectionAuthPAT,
			SecretRef: databricksv1alpha1.LocalSecretReference{
				Name: "sales-token", Namespace: "default", Key: "token",
			},
		},
		Status: databricksv1alpha1.ConnectionStatus{
			ObservedGeneration: 1,
			Conditions: []metav1.Condition{
				{Type: databricksv1alpha1.ConditionValidated, Status: metav1.ConditionTrue, ObservedGeneration: 1},
				{Type: databricksv1alpha1.ConditionReady, Status: metav1.ConditionTrue, ObservedGeneration: 1},
			},
		},
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "sales-token", Namespace: "default"}, Data: map[string][]byte{"token": []byte("provider-only-token")}}
	warehouse := &databricksv1alpha1.Warehouse{
		ObjectMeta: metav1.ObjectMeta{Name: "sales-warehouse", Generation: 1},
		Spec:       databricksv1alpha1.WarehouseSpec{ConnectionRef: "sales-connection", WarehouseID: "wh-123"},
		Status: databricksv1alpha1.WarehouseStatus{
			ObservedGeneration: 1,
			Conditions:         []metav1.Condition{{Type: databricksv1alpha1.ConditionReady, Status: metav1.ConditionTrue, ObservedGeneration: 1}},
		},
	}
	table := &databricksv1alpha1.Table{
		ObjectMeta: metav1.ObjectMeta{Name: "taxi-trips", Generation: 2},
		Spec: databricksv1alpha1.TableSpec{
			ConnectionRef: "sales-connection", WarehouseRef: "sales-warehouse",
			Catalog: "main", Schema: "gold", Table: "taxi_trips",
		},
		Status: databricksv1alpha1.TableStatus{
			ObservedGeneration: 2,
			Columns:            []databricksv1alpha1.Column{{Name: "trip_id", Type: "STRING"}, {Name: "distance", Type: "DOUBLE"}},
			Conditions:         []metav1.Condition{{Type: databricksv1alpha1.ConditionReady, Status: metav1.ConditionTrue, ObservedGeneration: 2}},
		},
	}
	query := &databricksv1alpha1.TableQuery{
		ObjectMeta: metav1.ObjectMeta{Name: "query-1", Generation: 4},
		Spec:       databricksv1alpha1.TableQuerySpec{ActionVersion: queryapi.ActionVersionV1, TableRef: "taxi-trips", Columns: []string{"trip_id"}, Limit: 10},
	}
	client := fake.NewClientBuilder().WithScheme(databricksscheme.NewScheme()).WithObjects(conn, secret, warehouse, table, query).WithStatusSubresource(&databricksv1alpha1.TableQuery{}).Build()
	executor := &fakeExecutor{result: queryapi.QueryTableResult{Columns: []queryapi.QueryColumn{{Name: "trip_id", Type: "STRING"}}, Rows: []map[string]any{{"trip_id": "t-1"}}}}
	r := &Reconciler{Executor: executor}
	if _, err := r.reconcileTableQuery(ctx, client, types.NamespacedName{Name: query.Name}); err != nil {
		t.Fatalf("reconcileTableQuery returned error: %v", err)
	}
	if executor.calls != 1 {
		t.Fatalf("executor calls = %d, want 1", executor.calls)
	}
	if executor.target.BearerToken != "provider-only-token" || executor.target.Warehouse.WarehouseID != "wh-123" || executor.target.Table.Table != "taxi_trips" {
		t.Fatalf("resolved target = %#v", executor.target)
	}
	if len(executor.target.AllowedColumns) != 2 || executor.target.AllowedColumns[0] != "trip_id" {
		t.Fatalf("allowed columns = %#v", executor.target.AllowedColumns)
	}
	var got databricksv1alpha1.TableQuery
	if err := client.Get(ctx, types.NamespacedName{Name: query.Name}, &got); err != nil {
		t.Fatalf("get query: %v", err)
	}
	if got.Status.Phase != databricksv1alpha1.TableQueryPhaseSucceeded || len(got.Status.Rows) != 1 {
		t.Fatalf("status = %#v", got.Status)
	}
	var row map[string]any
	if err := json.Unmarshal(got.Status.Rows[0].Raw, &row); err != nil || row["trip_id"] != "t-1" {
		t.Fatalf("stored row = %#v err=%v", row, err)
	}
	if strings.Contains(got.Status.Error, "provider-only-token") {
		t.Fatal("query status leaked credential")
	}
}

func TestReconcileTableQueryRejectsWarehouseConnectionMismatch(t *testing.T) {
	ctx := context.Background()
	table := &databricksv1alpha1.Table{ObjectMeta: metav1.ObjectMeta{Name: "taxi-trips", Generation: 1}, Spec: databricksv1alpha1.TableSpec{ConnectionRef: "connection-a", WarehouseRef: "warehouse-a", Catalog: "main", Schema: "gold", Table: "taxi_trips"}}
	warehouse := &databricksv1alpha1.Warehouse{ObjectMeta: metav1.ObjectMeta{Name: "warehouse-a", Generation: 1}, Spec: databricksv1alpha1.WarehouseSpec{ConnectionRef: "connection-b", WarehouseID: "wh-123"}}
	query := &databricksv1alpha1.TableQuery{ObjectMeta: metav1.ObjectMeta{Name: "query-1", Generation: 1}, Spec: databricksv1alpha1.TableQuerySpec{ActionVersion: queryapi.ActionVersionV1, TableRef: "taxi-trips", Limit: 1}}
	client := fake.NewClientBuilder().WithScheme(databricksscheme.NewScheme()).WithObjects(table, warehouse, query).WithStatusSubresource(&databricksv1alpha1.TableQuery{}).Build()
	executor := &fakeExecutor{}
	if _, err := (&Reconciler{Executor: executor}).reconcileTableQuery(ctx, client, types.NamespacedName{Name: query.Name}); err != nil {
		t.Fatalf("reconcileTableQuery returned error: %v", err)
	}
	if executor.calls != 0 {
		t.Fatalf("executor calls = %d, want 0", executor.calls)
	}
	var got databricksv1alpha1.TableQuery
	if err := client.Get(ctx, types.NamespacedName{Name: query.Name}, &got); err != nil {
		t.Fatalf("get query: %v", err)
	}
	if got.Status.Phase != databricksv1alpha1.TableQueryPhaseFailed || got.Status.Error != "table and warehouse connection references do not match" {
		t.Fatalf("status = %#v", got.Status)
	}
}

func TestReconcileTableQueryRejectsProjectionWithoutCachedSchema(t *testing.T) {
	ctx := context.Background()
	conn := &databricksv1alpha1.Connection{ObjectMeta: metav1.ObjectMeta{Name: "connection-a", Generation: 1}, Spec: databricksv1alpha1.ConnectionSpec{Host: "https://dbc-example.cloud.databricks.com", AuthType: databricksv1alpha1.ConnectionAuthPAT, SecretRef: databricksv1alpha1.LocalSecretReference{Name: "token"}}, Status: databricksv1alpha1.ConnectionStatus{ObservedGeneration: 1, Conditions: []metav1.Condition{{Type: databricksv1alpha1.ConditionValidated, Status: metav1.ConditionTrue, ObservedGeneration: 1}, {Type: databricksv1alpha1.ConditionReady, Status: metav1.ConditionTrue, ObservedGeneration: 1}}}}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "token", Namespace: "default"}, Data: map[string][]byte{"token": []byte("secret")}}
	warehouse := &databricksv1alpha1.Warehouse{ObjectMeta: metav1.ObjectMeta{Name: "warehouse-a", Generation: 1}, Spec: databricksv1alpha1.WarehouseSpec{ConnectionRef: "connection-a", WarehouseID: "wh-123"}, Status: databricksv1alpha1.WarehouseStatus{ObservedGeneration: 1, Conditions: []metav1.Condition{{Type: databricksv1alpha1.ConditionReady, Status: metav1.ConditionTrue, ObservedGeneration: 1}}}}
	table := &databricksv1alpha1.Table{ObjectMeta: metav1.ObjectMeta{Name: "taxi-trips", Generation: 1}, Spec: databricksv1alpha1.TableSpec{ConnectionRef: "connection-a", WarehouseRef: "warehouse-a", Catalog: "main", Schema: "gold", Table: "taxi_trips"}, Status: databricksv1alpha1.TableStatus{ObservedGeneration: 1, Conditions: []metav1.Condition{{Type: databricksv1alpha1.ConditionReady, Status: metav1.ConditionTrue, ObservedGeneration: 1}}}}
	query := &databricksv1alpha1.TableQuery{ObjectMeta: metav1.ObjectMeta{Name: "query-1", Generation: 1}, Spec: databricksv1alpha1.TableQuerySpec{ActionVersion: queryapi.ActionVersionV1, TableRef: "taxi-trips", Columns: []string{"trip_id"}, Limit: 1}}
	client := fake.NewClientBuilder().WithScheme(databricksscheme.NewScheme()).WithObjects(conn, secret, warehouse, table, query).WithStatusSubresource(&databricksv1alpha1.TableQuery{}).Build()
	executor := &fakeExecutor{}
	if _, err := (&Reconciler{Executor: executor}).reconcileTableQuery(ctx, client, types.NamespacedName{Name: query.Name}); err != nil {
		t.Fatalf("reconcileTableQuery returned error: %v", err)
	}
	if executor.calls != 0 {
		t.Fatalf("executor calls = %d, want 0", executor.calls)
	}
	var got databricksv1alpha1.TableQuery
	if err := client.Get(ctx, types.NamespacedName{Name: query.Name}, &got); err != nil {
		t.Fatalf("get query: %v", err)
	}
	if got.Status.Error != "table schema is not available for projection" {
		t.Fatalf("error = %q", got.Status.Error)
	}
}

func TestReconcileTableQueryDoesNotExecuteAgainstUnreadyDependencies(t *testing.T) {
	ctx := context.Background()
	conn := &databricksv1alpha1.Connection{
		ObjectMeta: metav1.ObjectMeta{Name: "sales-connection", Generation: 2},
		Spec: databricksv1alpha1.ConnectionSpec{
			Host:     "https://dbc-example.cloud.databricks.com",
			AuthType: databricksv1alpha1.ConnectionAuthPAT,
			SecretRef: databricksv1alpha1.LocalSecretReference{
				Name: "sales-token", Namespace: "default", Key: "token",
			},
		},
		Status: databricksv1alpha1.ConnectionStatus{
			ObservedGeneration: 2,
			Conditions: []metav1.Condition{
				{Type: databricksv1alpha1.ConditionValidated, Status: metav1.ConditionFalse, ObservedGeneration: 2},
				{Type: databricksv1alpha1.ConditionReady, Status: metav1.ConditionFalse, ObservedGeneration: 2},
			},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "sales-token", Namespace: "default"},
		Data:       map[string][]byte{"token": []byte("provider-only-token")},
	}
	warehouse := &databricksv1alpha1.Warehouse{
		ObjectMeta: metav1.ObjectMeta{Name: "sales-warehouse", Generation: 3},
		Spec:       databricksv1alpha1.WarehouseSpec{ConnectionRef: "sales-connection", WarehouseID: "wh-123"},
		Status: databricksv1alpha1.WarehouseStatus{
			ObservedGeneration: 3,
			Conditions: []metav1.Condition{
				{Type: databricksv1alpha1.ConditionReady, Status: metav1.ConditionFalse, ObservedGeneration: 3},
			},
		},
	}
	table := &databricksv1alpha1.Table{
		ObjectMeta: metav1.ObjectMeta{Name: "taxi-trips", Generation: 4},
		Spec: databricksv1alpha1.TableSpec{
			ConnectionRef: "sales-connection", WarehouseRef: "sales-warehouse",
			Catalog: "main", Schema: "gold", Table: "taxi_trips",
		},
		Status: databricksv1alpha1.TableStatus{
			ObservedGeneration: 4,
			Columns:            []databricksv1alpha1.Column{{Name: "trip_id", Type: "STRING"}},
			Conditions: []metav1.Condition{
				{Type: databricksv1alpha1.ConditionReady, Status: metav1.ConditionFalse, ObservedGeneration: 4},
			},
		},
	}
	query := &databricksv1alpha1.TableQuery{
		ObjectMeta: metav1.ObjectMeta{Name: "query-unready", Generation: 1},
		Spec: databricksv1alpha1.TableQuerySpec{
			ActionVersion: queryapi.ActionVersionV1,
			TableRef:      "taxi-trips",
			Columns:       []string{"trip_id"},
			Limit:         1,
		},
	}
	client := fake.NewClientBuilder().
		WithScheme(databricksscheme.NewScheme()).
		WithObjects(conn, secret, warehouse, table, query).
		WithStatusSubresource(&databricksv1alpha1.TableQuery{}).
		Build()
	executor := &fakeExecutor{result: queryapi.QueryTableResult{Rows: []map[string]any{{"trip_id": "must-not-run"}}}}
	if _, err := (&Reconciler{Executor: executor}).reconcileTableQuery(ctx, client, types.NamespacedName{Name: query.Name}); err != nil {
		t.Fatalf("reconcileTableQuery returned error: %v", err)
	}
	if executor.calls != 0 {
		t.Fatalf("executor calls = %d, want 0 while dependency conditions are not Ready/Validated", executor.calls)
	}
	var got databricksv1alpha1.TableQuery
	if err := client.Get(ctx, types.NamespacedName{Name: query.Name}, &got); err != nil {
		t.Fatalf("get query: %v", err)
	}
	if got.Status.Phase != databricksv1alpha1.TableQueryPhaseFailed {
		t.Fatalf("phase = %q, want Failed for unready dependencies", got.Status.Phase)
	}
	if strings.Contains(got.Status.Error, "provider-only-token") {
		t.Fatalf("query status leaked credential: %q", got.Status.Error)
	}
}
