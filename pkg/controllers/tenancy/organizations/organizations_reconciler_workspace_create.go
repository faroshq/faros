package organizations

import (
	"context"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	conditionsv1alpha1 "github.com/kcp-dev/kcp/pkg/apis/third_party/conditions/apis/conditions/v1alpha1"
	"github.com/kcp-dev/kcp/pkg/apis/third_party/conditions/util/conditions"
)

type kcpWorkspaceReconciler struct {
	createOrganizationWorkspace func(ctx context.Context, organization *tenancyv1alpha1.Organization) error
}

func (r *kcpWorkspaceReconciler) reconcile(ctx context.Context, organization *tenancyv1alpha1.Organization) (reconcileStatus, error) {
	// create faros workspaces in the child clusters
	err := r.createOrganizationWorkspace(ctx, organization)
	if err != nil {
		return reconcileStatusStopAndRequeue, err
	}

	conditions.MarkTrue(organization, conditionsv1alpha1.ReadyCondition)

	return reconcileStatusContinue, nil
}
