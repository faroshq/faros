package workspaces

import (
	"context"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
)

type kcpWorkspaceReconciler struct {
	createWorkspace func(ctx context.Context, workspace *tenancyv1alpha1.Workspace) error
}

func (r *kcpWorkspaceReconciler) reconcile(ctx context.Context, workspace *tenancyv1alpha1.Workspace) (reconcileStatus, error) {
	// create faros workspaces in the child clusters
	err := r.createWorkspace(ctx, workspace)
	if err != nil {
		return reconcileStatusStopAndRequeue, err
	}

	return reconcileStatusContinue, nil
}
