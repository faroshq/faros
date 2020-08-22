package controllers

// Copyright (c) Faros.sh.
// Licensed under the Apache License 2.0.

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	farosv1alpha1 "github.com/faroshq/faros/pkg/operator/apis/operator.faros.sh/v1alpha1"
	farosclient "github.com/faroshq/faros/pkg/operator/clientset/versioned/typed/operator.faros.sh/v1alpha1"
	"github.com/faroshq/faros/pkg/util/status"
	"github.com/faroshq/faros/pkg/util/version"
)

func SetCondition(ctx context.Context, arocli farosclient.OperatorV1alpha1Interface, cond *status.Condition) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cluster, err := arocli.Clusters().Get(ctx, farosv1alpha1.SingletonClusterName, metav1.GetOptions{})
		if err != nil {
			return err
		}

		changed := cluster.Status.Conditions.SetCondition(*cond)

		if setStaticStatus(cluster) {
			changed = true
		}

		if !changed {
			return nil
		}

		_, err = arocli.Clusters().UpdateStatus(ctx, cluster, metav1.UpdateOptions{})
		return err
	})
}

func setStaticStatus(cluster *farosv1alpha1.Cluster) (changed bool) {
	conditions := make(status.Conditions, 0, len(cluster.Status.Conditions))

	// cleanup any old conditions
	current := map[status.ConditionType]bool{}
	for _, ct := range farosv1alpha1.AllConditionTypes() {
		current[ct] = true
	}

	for _, cond := range cluster.Status.Conditions {
		if _, ok := current[cond.Type]; ok {
			conditions = append(conditions, cond)
		} else {
			changed = true
		}
	}

	cluster.Status.Conditions = conditions
	cluster.Status.OperatorVersion = version.GitCommit
	changed = true

	return
}
