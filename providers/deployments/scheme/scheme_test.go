// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package scheme

import (
	"testing"

	apiskcpv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
)

func TestNewRegistersAPIExportEndpointSlice(t *testing.T) {
	if _, _, err := New().ObjectKinds(&apiskcpv1alpha1.APIExportEndpointSlice{}); err != nil {
		t.Fatalf("APIExportEndpointSlice is not registered: %v", err)
	}
}
