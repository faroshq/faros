package cluster

// Copyright (c) Faros.sh.
// Licensed under the Apache License 2.0.

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"

	farosv1alpha1 "github.com/faroshq/faros/pkg/operator/apis/faros.sh/v1alpha1"
	farosclient "github.com/faroshq/faros/pkg/operator/clientset/faros.sh/v1alpha1/versioned/typed/faros.sh/v1alpha1"
	"github.com/faroshq/faros/pkg/util/status"
)

func setCondition(ctx context.Context, faroscli farosclient.FarosV1alpha1Interface, request ctrl.Request, cond *status.Condition) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		obj, err := faroscli.Clusters(request.Namespace).Get(ctx, request.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		changed := obj.Status.Conditions.SetCondition(*cond)

		if setStaticStatus(obj) {
			changed = true
		}

		if !changed {
			return nil
		}

		_, err = faroscli.Clusters(request.Namespace).UpdateStatus(ctx, obj, metav1.UpdateOptions{})
		return err
	})
}

func setStaticStatus(obj *farosv1alpha1.Cluster) (changed bool) {
	conditions := make(status.Conditions, 0, len(obj.Status.Conditions))

	// cleanup any old conditions
	current := map[status.ConditionType]bool{}
	for _, ct := range farosv1alpha1.AllConditionTypes() {
		current[ct] = true
	}

	for _, cond := range obj.Status.Conditions {
		if _, ok := current[cond.Type]; ok {
			conditions = append(conditions, cond)
		} else {
			changed = true
		}
	}

	obj.Status.Conditions = conditions
	changed = true

	return
}
