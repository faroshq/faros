// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// Package tenantindex owns the provider's indexed tenant-to-edge read model.
// The upstream Kuery store remains authoritative for cluster lifecycle rows.
package tenantindex

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const indexName = "faros_active_clusters_by_tenant"

func tenantExpression(db *gorm.DB) (string, error) {
	switch db.Name() {
	case "postgres":
		return "(labels ->> 'tenant')", nil
	case "sqlite":
		// Older/malformed rows were ignored by the former Go JSON decoder.
		// Preserve that behavior without making an invalid row break the index.
		return "(CASE WHEN json_valid(labels) THEN json_extract(labels, '$.tenant') END)", nil
	default:
		return "", fmt.Errorf("unsupported tenant index dialect %q", db.Name())
	}
}

// Ensure creates the provider-owned index after the upstream store migration.
// Existing rows are indexed too; no controller replay or data backfill is needed.
func Ensure(db *gorm.DB) error {
	expression, err := tenantExpression(db)
	if err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if db.Name() == "postgres" {
			// Serialize replicas creating the same index: IF NOT EXISTS alone can
			// still race on PostgreSQL's system catalog during first installation.
			if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", indexName).Error; err != nil {
				return err
			}
		}
		return tx.Exec("CREATE INDEX IF NOT EXISTS " + indexName + " ON clusters (" + expression + ", name) WHERE status = 'active'").Error
	})
}

// EdgeNames fetches only names belonging to the requested tenant. Both the
// label and the canonical name prefix must match; neither broad rows nor label
// JSON payloads are transferred to the API process.
func EdgeNames(ctx context.Context, db *gorm.DB, tenant string) ([]string, error) {
	expression, err := tenantExpression(db)
	if err != nil {
		return nil, err
	}
	prefix := tenant + "/"
	var names []string
	if err := db.WithContext(ctx).Table("clusters").
		Where("status = 'active'").Where(expression+" = ?", tenant).
		Where("substr(name, 1, length(?)) = ?", prefix, prefix).
		Order("name").Pluck("name", &names).Error; err != nil {
		return nil, fmt.Errorf("listing tenant edges: %w", err)
	}
	for i := range names {
		names[i] = strings.TrimPrefix(names[i], prefix)
	}
	return names, nil
}
