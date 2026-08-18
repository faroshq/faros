// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package main

import "testing"

func TestCodeClaimsContainOnlyCredentialSecretAuthority(t *testing.T) {
	claims := codeClaims()
	if len(claims) != 1 {
		t.Fatalf("got %d claims, want only the credential Secret claim", len(claims))
	}
	if claims[0].Group != "" || claims[0].Resource != "secrets" || claims[0].IdentityHash != "" {
		t.Fatalf("unexpected Code claim: %#v", claims[0])
	}
}
