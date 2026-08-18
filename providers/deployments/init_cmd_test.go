// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package main

import "testing"

func TestDeploymentClaims(t *testing.T) {
	if _, err := deploymentClaims("", "code-hash"); err == nil {
		t.Fatal("empty Infrastructure identity hash must fail")
	}
	if _, err := deploymentClaims("infra-hash", ""); err == nil {
		t.Fatal("empty Code identity hash must fail")
	}
	claims, err := deploymentClaims("infra-hash", "code-hash")
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 2 || claims[0].Resource != "instances" || claims[1].Resource != "repositorycheckouts" {
		t.Fatalf("unexpected claims: %#v", claims)
	}
	if claims[0].Group != "infrastructure.faros.sh" || claims[0].IdentityHash != "infra-hash" {
		t.Fatalf("unsynchronized Infrastructure claim: %#v", claims[0])
	}
	if claims[1].Group != "code.faros.sh" || claims[1].IdentityHash != "code-hash" {
		t.Fatalf("unsynchronized Code claim: %#v", claims[1])
	}
}
