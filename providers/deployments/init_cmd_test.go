// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package main

import "testing"

func TestDeploymentClaims(t *testing.T) {
	if _, err := deploymentClaims(""); err == nil {
		t.Fatal("empty Infrastructure identity hash must fail")
	}
	claims, err := deploymentClaims("infra-hash")
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].Resource != "instances" {
		t.Fatalf("unexpected claims: %#v", claims)
	}
	for _, claim := range claims {
		if claim.Group != "infrastructure.faros.sh" || claim.IdentityHash != "infra-hash" {
			t.Fatalf("unsynchronized claim: %#v", claim)
		}
	}
}
