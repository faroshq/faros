/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package changerequest

import (
	"context"
	"strings"
	"testing"

	codev1alpha1 "github.com/faroshq/provider-code/apis/v1alpha1"
)

func TestAfterApprovalRequiresAtLeastOneApproval(t *testing.T) {
	r := &Reconciler{}
	_, err := r.ensure(context.Background(), nil, &codev1alpha1.ChangeRequest{Spec: codev1alpha1.ChangeRequestSpec{MergePolicy: codev1alpha1.ChangeRequestMergePolicyAfterApproval}})
	if err == nil || !strings.Contains(err.Error(), "at least 1") {
		t.Fatalf("ensure error = %v", err)
	}
}
