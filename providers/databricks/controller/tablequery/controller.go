// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// Package tablequery executes transient tenant TableQuery resources. It is the
// only component allowed to resolve Databricks credential Secrets and invoke
// the backend statement executor for query_table.
package tablequery

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	databricksv1alpha1 "github.com/faroshq/provider-databricks/apis/databricks/v1alpha1"
	"github.com/faroshq/provider-databricks/backend"
	"github.com/faroshq/provider-databricks/controller/shared"
	"github.com/faroshq/provider-databricks/queryapi"
)

const (
	ReasonSucceeded             = "Succeeded"
	ReasonInvalidRequest        = "InvalidRequest"
	ReasonTableUnavailable      = "TableUnavailable"
	ReasonWarehouseUnavailable  = "WarehouseUnavailable"
	ReasonConnectionUnavailable = "ConnectionUnavailable"
	ReasonCredentialUnavailable = "CredentialUnavailable"
	ReasonTableNotReady         = "TableNotReady"
	ReasonWarehouseNotReady     = "WarehouseNotReady"
	ReasonConnectionNotReady    = "ConnectionNotReady"
	ReasonExecutionFailed       = "ExecutionFailed"
	ReasonExecutorUnavailable   = "ExecutorUnavailable"
	ReasonConnectionMismatch    = "ConnectionMismatch"
	QueryTimeout                = 30 * time.Second
)

type Reconciler struct {
	Manager  mcmanager.Manager
	Executor backend.QueryExecutor
}

func (r *Reconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.Manager = mgr
	return mcbuilder.ControllerManagedBy(mgr).
		Named("databricks-table-query").
		For(&databricksv1alpha1.TableQuery{}).
		Complete(r)
}

func (r *Reconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := klog.FromContext(ctx).WithValues("tableQuery", req.Name, "cluster", req.ClusterName)
	c, err := shared.ClusterClient(ctx, r.Manager, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}
	result, err := r.reconcileTableQuery(ctx, c, req.NamespacedName)
	if err == nil {
		logger.Info("TableQuery reconciled")
	}
	return result, err
}

