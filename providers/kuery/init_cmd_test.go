// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"slices"
	"testing"
)

func TestKueryPermissionClaimsUseCurrentEdgesAPI(t *testing.T) {
	claims := kueryPermissionClaims("edges-current-identity")
	if len(claims) != 1 {
		t.Fatalf("claims = %#v, want one KubernetesCluster claim", claims)
	}
	claim := claims[0]
	if claim.Group != "edges.faros.sh" || claim.Resource != "kubernetesclusters" {
		t.Fatalf("claim target = %s/%s, want edges.faros.sh/kubernetesclusters", claim.Group, claim.Resource)
	}
	if !slices.Equal(claim.Verbs, []string{"get", "list", "watch"}) {
		t.Fatalf("claim verbs = %v, want get/list/watch", claim.Verbs)
	}
	if claim.IdentityHash != "edges-current-identity" {
		t.Fatalf("claim identityHash = %q, want current edges identity", claim.IdentityHash)
	}
}
