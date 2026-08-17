/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package syncedsecret reconciles SyncedSecret CRs: it reads the addressed
// material from the referenced SecretStore's backend and projects it into a
// workspace Secret, re-reading on spec.refreshInterval so rotated values
// propagate. The projected Secret carries an ownerReference back to the
// SyncedSecret plus the provider's managed-by labels; the controller refuses
// to overwrite a Secret it does not manage.
package syncedsecret

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	secretsv1alpha1 "github.com/faroshq/provider-secrets/apis/v1alpha1"
	"github.com/faroshq/provider-secrets/backend"
	"github.com/faroshq/provider-secrets/controller/shared"
)

// defaultRefreshInterval applies when spec.refreshInterval is absent (the CRD
// also defaults it server-side) and caps how stale a projection can go.
const defaultRefreshInterval = time.Hour

// minRefreshInterval floors tenant-chosen intervals so a mis-typed "1ms"
// cannot turn the controller into a hot loop against the external store.
const minRefreshInterval = 10 * time.Second

// Reconciler projects SyncedSecrets into workspace Secrets.
type Reconciler struct {
	Manager  mcmanager.Manager
	Backends *backend.Registry
}

// SetupWithManager wires the reconciler into the multicluster manager. The
// projected Secret is owned, so drift (someone deleting or editing it)
// re-enqueues the SyncedSecret immediately instead of waiting a full refresh
// interval.
func (r *Reconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.Manager = mgr
	return mcbuilder.ControllerManagedBy(mgr).
		Named("secrets-syncedsecret").
		For(&secretsv1alpha1.SyncedSecret{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}

// Reconcile syncs one SyncedSecret.
func (r *Reconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := klog.FromContext(ctx).WithValues("syncedsecret", req.NamespacedName, "cluster", req.ClusterName)

	c, err := shared.ClusterClient(ctx, r.Manager, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	var sync secretsv1alpha1.SyncedSecret
	if err := c.Get(ctx, req.NamespacedName, &sync); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Deletion: remove the projected Secret (the ownerReference would GC it
	// too, but be deterministic about it), then release the finalizer.
	if !sync.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&sync, secretsv1alpha1.FinalizerSyncedSecret) {
			if err := r.deleteProjected(ctx, c, &sync); err != nil {
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(&sync, secretsv1alpha1.FinalizerSyncedSecret)
			if err := c.Update(ctx, &sync); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	if controllerutil.AddFinalizer(&sync, secretsv1alpha1.FinalizerSyncedSecret) {
		if err := c.Update(ctx, &sync); err != nil {
			return ctrl.Result{}, err
		}
		// Re-reconcile on the fresh object after the finalizer write.
		return ctrl.Result{Requeue: true}, nil
	}

	if len(sync.Spec.Data) == 0 && len(sync.Spec.DataFrom) == 0 {
		return r.fail(ctx, c, &sync, secretsv1alpha1.ReasonInvalidSpec, "spec addresses nothing: set data and/or dataFrom", 0)
	}

	store, err := shared.ResolveStore(ctx, c, sync.Spec.StoreRef.Name)
	if err != nil {
		return r.fail(ctx, c, &sync, secretsv1alpha1.ReasonStoreNotFound, err.Error(), shared.DependencyRetryAfter)
	}

	b, ok := r.Backends.Get(string(store.Spec.Backend))
	if !ok {
		return r.fail(ctx, c, &sync, secretsv1alpha1.ReasonBackendNotConfigured, fmt.Sprintf("no backend registered for %q", store.Spec.Backend), 0)
	}

	cred, err := shared.ResolveCredential(ctx, c, store)
	if err != nil {
		return r.fail(ctx, c, &sync, secretsv1alpha1.ReasonCredentialUnavailable, err.Error(), shared.DependencyRetryAfter)
	}

	data, err := assemble(ctx, b, store, cred, &sync.Spec)
	if err != nil {
		reason := backend.ClassifyError(err)
		if reason == secretsv1alpha1.ReasonValidationFailed {
			reason = secretsv1alpha1.ReasonSyncFailed
		}
		return r.fail(ctx, c, &sync, reason, err.Error(), refreshInterval(&sync.Spec))
	}

	secretName, err := r.project(ctx, c, &sync, data)
	if err != nil {
		return r.fail(ctx, c, &sync, secretsv1alpha1.ReasonTargetConflict, err.Error(), 0)
	}

	now := metav1.Now()
	sync.Status.ObservedGeneration = sync.Generation
	sync.Status.SecretName = secretName
	sync.Status.LastSyncTime = &now
	sync.Status.SyncedVersion = HashData(data)
	sync.Status.SyncedKeys = int32(len(data))
	shared.SetCondition(&sync.Status.Conditions, secretsv1alpha1.ConditionReady, metav1.ConditionTrue, secretsv1alpha1.ReasonReady, fmt.Sprintf("projected %d key(s) into secret %q", len(data), secretName), sync.Generation)
	if err := c.Status().Update(ctx, &sync); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("SyncedSecret projected", "secret", secretName, "keys", len(data))
	// Re-read on the refresh interval so external rotation propagates.
	return ctrl.Result{RequeueAfter: refreshInterval(&sync.Spec)}, nil
}

// project upserts the projected Secret and returns its name. It refuses to
// touch a pre-existing Secret that does not carry the provider's managed-by
// label for this SyncedSecret — a tenant's hand-placed Secret must never be
// silently clobbered.
func (r *Reconciler) project(ctx context.Context, c client.Client, sync *secretsv1alpha1.SyncedSecret, data map[string][]byte) (string, error) {
	name := targetName(sync)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: sync.Namespace,
		},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, c, secret, func() error {
		if secret.UID != "" && !ownedBy(secret, sync) {
			return fmt.Errorf("secret %s/%s exists and is not managed by this SyncedSecret; refusing to overwrite", sync.Namespace, name)
		}
		if secret.Labels == nil {
			secret.Labels = map[string]string{}
		}
		secret.Labels[secretsv1alpha1.ManagedByLabel] = secretsv1alpha1.ManagedByLabelValue
		secret.Labels[secretsv1alpha1.OwnerNameLabel] = sync.Name
		secret.Type = corev1.SecretTypeOpaque
		secret.Data = data
		return controllerutil.SetControllerReference(sync, secret, c.Scheme())
	})
	if err != nil {
		return "", err
	}
	if op != controllerutil.OperationResultNone {
		klog.FromContext(ctx).V(2).Info("projected secret written", "secret", name, "op", op)
	}
	return name, nil
}

// deleteProjected removes the projected Secret if it is still ours.
func (r *Reconciler) deleteProjected(ctx context.Context, c client.Client, sync *secretsv1alpha1.SyncedSecret) error {
	name := sync.Status.SecretName
	if name == "" {
		name = targetName(sync)
	}
	var secret corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{Namespace: sync.Namespace, Name: name}, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if !ownedBy(&secret, sync) {
		// Not ours (label lost or replaced by hand): leave it alone.
		return nil
	}
	if err := c.Delete(ctx, &secret); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// ownedBy reports whether the Secret is managed by exactly this SyncedSecret.
func ownedBy(secret *corev1.Secret, sync *secretsv1alpha1.SyncedSecret) bool {
	return secret.Labels[secretsv1alpha1.ManagedByLabel] == secretsv1alpha1.ManagedByLabelValue &&
		secret.Labels[secretsv1alpha1.OwnerNameLabel] == sync.Name
}

// targetName resolves the projected Secret's name.
func targetName(sync *secretsv1alpha1.SyncedSecret) string {
	if sync.Spec.Target != nil && sync.Spec.Target.Name != "" {
		return sync.Spec.Target.Name
	}
	return sync.Name
}

// refreshInterval resolves the effective refresh cadence.
func refreshInterval(spec *secretsv1alpha1.SyncedSecretSpec) time.Duration {
	d := defaultRefreshInterval
	if spec.RefreshInterval != nil && spec.RefreshInterval.Duration > 0 {
		d = spec.RefreshInterval.Duration
	}
	if d < minRefreshInterval {
		d = minRefreshInterval
	}
	return d
}

// fail records a not-ready status and swallows the error (the bad state is on
// the CR). A non-zero requeueAfter re-polls a recoverable cause; zero leaves
// recovery to the next spec change or a watched-object event.
func (r *Reconciler) fail(ctx context.Context, c client.Client, sync *secretsv1alpha1.SyncedSecret, reason, msg string, requeueAfter time.Duration) (ctrl.Result, error) {
	sync.Status.ObservedGeneration = sync.Generation
	shared.SetCondition(&sync.Status.Conditions, secretsv1alpha1.ConditionReady, metav1.ConditionFalse, reason, msg, sync.Generation)
	if err := c.Status().Update(ctx, sync); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}
