package workspaces

import (
	"context"

	conditionsv1alpha1 "github.com/kcp-dev/kcp/pkg/apis/third_party/conditions/apis/conditions/v1alpha1"
	"github.com/kcp-dev/kcp/pkg/apis/third_party/conditions/util/conditions"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
)

type workspaceRBACReconciler struct {
	getOrganization                  func(context.Context, *tenancyv1alpha1.Workspace) (*tenancyv1alpha1.Organization, error)
	getUserWithPrefixName            func(string) string
	createOrUpdateClusterRoleBinding func(context.Context, *tenancyv1alpha1.Organization, *tenancyv1alpha1.Workspace, *rbacv1.ClusterRoleBinding) error
}

func (r *workspaceRBACReconciler) reconcile(ctx context.Context, workspace *tenancyv1alpha1.Workspace) (reconcileStatus, error) {

	organization, err := r.getOrganization(ctx, workspace)
	if err != nil {
		return reconcileStatusStopAndRequeue, err
	}

	// Role binding to enable the cluster role
	subjects := []rbacv1.Subject{}
	for _, owner := range organization.Spec.OwnersRef {
		subjects = append(subjects, rbacv1.Subject{
			Kind: rbacv1.UserKind,
			Name: r.getUserWithPrefixName(owner.Email),
		})
	}

	clusterRoleBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "workspace-cluster-admins",
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     "cluster-admin",
		},
		Subjects: subjects,
	}

	err = r.createOrUpdateClusterRoleBinding(ctx, organization, workspace, clusterRoleBinding)
	if err != nil {
		return reconcileStatusStopAndRequeue, err
	}

	conditions.MarkTrue(workspace, conditionsv1alpha1.ReadyCondition)

	return reconcileStatusContinue, nil
}
