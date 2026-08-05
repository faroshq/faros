// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package mcpserver

import (
	"context"
	"fmt"
	"sort"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/faroshq/provider-databricks/queryapi"
)

type tableSummary struct {
	Name    string `json:"name"`
	Catalog string `json:"catalog"`
	Schema  string `json:"schema"`
	Table   string `json:"table"`
}

type listTablesOutput struct {
	Tables []tableSummary `json:"tables"`
}

type describeTableInput struct {
	TableRef string `json:"tableRef" jsonschema:"Imported kedge Table resource name, e.g. order-history"`
}

type queryTableInput struct {
	ActionVersion string   `json:"actionVersion" jsonschema:"Pinned provider action contract version; currently v1"`
	TableRef      string   `json:"tableRef" jsonschema:"Imported kedge Table resource name, e.g. order-history"`
	Columns       []string `json:"columns,omitempty" jsonschema:"Optional exact column-name projection; SQL expressions are not accepted"`
	Limit         int      `json:"limit,omitempty" jsonschema:"Maximum 100 rows; defaults to 100"`
}

type queryTableOutput struct {
	ActionVersion string                 `json:"actionVersion"`
	TableRef      string                 `json:"tableRef"`
	Columns       []queryapi.QueryColumn `json:"columns"`
	Rows          []map[string]any       `json:"rows"`
	Truncated     bool                   `json:"truncated,omitempty"`
}

func registerTools(srv *mcp.Server, resolver queryapi.TableResolver, runners ...queryapi.QueryRunner) {
	var runner queryapi.QueryRunner
	if len(runners) > 0 {
		runner = runners[0]
	}
	safeRegister("list_tables", func() {
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "list_tables",
			Title:       "List imported Databricks tables",
			Description: "List Databricks tables already imported into this kedge workspace. Use this before asking the user to pick a tableRef.",
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
		}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listTablesOutput, error) {
			tables, err := resolver.ListTables(ctx)
			if err != nil {
				return nil, listTablesOutput{}, err
			}
			out := make([]tableSummary, 0, len(tables))
			for name, ref := range tables {
				out = append(out, tableSummary{Name: name, Catalog: ref.Catalog, Schema: ref.Schema, Table: ref.Table})
			}
			sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
			return nil, listTablesOutput{Tables: out}, nil
		})
	})

	safeRegister("describe_table", func() {
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "describe_table",
			Title:       "Describe an imported Databricks table",
			Description: "Describe one imported kedge Databricks Table resource by tableRef. The resource is a pointer plus cached schema, not table data.",
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in describeTableInput) (*mcp.CallToolResult, tableSummary, error) {
			ref, ok, err := resolver.GetTable(ctx, in.TableRef)
			if err != nil {
				return nil, tableSummary{}, err
			}
			if !ok {
				return nil, tableSummary{}, fmt.Errorf("tableRef %q not found", in.TableRef)
			}
			return nil, tableSummary{Name: in.TableRef, Catalog: ref.Catalog, Schema: ref.Schema, Table: ref.Table}, nil
		})
	})

	safeRegister("query_table", func() {
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "query_table",
			Title:       "Query an imported Databricks table",
			Description: "Run the versioned v1 bounded table query action. Supply only an imported tableRef, optional exact column names, and a limit; the provider resolves the warehouse, connection, and credentials.",
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true},
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in queryTableInput) (*mcp.CallToolResult, queryTableOutput, error) {
			request, err := queryapi.NormalizeQueryRequest(queryapi.QueryTableRequest{
				ActionVersion: in.ActionVersion,
				TableRef:      in.TableRef,
				Columns:       in.Columns,
				Limit:         in.Limit,
			})
			if err != nil {
				return nil, queryTableOutput{}, err
			}
			if runner == nil {
				return nil, queryTableOutput{}, fmt.Errorf("databricks table query is unavailable")
			}
			result, err := runner.QueryTable(ctx, request)
			if err != nil {
				return nil, queryTableOutput{}, err
			}
			result = queryapi.BoundQueryResult(result)
			return nil, queryTableOutput{
				ActionVersion: queryapi.ActionVersionV1,
				TableRef:      request.TableRef,
				Columns:       result.Columns,
				Rows:          result.Rows,
				Truncated:     result.Truncated,
			}, nil
		})
	})
}
