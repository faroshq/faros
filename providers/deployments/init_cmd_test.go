// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package main

import "testing"

func TestDeploymentClaims(t *testing.T) {
	if _, err := deploymentClaims("infra-hash", ""); err == nil {
		t.Fatal("empty Code identity hash must fail")
	}
	claims, err := deploymentClaims("", "code-hash")
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 2 || claims[0].Resource != "repositorycheckouts" || claims[1].Resource != "configmaps" || claims[1].IdentityHash != "" {
		t.Fatalf("claims without optional Infrastructure = %#v", claims)
	}
	claims, err = deploymentClaims("infra-hash", "code-hash")
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 3 || claims[0].Resource != "repositorycheckouts" || claims[1].Resource != "instances" || claims[2].Resource != "configmaps" {
		t.Fatalf("unexpected claims: %#v", claims)
	}
	if claims[0].Group != "code.faros.sh" || claims[0].IdentityHash != "code-hash" {
		t.Fatalf("unsynchronized Code claim: %#v", claims[0])
	}
	if claims[1].Group != "infrastructure.faros.sh" || claims[1].IdentityHash != "infra-hash" {
		t.Fatalf("unsynchronized Infrastructure claim: %#v", claims[1])
	}
	if len(claims[1].Verbs) != 5 || claims[1].Verbs[0] != "get" || claims[1].Verbs[4] != "delete" {
		t.Fatalf("Infrastructure apply verbs = %#v", claims[1].Verbs)
	}
	if claims[2].Group != "" || claims[2].IdentityHash != "" || len(claims[2].Verbs) != 7 {
		t.Fatalf("core ConfigMap claim = %#v", claims[2])
	}
}
