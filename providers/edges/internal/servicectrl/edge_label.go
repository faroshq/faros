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
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	edgesv1alpha1 "github.com/faroshq/provider-edges/apis/v1alpha1"
)

// ensureEdgeLabel repairs the query projection for existing/API-created
// Services too. Discovery and the portal are not the only creation paths.
func ensureEdgeLabel(ctx context.Context, c client.Client, service *edgesv1alpha1.Service) error {
	want := edgesv1alpha1.ServiceEdgeLabelValue(service.Spec.EdgeRef.Name)
	if got, present := service.Labels[edgesv1alpha1.LabelEdge]; present && got == want {
		return nil
	}
	original := service.DeepCopy()
	if service.Labels == nil {
		service.Labels = make(map[string]string)
	}
	service.Labels[edgesv1alpha1.LabelEdge] = want
	if err := c.Patch(ctx, service, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("updating service edge label: %w", err)
	}
	return nil
}
