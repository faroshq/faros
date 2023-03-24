package workspaces

import (
	"context"

	conditionsv1alpha1 "github.com/kcp-dev/kcp/pkg/apis/third_party/conditions/apis/conditions/v1alpha1"
	"github.com/kcp-dev/kcp/pkg/apis/third_party/conditions/util/conditions"

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

	conditions.MarkTrue(workspace, conditionsv1alpha1.ReadyCondition)

	return reconcileStatusContinue, nil
}
