/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package secretstore reconciles SecretStore CRs: it resolves the referenced
// credential Secret and validates it against the external backend, recording
// the backend version on status. A SecretStore owns no external resource, so
// deletion just drops the finalizer.
package secretstore

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// Reconciler validates SecretStore credentials against the external backend.
type Reconciler struct {
	Manager  mcmanager.Manager
	Backends *backend.Registry
}

// SetupWithManager wires the reconciler into the multicluster manager.
//
// Deliberately no Secret watch: rotation of the store credential is picked up
// by the periodic re-validation (ValidationRefreshAfter), and a just-created
// credential Secret by the CredentialUnavailable requeue — same trade as the
// databricks Connection controller, keeping the informer surface minimal.
func (r *Reconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.Manager = mgr
	return mcbuilder.ControllerManagedBy(mgr).
		Named("secrets-secretstore").
		For(&secretsv1alpha1.SecretStore{}).
		Complete(r)
}

// Reconcile validates one SecretStore.
func (r *Reconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := klog.FromContext(ctx).WithValues("secretstore", req.Name, "cluster", req.ClusterName)

	c, err := shared.ClusterClient(ctx, r.Manager, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	var store secretsv1alpha1.SecretStore
	if err := c.Get(ctx, req.NamespacedName, &store); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Deletion: nothing external to clean up, just release the finalizer.
	if !store.DeletionTimestamp.IsZero() {
		if controllerutil.RemoveFinalizer(&store, secretsv1alpha1.FinalizerSecretStore) {
			if err := c.Update(ctx, &store); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	if controllerutil.AddFinalizer(&store, secretsv1alpha1.FinalizerSecretStore) {
		if err := c.Update(ctx, &store); err != nil {
			return ctrl.Result{}, err
		}
		// Re-reconcile on the fresh object after the finalizer write.
		return ctrl.Result{Requeue: true}, nil
	}

	b, ok := r.Backends.Get(string(store.Spec.Backend))
	if !ok {
		return r.fail(ctx, c, &store, secretsv1alpha1.ReasonBackendNotConfigured, fmt.Sprintf("no backend registered for %q", store.Spec.Backend))
	}

	cred, err := shared.ResolveCredential(ctx, c, &store)
	if err != nil {
		// A just-created Secret may not be claim-visible on this VW yet;
		// requeue so the store recovers once it lands.
		return r.failAfter(ctx, c, &store, secretsv1alpha1.ReasonCredentialUnavailable, err.Error())
	}

	info, err := b.Validate(ctx, &store, cred)
	if err != nil {
		return r.fail(ctx, c, &store, backend.ClassifyError(err), err.Error())
	}

	store.Status.ObservedGeneration = store.Generation
	store.Status.BackendVersion = info.Version
	shared.SetCondition(&store.Status.Conditions, secretsv1alpha1.ConditionValidated, metav1.ConditionTrue, secretsv1alpha1.ReasonReady, "credential authenticated against "+string(store.Spec.Backend), store.Generation)
	shared.SetCondition(&store.Status.Conditions, secretsv1alpha1.ConditionReady, metav1.ConditionTrue, secretsv1alpha1.ReasonReady, "", store.Generation)
	if err := c.Status().Update(ctx, &store); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("SecretStore validated", "backend", store.Spec.Backend, "version", info.Version)
	// Periodic re-validation notices rotated/revoked credentials without a
	// Secret watch.
	return ctrl.Result{RequeueAfter: shared.ValidationRefreshAfter}, nil
}

// fail records a not-ready status and swallows the error (the bad state is on
// the CR), re-polling on the validation cadence so transient backend outages
// self-heal.
func (r *Reconciler) fail(ctx context.Context, c client.Client, store *secretsv1alpha1.SecretStore, reason, msg string) (ctrl.Result, error) {
	return r.failWithDelay(ctx, c, store, reason, msg, shared.ValidationRefreshAfter)
}

// failAfter is fail with the short dependency-retry delay, for causes expected
// to resolve momentarily (a credential Secret still propagating).
func (r *Reconciler) failAfter(ctx context.Context, c client.Client, store *secretsv1alpha1.SecretStore, reason, msg string) (ctrl.Result, error) {
	return r.failWithDelay(ctx, c, store, reason, msg, shared.DependencyRetryAfter)
}

func (r *Reconciler) failWithDelay(ctx context.Context, c client.Client, store *secretsv1alpha1.SecretStore, reason, msg string, after time.Duration) (ctrl.Result, error) {
	store.Status.ObservedGeneration = store.Generation
	store.Status.BackendVersion = ""
	shared.SetCondition(&store.Status.Conditions, secretsv1alpha1.ConditionValidated, metav1.ConditionFalse, reason, msg, store.Generation)
	shared.SetCondition(&store.Status.Conditions, secretsv1alpha1.ConditionReady, metav1.ConditionFalse, reason, msg, store.Generation)
	if err := c.Status().Update(ctx, store); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: after}, nil
}
