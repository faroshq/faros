package synctargets

import (
	"context"

	kcpworkloadv1alpha1 "github.com/kcp-dev/kcp/pkg/apis/workload/v1alpha1"

	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type finalizerAddReconciler struct {
	getFinalizerName func() string
}

func (r *finalizerAddReconciler) reconcile(ctx context.Context, synctarget *kcpworkloadv1alpha1.SyncTarget) (reconcileStatus, error) {
	if !controllerutil.ContainsFinalizer(synctarget, r.getFinalizerName()) {
		controllerutil.AddFinalizer(synctarget, r.getFinalizerName())
		return reconcileStatusStopAndRequeue, nil
	} else {
		return reconcileStatusContinue, nil
	}
}

type finalizerRemoveReconciler struct {
	getFinalizerName func() string
}

func (r *finalizerRemoveReconciler) reconcile(ctx context.Context, synctarget *kcpworkloadv1alpha1.SyncTarget) (reconcileStatus, error) {
	controllerutil.RemoveFinalizer(synctarget, r.getFinalizerName())
	return reconcileStatusContinue, nil
}
