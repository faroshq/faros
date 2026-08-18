// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package api

// Per-agent identity for background runs.
//
// An interactive run acts as the human driving it: their bearer token reaches
// the platform data plane, which authorizes by re-reading the target instance
// as them. A scheduled, heartbeat, wakeup or inbound-channel run has no human,
// so it had no token and instance-backed tools (self-hosted search, a browser
// instance) simply refused to run.
//
// This gives each agent a ServiceAccount of its own in the tenant workspace,
// with a long-lived token, and background runs call the data plane as that
// identity. The shape mirrors the hub's per-MCPServer identity
// (pkg/hub/controllers/mcpserver, ensureMCPIdentity) — the same pattern kcp
// already supports. Long-lived because kcp has no TokenRequest API: its token
// controller populates a Secret, which is what "legacy" means here.
//
// SCOPE, STATED PLAINLY: the agent's ServiceAccount can read every
// infrastructure instance in its workspace, not only the ones its Connections
// name. That is a deliberate choice for a stable Role that does not churn as
// connections change. The consequence is that this token, if read out of the
// workspace, reaches any instance there — including a browser instance holding
// live logins the agent was never wired to. It is a standing credential: it
// does not expire, and revoking it means deleting the ServiceAccount.

import (
	"context"
	"fmt"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
)

var (
	serviceAccountGVR     = schema.GroupVersionResource{Version: "v1", Resource: "serviceaccounts"}
	clusterRoleGVR        = schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}
	clusterRoleBindingGVR = schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"}
	secretsGVR            = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}
)

// identityNamespace is where the agent ServiceAccount and its token Secret
// live — the same namespace the provider already keeps credential Secrets in,
// so the permission claim covers both without widening anything.
const identityNamespace = "default"

const (
	// instanceGroup and instanceResource identify the infrastructure provider's
	// permanent, flattened Instance API. Limiting both halves of the RBAC tuple
	// is important: a group wildcard would also expose Templates and future APIs.
	instanceGroup    = "infrastructure.faros.sh"
	instanceResource = "instances"
)

// agentIdentityName is the ServiceAccount / ClusterRole / binding name for one
// agent. Prefixed so it is obviously platform-managed in a workspace a human
// also uses.
func agentIdentityName(agent string) string { return "faros-agent-" + agent }

// agentTokenSecretName is the Secret kcp's token controller populates.
func agentTokenSecretName(agent string) string { return "faros-agent-" + agent + "-token" }

// identityCache memoises minted tokens per (cluster, agent). Provisioning is
// idempotent but involves several API calls and a poll, and the background
// scheduler may start many runs a minute — re-minting each time would be a
// needless round-trip storm against the tenant workspace.
//
// Entries expire. These tokens do not — revocation means deleting the
// ServiceAccount — so without a TTL a revoked identity would keep being used
// from memory until the provider restarted. The TTL bounds how long a run can
// act on an identity the tenant has already withdrawn.
type identityCache struct {
	mu     sync.Mutex
	tokens map[string]cachedIdentity
	ttl    time.Duration
	now    func() time.Time // overridable in tests
}

type cachedIdentity struct {
	token   string
	expires time.Time
}

// identityTTL is a compromise: long enough that a per-minute scheduler is not
// re-provisioning constantly, short enough that a withdrawn identity stops
// working within the hour.
const identityTTL = 30 * time.Minute

func newIdentityCache() *identityCache {
	return &identityCache{tokens: map[string]cachedIdentity{}, ttl: identityTTL, now: time.Now}
}

func (c *identityCache) get(cluster, agent string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.tokens[cluster+"/"+agent]
	if !ok || c.now().After(e.expires) {
		return "", false
	}
	return e.token, true
}

func (c *identityCache) put(cluster, agent, token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tokens[cluster+"/"+agent] = cachedIdentity{token: token, expires: c.now().Add(c.ttl)}
}

