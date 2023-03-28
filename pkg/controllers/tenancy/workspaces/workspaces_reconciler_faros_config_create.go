package workspaces

import (
	"context"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
)

// farosConfigComputeReconciler injects workspace server config into default namespace
// for other components to consume
type farosConfigComputeReconciler struct {
	createFarosConfigMap func(context.Context, *tenancyv1alpha1.Workspace) error
}

func (r *farosConfigComputeReconciler) reconcile(ctx context.Context, workspace *tenancyv1alpha1.Workspace) (reconcileStatus, error) {
	err := r.createFarosConfigMap(ctx, workspace)
	if err != nil {
		return reconcileStatusStopAndRequeue, err
	}

	return reconcileStatusContinue, nil
}
