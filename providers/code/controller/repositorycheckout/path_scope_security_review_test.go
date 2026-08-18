// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package repositorycheckout

import (
	"strings"
	"testing"

	"github.com/faroshq/provider-code/backend"
)

func TestPathScopedFilesPreservesGlobalTreeTruncationSignal(t *testing.T) {
	const truncation = "(tree truncated by the host: repository has more entries than the tree API returns)"
	_, skipped, err := pathScopedFiles([]backend.RepositoryCommitFile{{Path: ".faros/release.yaml", Content: "release"}}, []string{truncation}, ".faros")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range skipped {
		if strings.Contains(item, "tree truncated") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("path-scoping dropped the global tree-truncation signal; Deployments could apply an incomplete revision")
	}
}
