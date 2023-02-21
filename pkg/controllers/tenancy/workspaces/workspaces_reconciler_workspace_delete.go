package workspaces

import (
	"context"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
)

type kcpWorkspaceDeleteReconciler struct {
	deleteWorkspace func(ctx context.Context, workspace *tenancyv1alpha1.Workspace) error
}

func (r *kcpWorkspaceDeleteReconciler) reconcile(ctx context.Context, workspace *tenancyv1alpha1.Workspace) (reconcileStatus, error) {
	// delete faros workspaces in the child clusters
	err := r.deleteWorkspace(ctx, workspace)
	if err != nil {
		return reconcileStatusStopAndRequeue, err
	}

	return reconcileStatusContinue, nil
}
