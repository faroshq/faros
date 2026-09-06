// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package tenantindex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	kuerystore "github.com/faroshq/kuery/pkg/store"
)

func TestTenantEdgeIndex(t *testing.T) {
	s, err := kuerystore.NewStore(kuerystore.Config{Driver: "sqlite", DSN: ":memory:"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	checkTenantIndex(t, s.RawDB())
}

func TestPostgresTenantEdgeIndex(t *testing.T) {
	dsn := os.Getenv("KUERY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("KUERY_TEST_POSTGRES_DSN is not set")
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("KUERY_TEST_POSTGRES_DSN must be a PostgreSQL URL")
	}
	admin, err := kuerystore.NewStore(kuerystore.Config{Driver: "postgres", DSN: dsn})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schemaName := "tenant_index_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := admin.RawDB().Exec("CREATE SCHEMA " + schemaName).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.RawDB().Exec("DROP SCHEMA " + schemaName + " CASCADE").Error; err != nil {
			t.Errorf("cleanup schema: %v", err)
		}
	})
	q := u.Query()
	q.Set("search_path", schemaName)
	u.RawQuery = q.Encode()
	s, err := kuerystore.NewStore(kuerystore.Config{Driver: "postgres", DSN: u.String()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	checkTenantIndex(t, s.RawDB())
}

func checkTenantIndex(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.AutoMigrate(&kuerystore.ClusterModel{}); err != nil {
		t.Fatal(err)
	}
	const tenant = "tenant_%!a"
	now := time.Now().UTC()
	row := func(name, owner, status string) kuerystore.ClusterModel {
		labels, err := json.Marshal(map[string]string{"tenant": owner})
		if err != nil {
			t.Fatal(err)
		}
		return kuerystore.ClusterModel{Name: name, Status: status, Labels: labels, LastSeen: now}
	}
	rows := make([]kuerystore.ClusterModel, 0, 10007)
	for i := range 10000 {
		rows = append(rows, row(fmt.Sprintf("other/edge-%05d", i), "other", "active"))
	}
	rows = append(rows,
		row(tenant+"/edge-2", tenant, "active"),
		row(tenant+"/edge-1", tenant, "active"),
		row(tenant+"/stale", tenant, "stale"),
		row(tenant+"/wrong-label", "other", "active"),
		row("wrong-prefix/edge", tenant, "active"),
		row("tenant_XYZ!a/wildcard", tenant, "active"),
		row(strings.ToUpper(tenant)+"/wrong-case", tenant, "active"),
	)
	if err := db.CreateInBatches(rows, 200).Error; err != nil {
		t.Fatal(err)
	}
	if db.Name() == "sqlite" {
		bad := row(tenant+"/malformed", tenant, "active")
		bad.Labels = []byte("invalid-json")
		if err := db.Create(&bad).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Installation must index existing data and be repeatable.
	for range 2 {
		if err := Ensure(db); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Exec("ANALYZE clusters").Error; err != nil {
		t.Fatal(err)
	}
	var fetched int64
	var query string
	var args []any
	if err := db.Callback().Query().After("gorm:query").Register("tenant-index-budget", func(tx *gorm.DB) {
		fetched += tx.RowsAffected
		query = tx.Statement.SQL.String()
		args = append([]any(nil), tx.Statement.Vars...)
	}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	edges, err := EdgeNames(ctx, db, tenant)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(edges, []string{"edge-1", "edge-2"}) || fetched != 2 {
		t.Fatalf("edges=%v rows fetched=%d; want two matching names and two fetched rows", edges, fetched)
	}
	if db.Name() == "sqlite" {
		var plan []struct{ Detail string }
		if err := db.Raw("EXPLAIN QUERY PLAN "+query, args...).Scan(&plan).Error; err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(fmt.Sprint(plan), indexName) {
			t.Fatalf("query did not use tenant index: %v", plan)
		}
		t.Logf("10,007+ rows: fetched=%d, query plan=%v", fetched, plan)
	} else {
		var plan []struct {
			Plan string `gorm:"column:QUERY PLAN"`
		}
		if err := db.Raw("EXPLAIN "+query, args...).Scan(&plan).Error; err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(fmt.Sprint(plan), indexName) {
			t.Fatalf("query did not use tenant index: %v", plan)
		}
		t.Logf("10,007 rows: fetched=%d, query plan=%v", fetched, plan)
	}
	missing, err := EdgeNames(ctx, db, "absent")
	if err != nil || len(missing) != 0 {
		t.Fatalf("absent tenant: names=%v error=%v", missing, err)
	}
}
