package synctargets

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	jsonpatch "github.com/evanphx/json-patch"
	kcpapiresourcev1alpha1 "github.com/kcp-dev/kcp/pkg/apis/apiresource/v1alpha1"
	kcpworkloadv1alpha1 "github.com/kcp-dev/kcp/pkg/apis/workload/v1alpha1"
	"github.com/kcp-dev/logicalcluster/v3"
	"github.com/martinlindhe/base36"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog/v2"
	"k8s.io/kube-openapi/pkg/util/sets"
)

type reconcileStatus int

const (
	reconcileStatusStopAndRequeue reconcileStatus = iota
	reconcileStatusContinue
	reconcileStatusError
)

type reconciler interface {
	reconcile(ctx context.Context, syncTarget *kcpworkloadv1alpha1.SyncTarget) (reconcileStatus, error)
}

func (c *Controller) reconcile(ctx context.Context, syncTarget *kcpworkloadv1alpha1.SyncTarget, cluster logicalcluster.Path) (bool, error) {
	var reconcilers []reconciler

	// static metadata for all objects created by this controller
	syncTargetOwnerReferences := []metav1.OwnerReference{{
		APIVersion: kcpworkloadv1alpha1.SchemeGroupVersion.String(),
		Kind:       "SyncTarget",
		Name:       syncTarget.Name,
		UID:        syncTarget.UID,
	}}

	syncerID := getSyncerID(syncTarget)
	namespace := "default"

	createReconcilers := []reconciler{
		&finalizerAddReconciler{ // must be first
			getFinalizerName: func() string {
				return finalizerName
			},
		},
		&syncTargetBootstrapReconciler{
			createOrUpdateServiceAccount: func(ctx context.Context, syncTarget *kcpworkloadv1alpha1.SyncTarget) (*corev1.ServiceAccount, error) {
				return c.createOrUpdateServiceAccount(ctx, cluster, syncTargetOwnerReferences, syncTarget.Name, syncerID, namespace)
			},
			createOrUpdateClusterRole: func(ctx context.Context, syncTarget *kcpworkloadv1alpha1.SyncTarget) error {
				return c.createOrUpdateClusterRole(ctx, cluster, syncTargetOwnerReferences, syncTarget.Name, syncerID, namespace)
			},
			grantServiceAccountClusterRole: func(ctx context.Context, syncTarget *kcpworkloadv1alpha1.SyncTarget, sa *corev1.ServiceAccount) (string, string, error) {
				return c.grantServiceAccountClusterRole(ctx, sa, cluster, syncTargetOwnerReferences, syncTarget.Name, syncerID, namespace)
			},
			getResourcesForPermissions: func(ctx context.Context, syncTarget *kcpworkloadv1alpha1.SyncTarget) (sets.String, error) {
				return c.getResourcesForPermissions(ctx, cluster, syncTarget)
			},
			renderSyncerTemplate: func(ctx context.Context, syncTarget *kcpworkloadv1alpha1.SyncTarget, expectedResourcesForPermission sets.String, token, syncTargetID string) error {
				return c.renderSyncerTemplate(ctx, cluster, syncTarget, expectedResourcesForPermission, syncTargetOwnerReferences, token, syncTargetID)
			},
		},
	}

	deleteReconcilers := []reconciler{
		&finalizerRemoveReconciler{
			getFinalizerName: func() string {
				return finalizerName
			},
		},
	}

	if !syncTarget.DeletionTimestamp.IsZero() { //delete
		reconcilers = deleteReconcilers
	} else { //create or update
		reconcilers = createReconcilers
	}

	var errs []error

	requeue := false
	for _, r := range reconcilers {
		var err error
		var status reconcileStatus
		status, err = r.reconcile(ctx, syncTarget)
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

func (c *Controller) createOrUpdateServiceAccount(ctx context.Context, cluster logicalcluster.Path, syncTargetOwnerReferences []metav1.OwnerReference, syncTargetName, syncerID, namespace string) (*corev1.ServiceAccount, error) {
	sa, err := c.coreClientSet.CoreV1().Cluster(cluster).ServiceAccounts(namespace).Get(ctx, syncerID, metav1.GetOptions{})

	switch {
	case apierrors.IsNotFound(err):
		klog.V(4).Infof("Creating service account %q", syncerID)
		if sa, err = c.coreClientSet.CoreV1().Cluster(cluster).ServiceAccounts(namespace).Create(ctx, &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:            syncerID,
				OwnerReferences: syncTargetOwnerReferences,
			},
		}, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("failed to create ServiceAccount %s|%s/%s: %w", syncTargetName, namespace, syncerID, err)
		}
	case err == nil:
		oldData, err := json.Marshal(corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				OwnerReferences: sa.OwnerReferences,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to marshal old data for ServiceAccount %s|%s/%s: %w", syncTargetName, namespace, syncerID, err)
		}

		newData, err := json.Marshal(corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				UID:             sa.UID,
				ResourceVersion: sa.ResourceVersion,
				OwnerReferences: mergeOwnerReference(sa.ObjectMeta.OwnerReferences, syncTargetOwnerReferences),
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to marshal new data for ServiceAccount %s|%s/%s: %w", syncTargetName, namespace, syncerID, err)
		}

		patchBytes, err := jsonpatch.CreateMergePatch(oldData, newData)
		if err != nil {
			return nil, fmt.Errorf("failed to create patch for ServiceAccount %s|%s/%s: %w", syncTargetName, namespace, syncerID, err)
		}

		klog.V(4).Info("Patching service account %q", syncerID)
		if sa, err = c.coreClientSet.CoreV1().Cluster(cluster).ServiceAccounts(namespace).Patch(ctx, sa.Name, types.MergePatchType, patchBytes, metav1.PatchOptions{}); err != nil {
			return nil, fmt.Errorf("failed to patch ServiceAccount %s|%s/%s: %w", syncTargetName, syncerID, namespace, err)
		}
	default:
		return nil, fmt.Errorf("failed to get the ServiceAccount %s|%s/%s: %w", syncTargetName, syncerID, namespace, err)
	}
	return sa, nil
}

