// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package main

import "testing"

// TestPermissionClaims guards the init-side claim declaration. It must stay in
// lockstep with manifest.yaml and the Helm CatalogEntry template — see the
// comment on permissionClaims.
func TestPermissionClaims(t *testing.T) {
	claims := permissionClaims()
	if len(claims) != 1 {
		t.Fatalf("claims = %#v, want exactly the secrets claim", claims)
	}
	c := claims[0]
	if c.Group != "" || c.Resource != "secrets" {
		t.Fatalf("claim = %#v, want built-in secrets resource", c)
	}
	want := []string{"get", "list", "watch", "create", "update", "patch", "delete"}
	if len(c.Verbs) != len(want) {
		t.Fatalf("verbs = %v, want %v", c.Verbs, want)
	}
	for i, v := range want {
		if c.Verbs[i] != v {
			t.Fatalf("verbs = %v, want %v", c.Verbs, want)
		}
	}
}
