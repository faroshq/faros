// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package main

import (
	"bytes"
	"os"
	"testing"
)

func TestDeploymentSchemaModeAndDeletionPolicyContract(t *testing.T) {
	kcpSchema, err := os.ReadFile("config/kcp/apiresourceschema-deployments.deployments.faros.sh.yaml")
	if err != nil {
		t.Fatal(err)
	}
	chartSchema, err := os.ReadFile("deploy/chart/files/schemas/deployments.deployments.faros.sh.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(kcpSchema, chartSchema) {
		t.Fatal("deployment chart schema is not synchronized with the generated kcp schema")
	}
	for _, required := range [][]byte{
		[]byte("deletionPolicy:\n              default: Retain"),
		[]byte("- Retain\n              - Delete"),
		[]byte("mode:\n              default: production"),
		[]byte("- development\n              - production"),
	} {
		if !bytes.Contains(kcpSchema, required) {
			t.Fatalf("deployment schema does not contain %q", required)
		}
	}
}