// ensureAgentIdentity provisions (idempotently) the agent's ServiceAccount, its
// read-instances ClusterRole and binding, and its token Secret, then waits for
// kcp's token controller to fill the token in. Every object is created-if-absent
// and the managed ClusterRole must match the exact least-privilege rule. Legacy
// wildcard roles fail closed and require an administrator-owned migration; the
// provider intentionally has no authority to update arbitrary ClusterRoles.
func ensureAgentIdentity(ctx context.Context, dyn dynamic.Interface, agent string) (string, error) {
	name := agentIdentityName(agent)

	sa := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ServiceAccount",
		"metadata":   map[string]any{"name": name, "namespace": identityNamespace},
	}}
	if err := createIfAbsent(ctx, dyn.Resource(serviceAccountGVR).Namespace(identityNamespace), sa); err != nil {
		return "", fmt.Errorf("agent ServiceAccount: %w", err)
	}
	createdSA, err := dyn.Resource(serviceAccountGVR).Namespace(identityNamespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("read agent ServiceAccount: %w", err)
	}

	// Read-only, and only over the permanent Instance resource. Instances are
	// cluster-scoped, so this has to be a ClusterRole.
	role := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "rbac.authorization.k8s.io/v1",
		"kind":       "ClusterRole",
		"metadata":   map[string]any{"name": name},
		"rules": []any{map[string]any{
			"apiGroups": []any{instanceGroup},
			"resources": []any{instanceResource},
			"verbs":     []any{"get", "list"},
		}},
	}}
	if err := ensureAgentRole(ctx, dyn.Resource(clusterRoleGVR), role); err != nil {
		return "", fmt.Errorf("agent ClusterRole: %w", err)
	}

	binding := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "rbac.authorization.k8s.io/v1",
		"kind":       "ClusterRoleBinding",
		"metadata":   map[string]any{"name": name},
		"roleRef": map[string]any{
			"apiGroup": "rbac.authorization.k8s.io",
			"kind":     "ClusterRole",
			"name":     name,
		},
		"subjects": []any{map[string]any{
			"kind":      "ServiceAccount",
			"name":      name,
			"namespace": identityNamespace,
		}},
	}}
	if err := ensureAgentRoleBinding(ctx, dyn.Resource(clusterRoleBindingGVR), binding); err != nil {
		return "", fmt.Errorf("agent ClusterRoleBinding: %w", err)
	}

	return ensureAgentToken(ctx, dyn, agent, createdSA.GetUID())
}

// ensureAgentRoleBinding creates the managed binding or verifies that an
// existing same-named object binds only the managed ServiceAccount to the
// managed least-privilege role. As with the role, collisions fail closed and
// are never updated with the provider's create-only authority.
func ensureAgentRoleBinding(ctx context.Context, bindings dynamic.ResourceInterface, desired *unstructured.Unstructured) error {
	got, err := bindings.Get(ctx, desired.GetName(), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, createErr := bindings.Create(ctx, desired, metav1.CreateOptions{}); createErr == nil {
			return nil
		} else if !apierrors.IsAlreadyExists(createErr) {
			return createErr
		}
		got, err = bindings.Get(ctx, desired.GetName(), metav1.GetOptions{})
	}
	if err != nil {
		return err
	}

	desiredRoleRef, _, err := unstructured.NestedMap(desired.Object, "roleRef")
	if err != nil {
		return err
	}
	gotRoleRef, _, err := unstructured.NestedMap(got.Object, "roleRef")
	if err != nil {
		return err
	}
	desiredSubjects, _, err := unstructured.NestedSlice(desired.Object, "subjects")
	if err != nil {
		return err
	}
	gotSubjects, _, err := unstructured.NestedSlice(got.Object, "subjects")
	if err != nil {
		return err
	}
	if equality.Semantic.DeepEqual(gotRoleRef, desiredRoleRef) && equality.Semantic.DeepEqual(gotSubjects, desiredSubjects) {
		return nil
	}
	return fmt.Errorf("managed binding %q has legacy or unexpected roleRef/subjects; an administrator must bind only ServiceAccount %q/%q to ClusterRole %q", desired.GetName(), identityNamespace, desired.GetName(), desired.GetName())
}

