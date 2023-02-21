package workspaces

import (
	"context"
	"fmt"

	"github.com/kcp-dev/kcp/pkg/apis/core"
	kcptenancyv1alpha1 "github.com/kcp-dev/kcp/pkg/apis/tenancy/v1alpha1"
	"github.com/kcp-dev/logicalcluster/v3"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	farosclientset "github.com/faroshq/faros/pkg/client/clientset/versioned"
	"github.com/faroshq/faros/pkg/models"
)

type reconcileStatus int

const (
	reconcileStatusStopAndRequeue reconcileStatus = iota
	reconcileStatusContinue
	reconcileStatusError
)

type reconciler interface {
	reconcile(ctx context.Context, workspace *tenancyv1alpha1.Workspace) (reconcileStatus, error)
}

func (c *Controller) reconcile(ctx context.Context, cluster logicalcluster.Name, workspace *tenancyv1alpha1.Workspace) (bool, error) {
	var reconcilers []reconciler
	createReconcilers := []reconciler{
		&finalizerAddReconciler{ // must be first
			getFinalizerName: func() string {
				return finalizerName
			},
		},
		&kcpWorkspaceReconciler{ // must be second
			createWorkspace: func(ctx context.Context, workspace *tenancyv1alpha1.Workspace) error {
				return c.createWorkspace(ctx, workspace)
			},
		},
		&workspaceRBACReconciler{
			getOrganization: func(ctx context.Context, workspace *tenancyv1alpha1.Workspace) (*tenancyv1alpha1.Organization, error) {
				farosClient := c.farosClientSet.Cluster(cluster.Path())
				return c.getOrganization(ctx, farosClient, workspace)
			},
			getUserWithPrefixName: func(email string) string {
				return c.getUserWithPrefixName(email)
			},
			createOrUpdateClusterRoleBinding: func(ctx context.Context, org *tenancyv1alpha1.Organization, ws *tenancyv1alpha1.Workspace, crb *rbacv1.ClusterRoleBinding) error {
				return c.createOrUpdateClusterRoleBinding(ctx, org, ws, crb)
			},
		},
	}

	deleteReconcilers := []reconciler{
		&kcpWorkspaceDeleteReconciler{
			deleteWorkspace: func(ctx context.Context, workspace *tenancyv1alpha1.Workspace) error {
				return c.deleteWorkspace(ctx, workspace)
			},
		},
		&finalizerRemoveReconciler{
			getFinalizerName: func() string {
				return finalizerName
			},
		},
	}

	if !workspace.DeletionTimestamp.IsZero() { //delete
		reconcilers = deleteReconcilers
	} else { //create or update
		reconcilers = createReconcilers
	}

	var errs []error

	requeue := false
	for _, r := range reconcilers {
		var err error
		var status reconcileStatus
		status, err = r.reconcile(ctx, workspace)
		if err != nil {
			errs = append(errs, err)
		}
		if status == reconcileStatusStopAndRequeue {
			requeue = true
			break
		}
	}

	return requeue, utilerrors.NewAggregate(errs)
}

func (c *Controller) createWorkspace(ctx context.Context, workspace *tenancyv1alpha1.Workspace) error {
	logger := klog.FromContext(ctx)

	name := workspace.Labels[models.LabelWorkspace]

	ws := &kcptenancyv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: kcptenancyv1alpha1.WorkspaceSpec{
			Type: kcptenancyv1alpha1.WorkspaceTypeReference{
				Name: "faros",
				Path: "root",
			},
		},
	}

	orgCluster := logicalcluster.NewPath(core.RootCluster.Path().String() + ":" + workspace.Spec.OrganizationRef.Name)

	created, err := c.kcpClientSet.Cluster(orgCluster).TenancyV1alpha1().Workspaces().Get(ctx, ws.Name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		logger.Info("creating workspace", "workspace-name", workspace.Name)
		_, err = c.kcpClientSet.Cluster(orgCluster).TenancyV1alpha1().Workspaces().Create(ctx, ws, metav1.CreateOptions{})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create Workspace: %s", err)
		}
	case err == nil:
		// workspaces are not updatable, but we need to deal with all the stuff bellow
	default:
		return fmt.Errorf("failed to get the Workspace %s", err)
	}

	workspace.Status.WorkspaceURL = created.Spec.URL
	return nil
}

func (c *Controller) deleteWorkspace(ctx context.Context, workspace *tenancyv1alpha1.Workspace) error {
	orgCluster := logicalcluster.NewPath(core.RootCluster.Path().String() + ":" + workspace.Spec.OrganizationRef.Name)

	return c.kcpClientSet.Cluster(orgCluster).TenancyV1alpha1().Workspaces().Delete(ctx, workspace.Labels[models.LabelWorkspace], metav1.DeleteOptions{})
}

func (c *Controller) getUserWithPrefixName(email string) string {
	return fmt.Sprintf("%s%s", c.config.APIConfig.OIDCUserPrefix, email)
}

func (c *Controller) getOrganization(ctx context.Context, farosClient farosclientset.Interface, workspace *tenancyv1alpha1.Workspace) (*tenancyv1alpha1.Organization, error) {
	return farosClient.TenancyV1alpha1().Organizations().Get(ctx, workspace.Spec.OrganizationRef.Name, metav1.GetOptions{})
}

func (c *Controller) createOrUpdateClusterRoleBinding(ctx context.Context, org *tenancyv1alpha1.Organization, workspace *tenancyv1alpha1.Workspace, crb *rbacv1.ClusterRoleBinding) error {
	name := workspace.Labels[models.LabelWorkspace]
	workspaceCluster := logicalcluster.NewPath(core.RootCluster.Path().String() + ":" + workspace.Spec.OrganizationRef.Name + ":" + name)

	currentClusterRoleBinding, err := c.coreClientSet.RbacV1().ClusterRoleBindings().Cluster(workspaceCluster).Get(ctx, crb.Name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		_, err := c.coreClientSet.RbacV1().ClusterRoleBindings().Cluster(workspaceCluster).Create(ctx, crb, metav1.CreateOptions{})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create the ClusterRoleBindings %s", err)
		}
	case err == nil:
		currentClusterRoleBinding.RoleRef = crb.RoleRef
		currentClusterRoleBinding.Subjects = crb.Subjects
		currentClusterRoleBinding.ResourceVersion = ""
		_, err := c.coreClientSet.RbacV1().ClusterRoleBindings().Cluster(workspaceCluster).Update(ctx, currentClusterRoleBinding, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update the ClusterRoleBindings %s", err)
		}
	default:
		return fmt.Errorf("failed to create the ClusterRoleBindings %s", err)
	}

	return nil
}