func (c *Controller) createOrUpdateClusterRole(ctx context.Context, cluster logicalcluster.Path, syncTargetOwnerReferences []metav1.OwnerReference, syncTargetName, syncerID, namespace string) error {
	// Create a cluster role that provides the syncer the minimal permissions
	// required by KCP to manage the sync target, and by the syncer virtual
	// workspace to sync.
	rules := []rbacv1.PolicyRule{
		{
			Verbs:         []string{"sync"},
			APIGroups:     []string{kcpworkloadv1alpha1.SchemeGroupVersion.Group},
			ResourceNames: []string{syncTargetName},
			Resources:     []string{"synctargets"},
		},
		{
			Verbs:         []string{"get"},
			APIGroups:     []string{kcpworkloadv1alpha1.SchemeGroupVersion.Group},
			ResourceNames: []string{syncTargetName},
			Resources:     []string{"synctargets/tunnel"},
		},
		{
			Verbs:         []string{"get", "list", "watch"},
			APIGroups:     []string{kcpworkloadv1alpha1.SchemeGroupVersion.Group},
			Resources:     []string{"synctargets"},
			ResourceNames: []string{syncTargetName},
		},
		{
			Verbs:         []string{"update", "patch"},
			APIGroups:     []string{kcpworkloadv1alpha1.SchemeGroupVersion.Group},
			ResourceNames: []string{syncTargetName},
			Resources:     []string{"synctargets/status"},
		},
		{
			Verbs:     []string{"get", "create", "update", "delete", "list", "watch"},
			APIGroups: []string{kcpapiresourcev1alpha1.SchemeGroupVersion.Group},
			Resources: []string{"apiresourceimports"},
		},
		{
			Verbs:           []string{"access"},
			NonResourceURLs: []string{"/"},
		},
	}

	cr, err := c.coreClientSet.RbacV1().Cluster(cluster).ClusterRoles().Get(ctx,
		syncerID,
		metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		klog.V(4).Infof("Creating cluster role %q to give service account", syncerID)
		if _, err = c.coreClientSet.RbacV1().Cluster(cluster).ClusterRoles().Create(ctx, &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				Name:            syncerID,
				OwnerReferences: syncTargetOwnerReferences,
			},
			Rules: rules,
		}, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			return err
		}
	case err == nil:
		oldData, err := json.Marshal(rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				OwnerReferences: cr.OwnerReferences,
			},
			Rules: cr.Rules,
		})
		if err != nil {
			return fmt.Errorf("failed to marshal old data for ClusterRole %s|%s: %w", syncTargetName, syncerID, err)
		}

		newData, err := json.Marshal(rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{
				UID:             cr.UID,
				ResourceVersion: cr.ResourceVersion,
				OwnerReferences: mergeOwnerReference(cr.OwnerReferences, syncTargetOwnerReferences),
			},
			Rules: rules,
		})
		if err != nil {
			return fmt.Errorf("failed to marshal new data for ClusterRole %s|%s: %w", syncTargetName, syncerID, err)
		}

		patchBytes, err := jsonpatch.CreateMergePatch(oldData, newData)
		if err != nil {
			return fmt.Errorf("failed to create patch for ClusterRole %s|%s: %w", syncTargetName, syncerID, err)
		}

		klog.V(4).Infof("Updating cluster role %q ", syncerID)
		if _, err = c.coreClientSet.RbacV1().Cluster(cluster).ClusterRoles().Patch(ctx, cr.Name, types.MergePatchType, patchBytes, metav1.PatchOptions{}); err != nil {
			return fmt.Errorf("failed to patch ClusterRole %s|%s/%s: %w", syncTargetName, syncerID, namespace, err)
		}
	default:
		return err
	}

	return nil
}