// ensureAgentRole creates the managed role or verifies an existing role's exact
// policy. It never updates RBAC: widening the provider's create-only authority
// would let it mutate arbitrary tenant ClusterRoles. An administrator must
// narrow roles left by releases that used the infrastructure-group wildcard.
func ensureAgentRole(ctx context.Context, roles dynamic.ResourceInterface, desired *unstructured.Unstructured) error {
	got, err := roles.Get(ctx, desired.GetName(), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, createErr := roles.Create(ctx, desired, metav1.CreateOptions{}); createErr == nil {
			return nil
		} else if !apierrors.IsAlreadyExists(createErr) {
			return createErr
		}
		got, err = roles.Get(ctx, desired.GetName(), metav1.GetOptions{})
	}
	if err != nil {
		return err
	}

	desiredRules, _, err := unstructured.NestedSlice(desired.Object, "rules")
	if err != nil {
		return err
	}
	gotRules, _, err := unstructured.NestedSlice(got.Object, "rules")
	if err != nil {
		return err
	}
	_, hasAggregationRule := got.Object["aggregationRule"]
	if equality.Semantic.DeepEqual(gotRules, desiredRules) && !hasAggregationRule {
		return nil
	}
	return fmt.Errorf("managed role %q has legacy or unexpected policy; an administrator must narrow it to read-only resource %q in API group %q", desired.GetName(), instanceResource, instanceGroup)
}

// ensureAgentToken creates the legacy service-account-token Secret and waits
// for the token controller to populate it. Mirrors pkg/hub/providers
// ensureLegacySAToken; kcp has no TokenRequest subresource, so this is how a
// usable token is obtained.
func ensureAgentToken(ctx context.Context, dyn dynamic.Interface, agent string, serviceAccountUID types.UID) (string, error) {
	secretName := agentTokenSecretName(agent)
	secrets := dyn.Resource(secretsGVR).Namespace(identityNamespace)
	annotations := map[string]any{corev1.ServiceAccountNameKey: agentIdentityName(agent)}
	if serviceAccountUID != "" {
		annotations[corev1.ServiceAccountUIDKey] = string(serviceAccountUID)
	}

	sec := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":        secretName,
			"namespace":   identityNamespace,
			"annotations": annotations,
		},
		"type": string(corev1.SecretTypeServiceAccountToken),
	}}
	if err := createIfAbsent(ctx, secrets, sec); err != nil {
		return "", fmt.Errorf("agent token Secret: %w", err)
	}

	var token string
	err := wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		got, err := secrets.Get(ctx, secretName, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		if err := validateAgentTokenSecret(got, agent, serviceAccountUID); err != nil {
			return false, err
		}
		// Secret data is base64 in unstructured form; NestedString gives the
		// encoded value, so decode through the typed object instead.
		typed, err := fromU[corev1.Secret](got)
		if err != nil {
			return false, nil
		}
		if t := typed.Data[corev1.ServiceAccountTokenKey]; len(t) > 0 {
			token = string(t)
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return "", fmt.Errorf("waiting for kcp's token controller to populate Secret %s/%s: %w", identityNamespace, secretName, err)
	}
	return token, nil
}

func validateAgentTokenSecret(secret *unstructured.Unstructured, agent string, serviceAccountUID types.UID) error {
	wantName := agentTokenSecretName(agent)
	if secret.GetName() != wantName || secret.GetNamespace() != identityNamespace {
		return fmt.Errorf("managed token Secret identity is %s/%s, want %s/%s", secret.GetNamespace(), secret.GetName(), identityNamespace, wantName)
	}
	typeName, found, err := unstructured.NestedString(secret.Object, "type")
	if err != nil || !found || typeName != string(corev1.SecretTypeServiceAccountToken) {
		return fmt.Errorf("managed token Secret %s/%s has unexpected type %q", identityNamespace, wantName, typeName)
	}
	annotations := secret.GetAnnotations()
	wantSA := agentIdentityName(agent)
	if annotations[corev1.ServiceAccountNameKey] != wantSA {
		return fmt.Errorf("managed token Secret %s/%s names ServiceAccount %q, want %q", identityNamespace, wantName, annotations[corev1.ServiceAccountNameKey], wantSA)
	}
	if serviceAccountUID != "" && annotations[corev1.ServiceAccountUIDKey] != string(serviceAccountUID) {
		return fmt.Errorf("managed token Secret %s/%s names ServiceAccount UID %q, want %q", identityNamespace, wantName, annotations[corev1.ServiceAccountUIDKey], serviceAccountUID)
	}
	return nil
}

// createIfAbsent creates an object, treating AlreadyExists as success. The
// ClusterRole policy is verified separately so legacy wildcard grants fail
// closed without widening the provider's create-only RBAC authority.
func createIfAbsent(ctx context.Context, ri dynamic.ResourceInterface, obj *unstructured.Unstructured) error {
	_, err := ri.Create(ctx, obj, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}
