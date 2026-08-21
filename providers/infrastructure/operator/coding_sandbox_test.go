/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package operator

import (
	"strings"
	"testing"

	infrav1alpha1 "github.com/faroshq/provider-infrastructure/apis/v1alpha1"
)

func TestValidateCodingSandboxConfig(t *testing.T) {
	spec := infrav1alpha1.InfrastructureProviderSpec{}
	if err := validateCodingSandboxConfig(spec); err != nil {
		t.Fatalf("disabled coding sandbox rejected: %v", err)
	}
	spec.CodingSandbox.Enabled = true
	if err := validateCodingSandboxConfig(spec); err == nil {
		t.Fatal("enabled coding sandbox without universal image was accepted")
	}
	spec.Development.Images = map[string]string{
		"universal": "ghcr.io/faroshq/faros-universal-dev@sha256:" + strings.Repeat("a", 64),
	}
	if err := validateCodingSandboxConfig(spec); err != nil {
		t.Fatalf("immutable universal image rejected: %v", err)
	}
}
