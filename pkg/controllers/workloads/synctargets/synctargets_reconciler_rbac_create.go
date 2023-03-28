package synctargets

import (
	"context"

	kcpworkloadv1alpha1 "github.com/kcp-dev/kcp/pkg/apis/workload/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/kube-openapi/pkg/util/sets"
)

type syncTargetBootstrapReconciler struct {
	createOrUpdateServiceAccount   func(ctx context.Context, syncTarget *kcpworkloadv1alpha1.SyncTarget) (*corev1.ServiceAccount, error)
	createOrUpdateClusterRole      func(ctx context.Context, syncTarget *kcpworkloadv1alpha1.SyncTarget) error
	grantServiceAccountClusterRole func(ctx context.Context, syncTarget *kcpworkloadv1alpha1.SyncTarget, sa *corev1.ServiceAccount) (string, string, error)
	getResourcesForPermissions     func(ctx context.Context, syncTarget *kcpworkloadv1alpha1.SyncTarget) (sets.String, error)
	renderSyncerTemplate           func(ctx context.Context, syncTarget *kcpworkloadv1alpha1.SyncTarget, expectedResources sets.String, token, syncTargetID string) error
}

func (r *syncTargetBootstrapReconciler) reconcile(ctx context.Context, syncTarget *kcpworkloadv1alpha1.SyncTarget) (reconcileStatus, error) {
	sa, err := r.createOrUpdateServiceAccount(ctx, syncTarget)
	if err != nil {
		return reconcileStatusStopAndRequeue, err
	}

	if err := r.createOrUpdateClusterRole(ctx, syncTarget); err != nil {
		return reconcileStatusStopAndRequeue, err
	}

	token, syncTargetID, err := r.grantServiceAccountClusterRole(ctx, syncTarget, sa)
	if err != nil {
		return reconcileStatusStopAndRequeue, err
	}

	expectedResourcesForPermission, err := r.getResourcesForPermissions(ctx, syncTarget)
	if err != nil {
		return reconcileStatusStopAndRequeue, err
	}

	err = r.renderSyncerTemplate(ctx, syncTarget, expectedResourcesForPermission, token, syncTargetID)
	if err != nil {
		return reconcileStatusStopAndRequeue, err
	}

	return reconcileStatusContinue, nil
}
