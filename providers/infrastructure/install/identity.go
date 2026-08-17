/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package install

// Runtime identity bootstrap.
//
// MintRuntimeIdentity creates the ServiceAccount + Role + RoleBinding
// the serve subcommand uses, then reads a long-lived bearer from a
// kubernetes.io/service-account-token Secret populated by kcp's token
// controller. The returned RuntimeIdentity carries the SA's namespace + name + token,
// plus the server URL the serve mode connects to (the in-cluster
// kcp front-proxy URL).
//
// The RBAC is intentionally narrow and serves only the long-lived controller
// process:
//
//   - read access to platform Templates + Instances across
//     bound tenant workspaces (via the APIExport virtual workspace)
//   - manage rights on Templates' status (the Template controller
//     patches status on every reconcile)
//   - read-only endpoint discovery plus the exact APIExport-content verbs used
//     by the APIExport-backed Instance controller
//
// Cluster-admin operations (CRD apply, APIExport/APIResourceSchema mutation,
// etc.) stay in the bootstrap/admin credential's scope. Legacy single-binary
// bootstrap runs only when INFRASTRUCTURE_KUBECONFIG is unset and therefore
// uses that admin path; it must never rely on this runtime ServiceAccount.

import (
	"context"
	"fmt"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// RuntimeServiceAccountName is the well-known SA name the runtime
// uses. Hardcoded so init's RoleBinding and serve's kubeconfig
// reference the same identity without configuration.
const RuntimeServiceAccountName = "infrastructure-runtime"

// RuntimeServiceAccountNamespace is the namespace the SA lives in
// inside the provider workspace. Reusing "default" keeps the install
// flow trivial — every kcp workspace ships the default namespace.
const RuntimeServiceAccountNamespace = "default"

// RuntimeRoleName is the (Cluster)Role the SA is bound to. Cluster-
// scoped because the Template controller reads + patches Template
// status across the workspace, not in any single namespace.
const RuntimeRoleName = "infrastructure-runtime"

// RuntimeTokenSecretName is the kubernetes.io/service-account-token Secret
// that holds the runtime SA's long-lived bearer. kcp's token controller
// populates it; the token does not expire (valid until the Secret or SA is
// deleted), so the serve subcommand does not need a rotation loop.
const RuntimeTokenSecretName = "infrastructure-runtime-token"

// RuntimeIdentity is what MintRuntimeIdentity returns to the caller.
// Carries everything WriteKubeconfig needs to assemble a usable
// kubeconfig: server URL, CA data, token, identity name.
type RuntimeIdentity struct {
	// Server is the provider-workspace apiserver URL the kubeconfig targets.
	Server string

	// CAData is the apiserver's CA cert in PEM form, used to verify
	// the connection. Pulled from CAData, or materialized from CAFile, on
	// the admin rest.Config so the generated kubeconfig is self-contained.
	CAData []byte
	// Insecure and ServerName preserve the source rest.Config's supported
	// server-TLS behavior. Client certificate fields are intentionally omitted:
	// the minted ServiceAccount token is the runtime client identity.
	Insecure   bool
	ServerName string

	// Token is the SA's long-lived bearer, read from a
	// kubernetes.io/service-account-token Secret. Non-expiring, so no
	// rotation is required.
	Token string

	// ServiceAccount + Namespace echo back the identity for callers
	// that want to embed them in audit logs or Secret labels.
	ServiceAccount string
	Namespace      string
}

// MintRuntimeIdentity provisions the runtime SA + RBAC and mints a
// bearer for it. Idempotent on SA + role + binding creation.
func MintRuntimeIdentity(ctx context.Context, adminConfig *rest.Config) (*RuntimeIdentity, error) {
	caData, insecure, serverName, err := runtimeTLSForIdentity(adminConfig)
	if err != nil {
		return nil, err
	}
	cs, err := kubernetes.NewForConfig(adminConfig)
	if err != nil {
		return nil, fmt.Errorf("kubernetes client: %w", err)
	}

	sa, err := ensureServiceAccount(ctx, cs)
	if err != nil {
		return nil, fmt.Errorf("ensure SA: %w", err)
	}
	if err := ensureClusterRole(ctx, cs); err != nil {
		return nil, fmt.Errorf("ensure role: %w", err)
	}
	if err := ensureClusterRoleBinding(ctx, cs); err != nil {
		return nil, fmt.Errorf("ensure binding: %w", err)
	}

	// Long-lived (legacy) token: create a kubernetes.io/service-account-token
	// Secret bound to the SA and let kcp's token controller fill in a
	// non-expiring bearer. This replaces the short-lived TokenRequest path so
	// a long-lived serve process does not lose its credentials between retries.
	token, err := ensureLegacySAToken(ctx, cs, sa, RuntimeTokenSecretName)
	if err != nil {
		return nil, fmt.Errorf("ensure runtime token: %w", err)
	}

	return &RuntimeIdentity{
		Server:         adminConfig.Host,
		CAData:         caData,
		Insecure:       insecure,
		ServerName:     serverName,
		Token:          token,
		ServiceAccount: RuntimeServiceAccountName,
		Namespace:      RuntimeServiceAccountNamespace,
	}, nil
}

func runtimeTLSForIdentity(config *rest.Config) ([]byte, bool, string, error) {
	if config == nil {
		return nil, false, "", fmt.Errorf("runtime identity: nil admin config")
	}
	if config.Insecure {
		// CA settings are ignored by an insecure source config. Omitting them from
		// the generated kubeconfig avoids creating an invalid CA+insecure pair.
		return nil, true, config.ServerName, nil
	}
	if len(config.CAData) > 0 {
		return append([]byte(nil), config.CAData...), false, config.ServerName, nil
	}
	if config.CAFile == "" {
		return nil, false, config.ServerName, nil
	}
	caData, err := os.ReadFile(config.CAFile)
	if err != nil {
		return nil, false, "", fmt.Errorf("read admin kubeconfig CA file %q: %w", config.CAFile, err)
	}
	return caData, false, config.ServerName, nil
}

// ensureLegacySAToken creates (idempotently) a kubernetes.io/service-account-token
// Secret bound to saName and waits for kcp's token controller to populate its
// `token` field, then returns that token. Unlike a TokenRequest bearer this
// token does not expire — it stays valid until the Secret or its ServiceAccount
// is deleted — so callers need no rotation loop. Re-invoking reuses the existing
// Secret and returns the same token, keeping the value stable across re-runs of init.
func ensureLegacySAToken(ctx context.Context, cs kubernetes.Interface, sa *corev1.ServiceAccount, secretName string) (string, error) {
	if sa == nil {
		return "", fmt.Errorf("nil ServiceAccount")
	}
	namespace, saName := sa.Namespace, sa.Name
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
			Annotations: map[string]string{
				corev1.ServiceAccountNameKey: saName,
				corev1.ServiceAccountUIDKey:  string(sa.UID),
			},
		},
		Type: corev1.SecretTypeServiceAccountToken,
	}
	if _, err := cs.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return "", fmt.Errorf("creating service-account-token Secret %s/%s: %w", namespace, secretName, err)
	}

	var token string
	if err := wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		got, err := cs.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, fmt.Errorf("get service-account-token Secret: %w", err)
		}
		if got.Type != corev1.SecretTypeServiceAccountToken {
			return false, fmt.Errorf("Secret %s/%s has type %q, want %q", namespace, secretName, got.Type, corev1.SecretTypeServiceAccountToken)
		}
		if got.Annotations[corev1.ServiceAccountNameKey] != saName {
			return false, fmt.Errorf("Secret %s/%s names ServiceAccount %q, want %q", namespace, secretName, got.Annotations[corev1.ServiceAccountNameKey], saName)
		}
		if got.Annotations[corev1.ServiceAccountUIDKey] != string(sa.UID) {
			return false, fmt.Errorf("Secret %s/%s has ServiceAccount UID %q, want %q", namespace, secretName, got.Annotations[corev1.ServiceAccountUIDKey], sa.UID)
		}
		if t := got.Data[corev1.ServiceAccountTokenKey]; len(t) > 0 {
			token = string(t)
			return true, nil
		}
		return false, nil
	}); err != nil {
		return "", fmt.Errorf("waiting for token controller to populate Secret %s/%s: %w", namespace, secretName, err)
	}
	return token, nil
}

