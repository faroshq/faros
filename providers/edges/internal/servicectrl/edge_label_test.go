// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package servicectrl

import (
	"context"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	edgesv1alpha1 "github.com/faroshq/provider-edges/apis/v1alpha1"
)

func TestServiceEdgeLabelProjection(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := edgesv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	service := &edgesv1alpha1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "api-created", Labels: map[string]string{"keep": "value"}},
		Spec:       edgesv1alpha1.ServiceSpec{EdgeRef: edgesv1alpha1.ServiceEdgeRef{Name: "edge-a", Kind: "LinuxServer"}},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(service).Build()
	ctx := context.Background()
	key := types.NamespacedName{Name: service.Name}
	if err := c.Get(ctx, key, service); err != nil {
		t.Fatal(err)
	}
	if err := ensureEdgeLabel(ctx, c, service); err != nil {
		t.Fatal(err)
	}
	if service.Labels[edgesv1alpha1.LabelEdge] != "edge-a" || service.Labels["keep"] != "value" {
		t.Fatalf("backfilled labels = %v", service.Labels)
	}
	rv := service.ResourceVersion
	if err := ensureEdgeLabel(ctx, c, service); err != nil || service.ResourceVersion != rv {
		t.Fatalf("unchanged projection wrote again: rv=%s->%s err=%v", rv, service.ResourceVersion, err)
	}

	// An API edit can change the relation while leaving an old derived label.
	service.Spec.EdgeRef.Name = "edge-" + strings.Repeat("a", 70)
	if err := c.Update(ctx, service); err != nil {
		t.Fatal(err)
	}
	if err := ensureEdgeLabel(ctx, c, service); err != nil {
		t.Fatal(err)
	}
	value := service.Labels[edgesv1alpha1.LabelEdge]
	// Shared vector with the portal query/mutation contract test.
	if value != "sha256-3f579909109053617b5284437ee86c91aa928373b4fd858936a97268" || len(validation.IsValidLabelValue(value)) != 0 {
		t.Fatalf("long edge relation is not a valid label: %q", value)
	}

	// A delayed projection must not overwrite a newer spec's label.
	delete(service.Labels, edgesv1alpha1.LabelEdge)
	if err := c.Update(ctx, service); err != nil {
		t.Fatal(err)
	}
	stale := service.DeepCopy()
	service.Spec.EdgeRef.Name = "edge-new"
	if err := c.Update(ctx, service); err != nil {
		t.Fatal(err)
	}
	if err := ensureEdgeLabel(ctx, c, stale); !apierrors.IsConflict(err) {
		t.Fatalf("stale projection error = %v, want conflict", err)
	}
	if err := ensureEdgeLabel(ctx, c, service); err != nil {
		t.Fatal(err)
	}
	if service.Labels[edgesv1alpha1.LabelEdge] != "edge-new" {
		t.Fatalf("new relation did not converge: %v", service.Labels)
	}
}
