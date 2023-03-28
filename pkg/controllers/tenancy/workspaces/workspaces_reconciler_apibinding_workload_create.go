package workspaces

import (
	"context"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
)

type apiBindingComputeReconciler struct {
	createComputeAPIBinding func(context.Context, *tenancyv1alpha1.Workspace) error
}

func (r *apiBindingComputeReconciler) reconcile(ctx context.Context, workspace *tenancyv1alpha1.Workspace) (reconcileStatus, error) {
	err := r.createComputeAPIBinding(ctx, workspace)
	if err != nil {
		return reconcileStatusStopAndRequeue, err
	}

	return reconcileStatusContinue, nil
}
