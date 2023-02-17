package organizations

import (
	"context"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type finalizerAddReconciler struct {
	getFinalizerName func() string
}

func (r *finalizerAddReconciler) reconcile(ctx context.Context, organization *tenancyv1alpha1.Organization) (reconcileStatus, error) {
	if !controllerutil.ContainsFinalizer(organization, r.getFinalizerName()) {
		controllerutil.AddFinalizer(organization, r.getFinalizerName())
		return reconcileStatusStopAndRequeue, nil
	} else {
		return reconcileStatusContinue, nil
	}
}

type finalizerRemoveReconciler struct {
	getFinalizerName func() string
}

func (r *finalizerRemoveReconciler) reconcile(ctx context.Context, organization *tenancyv1alpha1.Organization) (reconcileStatus, error) {
	controllerutil.RemoveFinalizer(organization, r.getFinalizerName())
	return reconcileStatusContinue, nil
}
