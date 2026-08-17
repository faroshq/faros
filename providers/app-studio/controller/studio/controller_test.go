// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package studio

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestInstanceReadyRequiresCurrentObservedGeneration(t *testing.T) {
	for _, tc := range []struct {
		name       string
		generation int64
		status     map[string]any
		want       bool
	}{
		{
			name:       "current Ready condition",
			generation: 3,
			status: map[string]any{
				"observedGeneration": int64(3),
				"conditions":         []any{map[string]any{"type": "Ready", "status": "True"}},
			},
			want: true,
		},
		{
			name:       "stale Ready condition",
			generation: 4,
			status: map[string]any{
				"observedGeneration": int64(3),
				"conditions":         []any{map[string]any{"type": "Ready", "status": "True"}},
			},
		},
		{
			name:       "missing generation with active state",
			generation: 4,
			status:     map[string]any{"state": "ACTIVE"},
		},
		{
			name:       "current active state",
			generation: 4,
			status:     map[string]any{"observedGeneration": int64(4), "state": "ACTIVE"},
			want:       true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inst := &unstructured.Unstructured{Object: map[string]any{
				"metadata": map[string]any{"generation": tc.generation},
				"status":   tc.status,
			}}
			if got := instanceReady(inst); got != tc.want {
				t.Fatalf("instanceReady = %v, want %v", got, tc.want)
			}
		})
	}
}
