package organizations

import (
	"context"
	"fmt"

	"github.com/kcp-dev/kcp/pkg/apis/core"
	kcptenancyv1alpha1 "github.com/kcp-dev/kcp/pkg/apis/tenancy/v1alpha1"
	"github.com/kcp-dev/logicalcluster/v3"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
)

type reconcileStatus int

const (
	reconcileStatusStopAndRequeue reconcileStatus = iota
	reconcileStatusContinue
	reconcileStatusError
)

type reconciler interface {
	reconcile(ctx context.Context, organization *tenancyv1alpha1.Organization) (reconcileStatus, error)
}

func (c *Controller) reconcile(ctx context.Context, organization *tenancyv1alpha1.Organization) (bool, error) {
	var reconcilers []reconciler
	createReconcilers := []reconciler{
		&finalizerAddReconciler{ // must be first
			getFinalizerName: func() string {
				return finalizerName
			},
		},
		&kcpWorkspaceReconciler{ // must be second
			createOrganizationWorkspace: func(ctx context.Context, organization *tenancyv1alpha1.Organization) error {
				return c.createOrganizationWorkspace(ctx, organization)
			},
		},
		&kcpComputeWorkspaceReconciler{ // must be third
			createComputeWorkspaceWorkspace: func(ctx context.Context, organization *tenancyv1alpha1.Organization, name string) error {
				return c.createComputeWorkspaceWorkspace(ctx, organization, name)
			},
		},
	}

	deleteReconcilers := []reconciler{
		&kcpWorkspaceDeleteReconciler{
			deleteOrganizationWorkspace: func(ctx context.Context, organization *tenancyv1alpha1.Organization) error {
				return c.deleteOrganizationWorkspace(ctx, organization)
			},
		},
		&finalizerRemoveReconciler{
			getFinalizerName: func() string {
				return finalizerName
			},
		},
	}

	if !organization.DeletionTimestamp.IsZero() { //delete
		reconcilers = deleteReconcilers
	} else { //create or update
		reconcilers = createReconcilers
	}

	var errs []error

	requeue := false
	for _, r := range reconcilers {
		var err error
		var status reconcileStatus
		status, err = r.reconcile(ctx, organization)
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

func (c *Controller) createOrganizationWorkspace(ctx context.Context, organization *tenancyv1alpha1.Organization) error {
	logger := klog.FromContext(ctx)

	ws := &kcptenancyv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name: organization.Name,
		},
		Spec: kcptenancyv1alpha1.WorkspaceSpec{
			Type: kcptenancyv1alpha1.WorkspaceTypeReference{
				Name: "organization",
				Path: "root",
			},
		},
	}

	_, err := c.kcpClientSet.Cluster(core.RootCluster.Path()).TenancyV1alpha1().Workspaces().Get(ctx, ws.Name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		logger.Info("creating workspace", "workspace-name", organization.Name)
		_, err = c.kcpClientSet.Cluster(core.RootCluster.Path()).TenancyV1alpha1().Workspaces().Create(ctx, ws, metav1.CreateOptions{})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create Workspace: %s", err)
		}
	case err == nil:
		// workspaces are not updatable, but we need to deal with all the stuff bellow
	default:
		return fmt.Errorf("failed to get the Workspace %s", err)
	}

	return nil
}

func (c *Controller) createComputeWorkspaceWorkspace(ctx context.Context, organization *tenancyv1alpha1.Organization, name string) error {
	logger := klog.FromContext(ctx)

	// model for compute
	// TODO: move to custom workspace type
	ws := &kcptenancyv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: kcptenancyv1alpha1.WorkspaceSpec{
			Type: kcptenancyv1alpha1.WorkspaceTypeReference{
				Name: "universal",
				Path: "root",
			},
		},
	}

	_, err := c.kcpClientSet.Cluster(core.RootCluster.Path()).TenancyV1alpha1().Workspaces().Get(ctx, organization.Name, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get the Workspace %s", err)
	}

	orgCluster := logicalcluster.NewPath(core.RootCluster.Path().String() + ":" + organization.Name)

	_, err = c.kcpClientSet.Cluster(orgCluster).TenancyV1alpha1().Workspaces().Get(ctx, ws.Name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		logger.Info("creating compute workspace", "workspace-name", ws.Name)
		_, err = c.kcpClientSet.Cluster(orgCluster).TenancyV1alpha1().Workspaces().Create(ctx, ws, metav1.CreateOptions{})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create Workspace: %s", err)
		}
	case err == nil:
		// workspaces are not updatable, but we need to deal with all the stuff bellow
	default:
		return fmt.Errorf("failed to get the Workspace %s", err)
	}

	return nil

}

func (c *Controller) deleteOrganizationWorkspace(ctx context.Context, organization *tenancyv1alpha1.Organization) error {
	ws := &kcptenancyv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name: organization.Name,
		},
	}

	return c.kcpClientSet.Cluster(core.RootCluster.Path()).TenancyV1alpha1().Workspaces().Delete(ctx, ws.Name, metav1.DeleteOptions{})
}
