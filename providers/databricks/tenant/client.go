// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package tenant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	krand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	databricksv1alpha1 "github.com/faroshq/provider-databricks/apis/databricks/v1alpha1"
	"github.com/faroshq/provider-databricks/queryapi"
)

var (
	tablesGVR       = databricksv1alpha1.SchemeGroupVersion.WithResource("tables")
	tableQueriesGVR = databricksv1alpha1.SchemeGroupVersion.WithResource("tablequeries")
)

const (
	queryPollInterval = 100 * time.Millisecond
	queryPollTimeout  = 45 * time.Second
)

type ClientFactory struct {
	baseHost string
	baseTLS  rest.TLSClientConfig

	mu  sync.RWMutex
	hot map[string]dynamic.Interface
}

func NewClientFactory(base *rest.Config) *ClientFactory {
	if base == nil {
		return nil
	}
	baseHost, err := stripClusterSuffix(base.Host)
	if err != nil {
		baseHost = strings.TrimRight(base.Host, "/")
	}
	tls := base.TLSClientConfig
	tls.CertData = nil
	tls.CertFile = ""
	tls.KeyData = nil
	tls.KeyFile = ""
	return &ClientFactory{
		baseHost: baseHost,
		baseTLS:  tls,
		hot:      make(map[string]dynamic.Interface),
	}
}

func (f *ClientFactory) For(clusterID, token string) (dynamic.Interface, error) {
	if token == "" {
		return nil, errors.New("no bearer token on request; cannot act on the tenant's behalf")
	}
	key := clusterID + ":" + hashToken(token)

	f.mu.RLock()
	dyn, ok := f.hot[key]
	f.mu.RUnlock()
	if ok {
		return dyn, nil
	}

	cfg := &rest.Config{
		Host:            f.baseHost + "/clusters/" + clusterID,
		BearerToken:     token,
		TLSClientConfig: f.baseTLS,
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("dynamic client for cluster %q: %w", clusterID, err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if existing, ok := f.hot[key]; ok {
		return existing, nil
	}
	if f.hot == nil {
		f.hot = make(map[string]dynamic.Interface)
	}
	f.hot[key] = dyn
	return dyn, nil
}

func (f *ClientFactory) TableResolverForRequest(r *http.Request) queryapi.TableResolver {
	if f == nil {
		return queryapi.UnavailableResolver{Message: "tenant client unavailable (provider kubeconfig not set)"}
	}
	ident := identityFromRequest(r)
	return tableResolver{factory: f, identity: ident}
}

func (f *ClientFactory) QueryRunnerForRequest(r *http.Request) queryapi.QueryRunner {
	if f == nil {
		return unavailableQueryRunner{message: "tenant client unavailable (provider kubeconfig not set)"}
	}
	return queryRunner{factory: f, identity: identityFromRequest(r)}
}

type identity struct {
	tenantPath string
	clusterID  string
	token      string
}

func identityFromRequest(r *http.Request) identity {
	return identity{
		tenantPath: r.Header.Get("X-Kedge-Tenant"),
		clusterID:  r.Header.Get("X-Kedge-Cluster"),
		token:      bearerToken(r),
	}
}

func bearerToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

type tableResolver struct {
	factory  *ClientFactory
	identity identity
}

type queryRunner struct {
	factory  *ClientFactory
	identity identity
}

func (r queryRunner) QueryTable(ctx context.Context, request queryapi.QueryTableRequest) (result queryapi.QueryTableResult, err error) {
	request, err = queryapi.NormalizeQueryRequest(request)
	if err != nil {
		return queryapi.QueryTableResult{}, err
	}
	dyn, err := r.dynamicClient()
	if err != nil {
		return queryapi.QueryTableResult{}, err
	}
	name := "table-query-" + randomNameSuffix()
	spec := map[string]any{
		"actionVersion": request.ActionVersion,
		"tableRef":      request.TableRef,
		"limit":         int64(request.Limit),
	}
	if len(request.Columns) > 0 {
		columns := make([]any, 0, len(request.Columns))
		for _, column := range request.Columns {
			columns = append(columns, column)
		}
		spec["columns"] = columns
	}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": groupVersionString(databricksv1alpha1.GroupName, databricksv1alpha1.Version),
		"kind":       "TableQuery",
		"metadata": map[string]any{
			"name": name,
		},
		"spec": spec,
	}}
	resource := dyn.Resource(tableQueriesGVR)
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		cleanupErr := resource.Delete(cleanupCtx, name, metav1.DeleteOptions{})
		if apierrors.IsNotFound(cleanupErr) {
			cleanupErr = nil
		}
		if cleanupErr != nil {
			if err != nil {
				err = fmt.Errorf("%w; query cleanup failed", err)
			} else {
				result = queryapi.QueryTableResult{}
				err = errors.New("table query cleanup failed")
			}
		}
	}()
	if _, err = resource.Create(ctx, obj, metav1.CreateOptions{}); err != nil {
		return queryapi.QueryTableResult{}, sanitizeQueryError(err)
	}

	deadline := time.NewTimer(queryPollTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(queryPollInterval)
	defer ticker.Stop()
	for {
		current, getErr := resource.Get(ctx, name, metav1.GetOptions{})
		if getErr != nil {
			return queryapi.QueryTableResult{}, sanitizeQueryError(getErr)
		}
		phase, _, _ := unstructured.NestedString(current.Object, "status", "phase")
		switch databricksv1alpha1.TableQueryPhase(phase) {
		case databricksv1alpha1.TableQueryPhaseSucceeded:
			return resultFromStatus(current, request.TableRef), nil
		case databricksv1alpha1.TableQueryPhaseFailed:
			message, _, _ := unstructured.NestedString(current.Object, "status", "error")
			return queryapi.QueryTableResult{}, sanitizeQueryError(errors.New(message))
		}
		select {
		case <-ctx.Done():
			return queryapi.QueryTableResult{}, ctx.Err()
		case <-deadline.C:
			return queryapi.QueryTableResult{}, errors.New("table query timed out")
		case <-ticker.C:
		}
	}
}