func ensureServiceAccount(ctx context.Context, cs kubernetes.Interface) (*corev1.ServiceAccount, error) {
	existing, err := cs.CoreV1().
		ServiceAccounts(RuntimeServiceAccountNamespace).
		Get(ctx, RuntimeServiceAccountName, metav1.GetOptions{})
	if err == nil {
		return existing, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}
	created, err := cs.CoreV1().
		ServiceAccounts(RuntimeServiceAccountNamespace).
		Create(ctx, &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      RuntimeServiceAccountName,
				Namespace: RuntimeServiceAccountNamespace,
			},
		}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return nil, err
	}
	if apierrors.IsAlreadyExists(err) {
		return cs.CoreV1().ServiceAccounts(RuntimeServiceAccountNamespace).Get(ctx, RuntimeServiceAccountName, metav1.GetOptions{})
	}
	return created, nil
}

func ensureClusterRole(ctx context.Context, cs kubernetes.Interface) error {
	want := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: RuntimeRoleName},
		Rules: []rbacv1.PolicyRule{
			// Templates — read, finalizer update, and delete: the
			// controller enforces retirement of removed platform
			// templates by deleting them on sight.
			{
				APIGroups: []string{"infrastructure.faros.sh"},
				Resources: []string{"templates"},
				Verbs:     []string{"get", "list", "watch", "update", "delete"},
			},
			{
				APIGroups: []string{"infrastructure.faros.sh"},
				Resources: []string{"templates/status"},
				Verbs:     []string{"get", "patch", "update"},
			},
			// The stable Instance API is reconciled through the APIExport virtual
			// workspace. Spec/finalizer and status are separate authorization tuples.
			{
				APIGroups: []string{"infrastructure.faros.sh"},
				Resources: []string{"instances"},
				Verbs:     []string{"get", "list", "watch", "patch", "update"},
			},
			{
				APIGroups: []string{"infrastructure.faros.sh"},
				Resources: []string{"instances/status"},
				Verbs:     []string{"get", "patch", "update"},
			},
			// The imported multicluster provider's base cache watches only the named
			// APIExportEndpointSlice to discover virtual-workspace shard URLs.
			{
				APIGroups: []string{"apis.kcp.io"},
				Resources: []string{"apiexportendpointslices"},
				Verbs:     []string{"get", "list", "watch"},
			},
			// apiexports/content is the kcp VW authorizer's gate (see
			// kcp/pkg/virtual/apiexport/authorizer/content.go). Every
			// call against the APIExport's VW URL runs a SAR for
			// {resource: apiexports/content, name: <export-name>}
			// before any per-resource RBAC kicks in. Without this the
			// runtime SA hits 403 on /api and /apis discovery even
			// though it has discovery non-resource URL access.
			{
				APIGroups:     []string{"apis.kcp.io"},
				Resources:     []string{"apiexports/content"},
				ResourceNames: []string{APIExportName},
				// The content authorizer checks the requested resource verb. The
				// controller watches APIBindings/Instances and updates Instance spec,
				// finalizers, and status; it never creates or deletes tenant objects.
				Verbs: []string{"get", "list", "watch", "patch", "update"},
			},
		},
	}
	// API discovery non-resource URLs. Required for the kcp-apiexport
	// provider's client-go to do server-groups + version discovery
	// against the VW URL ("/api", "/apis", etc.) before it can
	// construct any typed informer. Inlined here so the runtime SA's
	// permission boundary stays in one place — alternative would be a
	// second binding to system:discovery.
	want.Rules = append(want.Rules, rbacv1.PolicyRule{
		NonResourceURLs: []string{"/api", "/api/*", "/apis", "/apis/*", "/version", "/openapi", "/openapi/*", "/healthz", "/livez", "/readyz"},
		Verbs:           []string{"get"},
	})

	existing, err := cs.RbacV1().ClusterRoles().Get(ctx, RuntimeRoleName, metav1.GetOptions{})
	if err == nil {
		// Idempotent update — overwrite rules so any change to the
		// embedded definition above takes effect on the next init.
		existing.Rules = want.Rules
		_, err = cs.RbacV1().ClusterRoles().Update(ctx, existing, metav1.UpdateOptions{})
		return err
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	_, err = cs.RbacV1().ClusterRoles().Create(ctx, want, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func ensureClusterRoleBinding(ctx context.Context, cs kubernetes.Interface) error {
	want := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: RuntimeRoleName},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     RuntimeRoleName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      RuntimeServiceAccountName,
				Namespace: RuntimeServiceAccountNamespace,
			},
		},
	}
	existing, err := cs.RbacV1().ClusterRoleBindings().Get(ctx, RuntimeRoleName, metav1.GetOptions{})
	if err == nil {
		existing.RoleRef = want.RoleRef
		existing.Subjects = want.Subjects
		_, err = cs.RbacV1().ClusterRoleBindings().Update(ctx, existing, metav1.UpdateOptions{})
		return err
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	_, err = cs.RbacV1().ClusterRoleBindings().Create(ctx, want, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}