func (r *Reconciler) reconcileTableQuery(ctx context.Context, c client.Client, key types.NamespacedName) (ctrl.Result, error) {
	var query databricksv1alpha1.TableQuery
	if err := c.Get(ctx, key, &query); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if !query.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	if query.Status.ObservedGeneration == query.Generation &&
		(query.Status.Phase == databricksv1alpha1.TableQueryPhaseSucceeded || query.Status.Phase == databricksv1alpha1.TableQueryPhaseFailed) {
		return ctrl.Result{}, nil
	}

	request := queryapi.QueryTableRequest{
		ActionVersion: query.Spec.ActionVersion,
		TableRef:      query.Spec.TableRef,
		Columns:       append([]string(nil), query.Spec.Columns...),
		Limit:         int(query.Spec.Limit),
	}
	request, err := queryapi.NormalizeQueryRequest(request)
	if err != nil {
		return r.fail(ctx, c, &query, ReasonInvalidRequest, err.Error())
	}

	tbl, err := shared.ResolveTable(ctx, c, request.TableRef)
	if err != nil {
		return r.fail(ctx, c, &query, ReasonTableUnavailable, err.Error())
	}
	wh, err := shared.ResolveWarehouse(ctx, c, tbl.Spec.WarehouseRef)
	if err != nil {
		return r.fail(ctx, c, &query, ReasonWarehouseUnavailable, err.Error())
	}
	if strings.TrimSpace(tbl.Spec.ConnectionRef) == "" || tbl.Spec.ConnectionRef != wh.Spec.ConnectionRef {
		return r.fail(ctx, c, &query, ReasonConnectionMismatch, "table and warehouse connection references do not match")
	}
	if !shared.CurrentConditionTrue(tbl.Status.Conditions, tbl.Status.ObservedGeneration, tbl.Generation, databricksv1alpha1.ConditionReady) {
		return r.fail(ctx, c, &query, ReasonTableNotReady, "table is not ready")
	}
	if !shared.CurrentConditionTrue(wh.Status.Conditions, wh.Status.ObservedGeneration, wh.Generation, databricksv1alpha1.ConditionReady) {
		return r.fail(ctx, c, &query, ReasonWarehouseNotReady, "warehouse is not ready")
	}
	conn, err := shared.ResolveConnection(ctx, c, tbl.Spec.ConnectionRef)
	if err != nil {
		return r.fail(ctx, c, &query, ReasonConnectionUnavailable, err.Error())
	}
	if !shared.CurrentConditionTrue(conn.Status.Conditions, conn.Status.ObservedGeneration, conn.Generation, databricksv1alpha1.ConditionValidated) ||
		!shared.CurrentConditionTrue(conn.Status.Conditions, conn.Status.ObservedGeneration, conn.Generation, databricksv1alpha1.ConditionReady) {
		return r.fail(ctx, c, &query, ReasonConnectionNotReady, "connection is not ready")
	}
	if conn.Spec.AuthType != databricksv1alpha1.ConnectionAuthPAT {
		return r.fail(ctx, c, &query, ReasonConnectionUnavailable, "connection authType is unsupported")
	}
	token, err := shared.ResolveBearerToken(ctx, c, conn)
	if err != nil {
		return r.fail(ctx, c, &query, ReasonCredentialUnavailable, err.Error())
	}
	if r.Executor == nil {
		return r.fail(ctx, c, &query, ReasonExecutorUnavailable, "databricks query executor is not configured")
	}
	allowed := make([]string, 0, len(tbl.Status.Columns))
	for _, column := range tbl.Status.Columns {
		allowed = append(allowed, column.Name)
	}
	if len(request.Columns) > 0 && len(allowed) == 0 {
		return r.fail(ctx, c, &query, ReasonInvalidRequest, "table schema is not available for projection")
	}

	now := metav1.Now()
	query.Status.ObservedGeneration = query.Generation
	query.Status.Phase = databricksv1alpha1.TableQueryPhaseRunning
	query.Status.Error = ""
	query.Status.Columns = nil
	query.Status.Rows = nil
	query.Status.Truncated = false
	query.Status.StartedAt = &now
	query.Status.CompletedAt = nil
	if err := c.Status().Update(ctx, &query); err != nil {
		return ctrl.Result{}, err
	}

	queryCtx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	result, err := r.Executor.ExecuteTableQuery(queryCtx, backend.QueryExecutionTarget{
		Table: queryapi.TableRef{
			Catalog: tbl.Spec.Catalog,
			Schema:  tbl.Spec.Schema,
			Table:   tbl.Spec.Table,
		},
		Connection: queryapi.ConnectionRef{
			Name:     conn.Name,
			Host:     conn.Spec.Host,
			AuthType: string(conn.Spec.AuthType),
		},
		Warehouse: queryapi.WarehouseRef{
			Name:        wh.Name,
			WarehouseID: wh.Spec.WarehouseID,
		},
		BearerToken:    token,
		Projection:     request.Columns,
		Limit:          request.Limit,
		AllowedColumns: allowed,
	})
	if err != nil {
		return r.fail(ctx, c, &query, ReasonExecutionFailed, safeExecutionMessage(err))
	}
	completed := metav1.Now()
	query.Status.Phase = databricksv1alpha1.TableQueryPhaseSucceeded
	query.Status.Columns = make([]databricksv1alpha1.QueryColumn, 0, len(result.Columns))
	for _, column := range result.Columns {
		query.Status.Columns = append(query.Status.Columns, databricksv1alpha1.QueryColumn{Name: column.Name, Type: column.Type})
	}
	rows, err := marshalRows(result.Rows)
	if err != nil {
		return r.fail(ctx, c, &query, ReasonExecutionFailed, "query result could not be encoded")
	}
	query.Status.Rows = rows
	query.Status.Truncated = result.Truncated
	query.Status.CompletedAt = &completed
	query.Status.Error = ""
	if err := c.Status().Update(ctx, &query); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func marshalRows(rows []map[string]any) ([]runtime.RawExtension, error) {
	out := make([]runtime.RawExtension, 0, len(rows))
	for _, row := range rows {
		raw, err := json.Marshal(row)
		if err != nil {
			return nil, err
		}
		out = append(out, runtime.RawExtension{Raw: raw})
	}
	return out, nil
}

func (r *Reconciler) fail(ctx context.Context, c client.Client, query *databricksv1alpha1.TableQuery, reason, message string) (ctrl.Result, error) {
	query.Status.ObservedGeneration = query.Generation
	query.Status.Phase = databricksv1alpha1.TableQueryPhaseFailed
	query.Status.Columns = nil
	query.Status.Rows = nil
	query.Status.Truncated = false
	query.Status.Error = truncateStatusMessage(message)
	now := metav1.Now()
	query.Status.CompletedAt = &now
	if err := c.Status().Update(ctx, query); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func truncateStatusMessage(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > 512 {
		return message[:512]
	}
	return message
}

func safeExecutionMessage(err error) string {
	message := strings.TrimSpace(backend.SafeStatusMessage(err))
	lower := strings.ToLower(message)
	for _, marker := range []string{"bearer", "token", "secret", "password", "authorization"} {
		if strings.Contains(lower, marker) {
			return "databricks table query failed"
		}
	}
	if strings.Contains(lower, "invalid identifier") || strings.Contains(lower, "not present in the imported table schema") {
		return "table query projection is invalid"
	}
	if message == "" {
		return "databricks table query failed"
	}
	return truncateStatusMessage(message)
}
