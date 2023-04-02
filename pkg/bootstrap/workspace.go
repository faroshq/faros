package bootstrap

import (
	"context"
	"time"

	corev1alpha1 "github.com/kcp-dev/kcp/pkg/apis/core/v1alpha1"
	tenancyv1alpha1 "github.com/kcp-dev/kcp/pkg/apis/tenancy/v1alpha1"
	"github.com/kcp-dev/logicalcluster/v3"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
)

// CreateWorkspace creates a workspace for the given cluster.
func (b *bootstrap) CreateWorkspace(ctx context.Context, workspace string) error {
	klog.Infof("Creating workspace %s", workspace)
	clusterPath := logicalcluster.NewPath(workspace)

	parent, exists := clusterPath.Parent()
	if exists {
		if parent.String() != "root" {
			if err := b.CreateWorkspace(ctx, parent.String()); err != nil {
				return err
			}
		}
	}

	_, err := b.kcpClientSet.Cluster(parent).TenancyV1alpha1().Workspaces().Get(ctx, clusterPath.Base(), metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	structuredWorkspaceType := tenancyv1alpha1.WorkspaceTypeReference{}
	ws, err := b.kcpClientSet.Cluster(parent).TenancyV1alpha1().Workspaces().Create(ctx, &tenancyv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name: clusterPath.Base(),
		},
		Spec: tenancyv1alpha1.WorkspaceSpec{
			Type: structuredWorkspaceType,
		},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	if err := wait.PollImmediate(time.Millisecond*100, time.Second*5, func() (bool, error) {
		if _, err := b.kcpClientSet.Cluster(parent).TenancyV1alpha1().Workspaces().Get(ctx, clusterPath.Base(), metav1.GetOptions{}); err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		return true, nil
	}); err != nil {
		return err
	}

	// wait for being ready
	if ws.Status.Phase != corev1alpha1.LogicalClusterPhaseReady {
		if err := wait.PollImmediate(time.Millisecond*500, time.Second*5, func() (bool, error) {
			ws, err = b.kcpClientSet.Cluster(parent).TenancyV1alpha1().Workspaces().Get(ctx, clusterPath.Base(), metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			if ws.Status.Phase == corev1alpha1.LogicalClusterPhaseReady {
				return true, nil
			}
			return false, nil
		}); err != nil {
			return err
		}
	}

	return nil
}
