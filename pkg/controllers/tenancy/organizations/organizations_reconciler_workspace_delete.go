package organizations

import (
	"context"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
)

type kcpWorkspaceDeleteReconciler struct {
	deleteOrganizationWorkspace func(ctx context.Context, organization *tenancyv1alpha1.Organization) error
}

func (r *kcpWorkspaceDeleteReconciler) reconcile(ctx context.Context, organization *tenancyv1alpha1.Organization) (reconcileStatus, error) {
	// delete faros workspaces in the child clusters
	err := r.deleteOrganizationWorkspace(ctx, organization)
	if err != nil {
		return reconcileStatusStopAndRequeue, err
	}

	return reconcileStatusContinue, nil
}
