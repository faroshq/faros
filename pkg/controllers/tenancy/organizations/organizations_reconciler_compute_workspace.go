package organizations

import (
	"context"

	conditionsv1alpha1 "github.com/kcp-dev/kcp/pkg/apis/third_party/conditions/apis/conditions/v1alpha1"
	"github.com/kcp-dev/kcp/pkg/apis/third_party/conditions/util/conditions"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
)

var computeName = "compute"

type kcpComputeWorkspaceReconciler struct {
	createComputeWorkspaceWorkspace func(ctx context.Context, organization *tenancyv1alpha1.Organization, name string) error
}

func (r *kcpComputeWorkspaceReconciler) reconcile(ctx context.Context, organization *tenancyv1alpha1.Organization) (reconcileStatus, error) {
	// create faros workspaces in the child clusters
	err := r.createComputeWorkspaceWorkspace(ctx, organization, computeName)
	if err != nil {
		return reconcileStatusStopAndRequeue, err
	}

	conditions.MarkTrue(organization, conditionsv1alpha1.ReadyCondition)

	return reconcileStatusContinue, nil
}