func (r queryRunner) dynamicClient() (dynamic.Interface, error) {
	if r.identity.tenantPath == "" {
		return nil, errors.New("no tenant identity on this request; bearer token did not resolve to a workspace")
	}
	if r.identity.clusterID == "" {
		return nil, errors.New("no workspace cluster on this request (X-Kedge-Cluster missing)")
	}
	if r.factory == nil {
		return nil, errors.New("tenant client unavailable (provider kubeconfig not set)")
	}
	return r.factory.For(r.identity.clusterID, r.identity.token)
}

type unavailableQueryRunner struct{ message string }

func (r unavailableQueryRunner) QueryTable(context.Context, queryapi.QueryTableRequest) (queryapi.QueryTableResult, error) {
	return queryapi.QueryTableResult{}, errors.New(r.message)
}

func resultFromStatus(obj *unstructured.Unstructured, tableRef string) queryapi.QueryTableResult {
	result := queryapi.QueryTableResult{ActionVersion: queryapi.ActionVersionV1, TableRef: tableRef}
	if columns, ok, _ := unstructured.NestedSlice(obj.Object, "status", "columns"); ok {
		for _, raw := range columns {
			column, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			name, _, _ := unstructured.NestedString(column, "name")
			typ, _, _ := unstructured.NestedString(column, "type")
			if name != "" {
				result.Columns = append(result.Columns, queryapi.QueryColumn{Name: name, Type: typ})
			}
		}
	}
	if rows, ok, _ := unstructured.NestedSlice(obj.Object, "status", "rows"); ok {
		for _, raw := range rows {
			row, ok := raw.(map[string]any)
			if ok {
				result.Rows = append(result.Rows, row)
			}
		}
	}
	result.Truncated, _, _ = unstructured.NestedBool(obj.Object, "status", "truncated")
	return boundResult(result)
}

func boundResult(result queryapi.QueryTableResult) queryapi.QueryTableResult {
	return queryapi.BoundQueryResult(result)
}

func sanitizeQueryError(err error) error {
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(err.Error())
	lower := strings.ToLower(message)
	for _, marker := range []string{"bearer", "token", "secret", "password", "authorization"} {
		if strings.Contains(lower, marker) {
			return errors.New("table query failed")
		}
	}
	if len(message) > 512 {
		message = message[:512]
	}
	if message == "" {
		return errors.New("table query failed")
	}
	return errors.New(message)
}

func randomNameSuffix() string {
	return krand.String(10)
}

func groupVersionString(group, version string) string {
	if group == "" {
		return version
	}
	return group + "/" + version
}

func (r tableResolver) ListTables(ctx context.Context) (map[string]queryapi.TableRef, error) {
	dyn, err := r.dynamicClient()
	if err != nil {
		return nil, err
	}
	list, err := dyn.Resource(tablesGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make(map[string]queryapi.TableRef, len(list.Items))
	for _, item := range list.Items {
		ref, ok := tableRefFromObject(item)
		if ok {
			out[item.GetName()] = ref
		}
	}
	return out, nil
}

func (r tableResolver) GetTable(ctx context.Context, name string) (queryapi.TableRef, bool, error) {
	dyn, err := r.dynamicClient()
	if err != nil {
		return queryapi.TableRef{}, false, err
	}
	item, err := dyn.Resource(tablesGVR).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return queryapi.TableRef{}, false, nil
	}
	if err != nil {
		return queryapi.TableRef{}, false, err
	}
	ref, ok := tableRefFromObject(*item)
	if !ok {
		return queryapi.TableRef{}, false, nil
	}
	return ref, true, nil
}

func (r tableResolver) dynamicClient() (dynamic.Interface, error) {
	if r.identity.tenantPath == "" {
		return nil, errors.New("no tenant identity on this request; bearer token did not resolve to a workspace")
	}
	if r.identity.clusterID == "" {
		return nil, errors.New("no workspace cluster on this request (X-Kedge-Cluster missing)")
	}
	if r.factory == nil {
		return nil, errors.New("tenant client unavailable (provider kubeconfig not set)")
	}
	return r.factory.For(r.identity.clusterID, r.identity.token)
}

func tableRefFromObject(item unstructured.Unstructured) (queryapi.TableRef, bool) {
	catalog, _, _ := unstructured.NestedString(item.Object, "spec", "catalog")
	schemaName, _, _ := unstructured.NestedString(item.Object, "spec", "schema")
	table, _, _ := unstructured.NestedString(item.Object, "spec", "table")
	if strings.TrimSpace(catalog) == "" || strings.TrimSpace(schemaName) == "" || strings.TrimSpace(table) == "" {
		return queryapi.TableRef{}, false
	}
	return queryapi.TableRef{Catalog: catalog, Schema: schemaName, Table: table}, true
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:8])
}

func stripClusterSuffix(host string) (string, error) {
	u, err := url.Parse(host)
	if err != nil {
		return "", fmt.Errorf("parse base kubeconfig host %q: %w", host, err)
	}
	idx := strings.Index(u.Path, "/clusters/")
	if idx < 0 {
		return strings.TrimRight(host, "/"), nil
	}
	u.Path = u.Path[:idx]
	return strings.TrimRight(u.String(), "/"), nil
}
