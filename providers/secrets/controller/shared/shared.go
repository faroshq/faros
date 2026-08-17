/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package shared holds helpers common to the secrets provider's reconcilers:
// resolving the per-tenant client from the multicluster manager, condition
// bookkeeping, and credential resolution from a SecretStore's secretRef.
package shared

import (
	"context"
	"fmt"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	secretsv1alpha1 "github.com/faroshq/provider-secrets/apis/v1alpha1"
	"github.com/faroshq/provider-secrets/backend"
)

const (
	// DefaultTokenKey is the Secret data key holding the store credential
	// when the SecretStore's secretRef.Key is empty.
	DefaultTokenKey = "token"

	// DependencyRetryAfter is the requeue delay while a dependency (the
	// credential Secret, the SecretStore) is expected to appear shortly.
	DependencyRetryAfter = 15 * time.Second

	// ValidationRefreshAfter re-validates a healthy SecretStore periodically
	// so credential rotation/revocation is noticed without a Secret watch
	// (mirrors the databricks Connection controller's discipline).
	ValidationRefreshAfter = 5 * time.Minute
)

// DefaultCredentialsNamespace is the namespace a SecretStore's secretRef
// resolves to when its Namespace field is empty. Overridable so an admin can
// push credential Secrets into a namespace tenants cannot write to.
func DefaultCredentialsNamespace() string {
	if v := os.Getenv("FAROS_TENANT_CREDENTIALS_NAMESPACE"); v != "" {
		return v
	}
	return "default"
}

// ClusterClient resolves the controller-runtime client scoped to the tenant
// workspace named by clusterName (the kcp logical cluster the CR lives in).
func ClusterClient(ctx context.Context, mgr mcmanager.Manager, clusterName multicluster.ClusterName) (client.Client, error) {
	cl, err := mgr.GetCluster(ctx, clusterName)
	if err != nil {
		return nil, fmt.Errorf("getting cluster %s: %w", clusterName, err)
	}
	return cl.GetClient(), nil
}

// SetCondition upserts a condition keyed by type. It delegates to apimachinery's
// meta.SetStatusCondition, which manages LastTransitionTime (set to now when the
// status changes, preserved otherwise) — a required field the API server does
// NOT default, so it must be stamped client-side.
func SetCondition(conds *[]metav1.Condition, condType string, status metav1.ConditionStatus, reason, msg string, observedGen int64) {
	apimeta.SetStatusCondition(conds, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: observedGen,
	})
}

// ResolveStore fetches the (cluster-scoped) SecretStore named ref in the same
// workspace. Returns a not-found-friendly error the caller can requeue on.
func ResolveStore(ctx context.Context, c client.Client, ref string) (*secretsv1alpha1.SecretStore, error) {
	var store secretsv1alpha1.SecretStore
	if err := c.Get(ctx, types.NamespacedName{Name: ref}, &store); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("secretstore %q not found", ref)
		}
		return nil, fmt.Errorf("get secretstore %q: %w", ref, err)
	}
	return &store, nil
}

// ResolveCredential reads the SecretStore's referenced Secret via the typed
// tenant-scoped client and returns the backend credential. The secrets read is
// authorized by the provider's APIExport secrets permission claim.
func ResolveCredential(ctx context.Context, c client.Client, store *secretsv1alpha1.SecretStore) (backend.Credential, error) {
	ns := store.Spec.SecretRef.Namespace
	if ns == "" {
		ns = DefaultCredentialsNamespace()
	}
	key := store.Spec.SecretRef.Key
	if key == "" {
		key = DefaultTokenKey
	}
	var secret corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: store.Spec.SecretRef.Name}, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return backend.Credential{}, fmt.Errorf("credential secret %s/%s not found", ns, store.Spec.SecretRef.Name)
		}
		if apierrors.IsForbidden(err) {
			return backend.Credential{}, fmt.Errorf("credential secret %s/%s is not readable; check the provider APIBinding secrets claim", ns, store.Spec.SecretRef.Name)
		}
		return backend.Credential{}, fmt.Errorf("get credential secret %s/%s: %w", ns, store.Spec.SecretRef.Name, err)
	}
	tok, ok := secret.Data[key]
	if !ok || len(tok) == 0 {
		return backend.Credential{}, fmt.Errorf("credential secret %s/%s has no non-empty key %q", ns, store.Spec.SecretRef.Name, key)
	}
	return backend.Credential{Token: string(tok)}, nil
}
