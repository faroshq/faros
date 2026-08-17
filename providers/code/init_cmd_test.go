// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package main

import "testing"

func TestCodeClaimsRequireDeploymentsIdentity(t *testing.T) {
	if _, err := codeClaims(""); err == nil {
		t.Fatal("expected missing deployments identity to fail closed")
	}
	claims, err := codeClaims("deployments-hash")
	if err != nil {
		t.Fatalf("codeClaims returned error: %v", err)
	}
	if len(claims) != 3 {
		t.Fatalf("got %d claims, want secret + release + deployment", len(claims))
	}
	for _, claim := range claims[1:] {
		if claim.IdentityHash != "deployments-hash" {
			t.Fatalf("claim %s has identity %q", claim.Resource, claim.IdentityHash)
		}
	}
}