func (c *Controller) grantServiceAccountClusterRole(ctx context.Context, sa *corev1.ServiceAccount, cluster logicalcluster.Path, syncTargetOwnerReferences []metav1.OwnerReference, syncTargetName, syncerID, namespace string) (string, string, error) {
	// Create a cluster role binding that grants the syncer service account
	// Grant the service account the role created just above in the workspace
	subjects := []rbacv1.Subject{{
		Kind:      "ServiceAccount",
		Name:      syncerID,
		Namespace: namespace,
	}}
	roleRef := rbacv1.RoleRef{
		Kind:     "ClusterRole",
		Name:     syncerID,
		APIGroup: "rbac.authorization.k8s.io",
	}

	_, err := c.coreClientSet.RbacV1().Cluster(cluster).ClusterRoleBindings().Get(ctx,
		syncerID,
		metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return "", "", err
	}
	if err == nil {
		if err := c.coreClientSet.RbacV1().Cluster(cluster).ClusterRoleBindings().Delete(ctx, syncerID, metav1.DeleteOptions{}); err != nil {
			return "", "", err
		}
	}

	klog.V(4).Infof("Creating or updating cluster role binding %q to bind service account %q to cluster role %q.\n", syncerID, syncerID, syncerID)
	if _, err = c.coreClientSet.RbacV1().Cluster(cluster).ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:            syncerID,
			OwnerReferences: syncTargetOwnerReferences,
		},
		Subjects: subjects,
		RoleRef:  roleRef,
	}, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return "", "", err
	}

	// Wait for the service account to be updated with the name of the token secret
	tokenSecretName := ""
	err = wait.PollImmediateWithContext(ctx, 100*time.Millisecond, 20*time.Second, func(ctx context.Context) (bool, error) {
		serviceAccount, err := c.coreClientSet.CoreV1().Cluster(cluster).ServiceAccounts(namespace).Get(ctx, sa.Name, metav1.GetOptions{})
		if err != nil {
			klog.Errorf("failed to retrieve ServiceAccount: %s", err)
			return false, nil
		}
		if len(serviceAccount.Secrets) == 0 {
			return false, nil
		}
		tokenSecretName = serviceAccount.Secrets[0].Name
		return true, nil
	})
	if err != nil {
		return "", "", fmt.Errorf("timed out waiting for token secret name to be set on ServiceAccount %s/%s", namespace, sa.Name)
	}

	// Retrieve the token that the syncer will use to authenticate to kcp
	tokenSecret, err := c.coreClientSet.CoreV1().Cluster(cluster).Secrets(namespace).Get(ctx, tokenSecretName, metav1.GetOptions{})
	if err != nil {
		return "", "", fmt.Errorf("failed to retrieve Secret: %w", err)
	}
	saTokenBytes := tokenSecret.Data["token"]
	if len(saTokenBytes) == 0 {
		return "", "", fmt.Errorf("token secret %s/%s is missing a value for `token`", namespace, tokenSecretName)
	}

	return string(saTokenBytes), syncerID, nil
}

func (c *Controller) getResourcesForPermissions(ctx context.Context, cluster logicalcluster.Path, syncTarget *kcpworkloadv1alpha1.SyncTarget) (sets.String, error) {

	// TODO: Customizable list of resources to sync
	expectedResourcesForPermission := sets.NewString()
	// secrets and configmaps are always needed.
	expectedResourcesForPermission.Insert("secrets", "configmaps")

	if len(syncTarget.Spec.SupportedAPIExports) == 1 &&
		syncTarget.Spec.SupportedAPIExports[0].Export == "kubernetes" &&
		(len(syncTarget.Spec.SupportedAPIExports[0].Path) == 0 ||
			syncTarget.Spec.SupportedAPIExports[0].Path == cluster.String()) {
		return nil, fmt.Errorf("syncTarget is not yet reporting its supported API exports")
	}

	if len(syncTarget.Status.SyncedResources) == 0 {
		return nil, fmt.Errorf("syncTarget is not yet reporting its synced resources")
	}
	for _, rs := range syncTarget.Status.SyncedResources {
		expectedResourcesForPermission.Insert(fmt.Sprintf("%s.%s", rs.Resource, rs.Group))
	}

	return expectedResourcesForPermission, nil
}

// getSyncerID returns a unique ID for a syncer derived from the name and its UID. It's
// a valid DNS segment and can be used as namespace or object names.
func getSyncerID(syncTarget *kcpworkloadv1alpha1.SyncTarget) string {
	syncerHash := sha256.Sum224([]byte(syncTarget.UID))
	base36hash := strings.ToLower(base36.EncodeBytes(syncerHash[:]))
	return fmt.Sprintf("faros-syncer-%s-%s", syncTarget.Name, base36hash[:8])
}

// mergeOwnerReference: merge a slice of ownerReference with a given ownerReferences.
func mergeOwnerReference(ownerReferences, newOwnerReferences []metav1.OwnerReference) []metav1.OwnerReference {
	var merged []metav1.OwnerReference

	merged = append(merged, ownerReferences...)

	for _, ownerReference := range newOwnerReferences {
		found := false
		for _, mergedOwnerReference := range merged {
			if mergedOwnerReference.UID == ownerReference.UID {
				found = true
				break
			}
		}
		if !found {
			merged = append(merged, ownerReference)
		}
	}

	return merged
}
