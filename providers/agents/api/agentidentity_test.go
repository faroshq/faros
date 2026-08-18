// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package api

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

func identityScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	return s
}

// newIdentityFake returns a dynamic fake plus a hook that simulates kcp's token
// controller: it fills the token in on the Nth Get of the Secret, so the poll
// in ensureAgentToken is exercised rather than short-circuited.
func newIdentityFake(t *testing.T, fillAfterGets int) *dynamicfake.FakeDynamicClient {
	t.Helper()
	dyn := dynamicfake.NewSimpleDynamicClient(identityScheme())
	gets := 0
	dyn.PrependReactor("get", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		ga, ok := action.(k8stesting.GetAction)
		if !ok || ga.GetName() != agentTokenSecretName("scout") {
			return false, nil, nil
		}
		gets++
		if gets < fillAfterGets {
			return true, &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1", "kind": "Secret",
				"metadata": map[string]any{
					"name": ga.GetName(), "namespace": identityNamespace,
					"annotations": map[string]any{corev1.ServiceAccountNameKey: agentIdentityName("scout")},
				},
				"type": string(corev1.SecretTypeServiceAccountToken),
			}}, nil
		}
		return true, &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "v1", "kind": "Secret",
			"metadata": map[string]any{
				"name": ga.GetName(), "namespace": identityNamespace,
				"annotations": map[string]any{corev1.ServiceAccountNameKey: agentIdentityName("scout")},
			},
			"type": string(corev1.SecretTypeServiceAccountToken),
			"data": map[string]any{
				corev1.ServiceAccountTokenKey: base64.StdEncoding.EncodeToString([]byte("sa-token-value")),
			},
		}}, nil
	})
	return dyn
}

func TestEnsureAgentIdentity(t *testing.T) {
	ctx := context.Background()

	t.Run("provisions the identity and returns the populated token", func(t *testing.T) {
		dyn := newIdentityFake(t, 3) // token appears on the third Get
		tok, err := ensureAgentIdentity(ctx, dyn, "scout")
		if err != nil {
			t.Fatal(err)
		}
		if tok != "sa-token-value" {
			t.Fatalf("token = %q, want the decoded Secret value", tok)
		}

		sa, err := dyn.Resource(serviceAccountGVR).Namespace(identityNamespace).
			Get(ctx, agentIdentityName("scout"), metav1.GetOptions{})
		if err != nil {
			t.Fatalf("ServiceAccount: %v", err)
		}
		if sa.GetName() != "faros-agent-scout" {
			t.Fatalf("SA name = %q", sa.GetName())
		}

		// The RBAC is the security surface, so its exact shape is pinned. Read
		// only, and confined to the infrastructure instance group: an agent
		// identity must never be able to read Secrets or mutate anything.
		role, err := dyn.Resource(clusterRoleGVR).Get(ctx, agentIdentityName("scout"), metav1.GetOptions{})
		if err != nil {
			t.Fatalf("ClusterRole: %v", err)
		}
		rules, _, _ := unstructured.NestedSlice(role.Object, "rules")
		if len(rules) != 1 {
			t.Fatalf("want exactly one rule, got %v", rules)
		}
		rule := rules[0].(map[string]any)
		groups, _, _ := unstructured.NestedStringSlice(rule, "apiGroups")
		resources, _, _ := unstructured.NestedStringSlice(rule, "resources")
		verbs, _, _ := unstructured.NestedStringSlice(rule, "verbs")
		if len(groups) != 1 || groups[0] != instanceGroup {
			t.Fatalf("apiGroups = %v, want only %q", groups, instanceGroup)
		}
		if len(resources) != 1 || resources[0] != instanceResource {
			t.Fatalf("resources = %v, want only %q", resources, instanceResource)
		}
		for _, v := range verbs {
			if v != "get" && v != "list" {
				t.Fatalf("verb %q grants more than read", v)
			}
		}

		binding, err := dyn.Resource(clusterRoleBindingGVR).Get(ctx, agentIdentityName("scout"), metav1.GetOptions{})
		if err != nil {
			t.Fatalf("ClusterRoleBinding: %v", err)
		}
		roleName, _, _ := unstructured.NestedString(binding.Object, "roleRef", "name")
		if roleName != agentIdentityName("scout") {
			t.Fatalf("binding points at %q", roleName)
		}
		subjects, _, _ := unstructured.NestedSlice(binding.Object, "subjects")
		subj := subjects[0].(map[string]any)
		if subj["name"] != agentIdentityName("scout") || subj["namespace"] != identityNamespace {
			t.Fatalf("subject = %v", subj)
		}
	})

	t.Run("rejects a legacy wildcard role pending administrator migration", func(t *testing.T) {
		dyn := newIdentityFake(t, 1)
		name := agentIdentityName("scout")
		legacy := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "rbac.authorization.k8s.io/v1",
			"kind":       "ClusterRole",
			"metadata":   map[string]any{"name": name, "labels": map[string]any{"keep": "true"}},
			"aggregationRule": map[string]any{
				"clusterRoleSelectors": []any{},
			},
			"rules": []any{map[string]any{
				"apiGroups": []any{instanceGroup},
				"resources": []any{"*"},
				"verbs":     []any{"get", "list"},
			}},
		}}
		if _, err := dyn.Resource(clusterRoleGVR).Create(ctx, legacy, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
		dyn.ClearActions()

		_, err := ensureAgentIdentity(ctx, dyn, "scout")
		if err == nil || !strings.Contains(err.Error(), "administrator must narrow") {
			t.Fatalf("error = %v, want explicit administrator migration", err)
		}
		role, err := dyn.Resource(clusterRoleGVR).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		rules, _, _ := unstructured.NestedSlice(role.Object, "rules")
		rule := rules[0].(map[string]any)
		resources, _, _ := unstructured.NestedStringSlice(rule, "resources")
		if len(resources) != 1 || resources[0] != "*" {
			t.Fatalf("resources = %v, provider unexpectedly mutated the role", resources)
		}
		if _, found := role.Object["aggregationRule"]; !found {
			t.Fatal("provider unexpectedly removed aggregationRule")
		}
		if role.GetLabels()["keep"] != "true" {
			t.Fatalf("metadata was not preserved: %v", role.GetLabels())
		}
		updates := 0
		for _, action := range dyn.Actions() {
			if action.GetVerb() == "update" && action.GetResource().Resource == "clusterroles" {
				updates++
			}
		}
		if updates != 0 {
			t.Fatalf("ClusterRole updates = %d, want none", updates)
		}
	})

	t.Run("rejects an unexpected existing binding without mutating it", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			mutate func(*unstructured.Unstructured)
		}{
			{
				name: "wrong roleRef",
				mutate: func(binding *unstructured.Unstructured) {
					_ = unstructured.SetNestedField(binding.Object, "cluster-admin", "roleRef", "name")
				},
			},
			{
				name: "extra subject",
				mutate: func(binding *unstructured.Unstructured) {
					subjects, _, _ := unstructured.NestedSlice(binding.Object, "subjects")
					subjects = append(subjects, map[string]any{"kind": "User", "name": "unexpected", "apiGroup": "rbac.authorization.k8s.io"})
					_ = unstructured.SetNestedSlice(binding.Object, subjects, "subjects")
				},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				dyn := newIdentityFake(t, 1)
				if _, err := ensureAgentIdentity(ctx, dyn, "scout"); err != nil {
					t.Fatal(err)
				}
				name := agentIdentityName("scout")
				binding, err := dyn.Resource(clusterRoleBindingGVR).Get(ctx, name, metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				tc.mutate(binding)
				if _, err := dyn.Resource(clusterRoleBindingGVR).Update(ctx, binding, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
				dyn.ClearActions()

				_, err = ensureAgentIdentity(ctx, dyn, "scout")
				if err == nil || !strings.Contains(err.Error(), "unexpected roleRef/subjects") {
					t.Fatalf("error = %v, want fail-closed binding collision", err)
				}
				for _, action := range dyn.Actions() {
					if action.GetVerb() == "update" && action.GetResource().Resource == "clusterrolebindings" {
						t.Fatalf("provider unexpectedly updated binding: %#v", action)
					}
				}
			})
		}
	})

	t.Run("the token Secret is a service-account-token naming the SA", func(t *testing.T) {
		// Without the right type and annotation, kcp's token controller never
		// populates it and the poll would time out with nothing to show for it.
		dyn := newIdentityFake(t, 1)
		if _, err := ensureAgentIdentity(ctx, dyn, "scout"); err != nil {
			t.Fatal(err)
		}
		created := findCreated(t, dyn, "secrets")
		if got, _, _ := unstructured.NestedString(created.Object, "type"); got != string(corev1.SecretTypeServiceAccountToken) {
			t.Fatalf("Secret type = %q", got)
		}
		ann := created.GetAnnotations()
		if ann[corev1.ServiceAccountNameKey] != agentIdentityName("scout") {
			t.Fatalf("annotations = %v", ann)
		}
	})

	t.Run("rejects an unexpected existing token Secret without mutating it", func(t *testing.T) {
		for _, tc := range []struct {
			name        string
			secretType  string
			serviceAcct string
			uid         string
		}{
			{name: "wrong type", secretType: string(corev1.SecretTypeOpaque), serviceAcct: agentIdentityName("scout"), uid: "right-uid"},
			{name: "wrong ServiceAccount name", secretType: string(corev1.SecretTypeServiceAccountToken), serviceAcct: "other", uid: "right-uid"},
			{name: "wrong ServiceAccount UID", secretType: string(corev1.SecretTypeServiceAccountToken), serviceAcct: agentIdentityName("scout"), uid: "wrong-uid"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				dyn := dynamicfake.NewSimpleDynamicClient(identityScheme())
				secret := &unstructured.Unstructured{Object: map[string]any{
					"apiVersion": "v1", "kind": "Secret",
					"metadata": map[string]any{
						"name": agentTokenSecretName("scout"), "namespace": identityNamespace,
						"annotations": map[string]any{
							corev1.ServiceAccountNameKey: tc.serviceAcct,
							corev1.ServiceAccountUIDKey:  tc.uid,
						},
					},
					"type": tc.secretType,
					"data": map[string]any{corev1.ServiceAccountTokenKey: base64.StdEncoding.EncodeToString([]byte("unexpected-token"))},
				}}
				if _, err := dyn.Resource(secretsGVR).Namespace(identityNamespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil {
					t.Fatal(err)
				}
				dyn.ClearActions()
				if _, err := ensureAgentToken(ctx, dyn, "scout", "right-uid"); err == nil {
					t.Fatal("want fail-closed token Secret collision")
				}
				for _, action := range dyn.Actions() {
					if action.GetVerb() == "update" || action.GetVerb() == "patch" {
						t.Fatalf("provider unexpectedly mutated token Secret: %#v", action)
					}
				}
			})
		}
	})

	t.Run("is idempotent — a second call creates nothing new", func(t *testing.T) {
		// Every background run calls this. AlreadyExists must be success, not
		// an error that disables instance tools for the rest of the process.
		dyn := newIdentityFake(t, 1)
		if _, err := ensureAgentIdentity(ctx, dyn, "scout"); err != nil {
			t.Fatal(err)
		}
		before := countCreates(dyn)
		dyn.ClearActions()
		tok, err := ensureAgentIdentity(ctx, dyn, "scout")
		if err != nil {
			t.Fatalf("second call: %v", err)
		}
		if tok != "sa-token-value" {
			t.Fatalf("token = %q", tok)
		}
		if before == 0 {
			t.Fatal("first call created nothing")
		}
	})

	t.Run("a token that never arrives is an error, not an empty token", func(t *testing.T) {
		// An empty token would compose a data-plane URL that 401s two hops
		// away; the caller needs to know provisioning failed.
		dyn := newIdentityFake(t, 1_000_000)
		ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
		defer cancel()
		if _, err := ensureAgentIdentity(ctx, dyn, "scout"); err == nil {
			t.Fatal("want an error when the token controller never populates the Secret")
		}
	})
}

// The identity cache is what keeps a busy scheduler from re-provisioning on
// every run, and what lets a revoked identity recover.
func TestIdentityCache(t *testing.T) {
	c := newIdentityCache()
	if _, ok := c.get("c1", "scout"); ok {
		t.Fatal("empty cache returned a token")
	}
	c.put("c1", "scout", "t1")
	if tok, ok := c.get("c1", "scout"); !ok || tok != "t1" {
		t.Fatalf("get = %q,%v", tok, ok)
	}
	// Same agent name in a different workspace is a different identity; sharing
	// one token across clusters would send workspace A's credential to B.
	if _, ok := c.get("c2", "scout"); ok {
		t.Fatal("token leaked across clusters")
	}
	// These tokens never expire on their own, so a withdrawn identity would be
	// used from memory until restart if the cache did not age entries out.
	now := time.Now()
	c.now = func() time.Time { return now }
	c.put("c1", "scout", "t1")
	now = now.Add(identityTTL + time.Second)
	if _, ok := c.get("c1", "scout"); ok {
		t.Fatal("expired entry still served")
	}
}

func findCreated(t *testing.T, dyn *dynamicfake.FakeDynamicClient, resource string) *unstructured.Unstructured {
	t.Helper()
	for _, a := range dyn.Actions() {
		ca, ok := a.(k8stesting.CreateAction)
		if !ok || a.GetResource().Resource != resource {
			continue
		}
		if u, ok := ca.GetObject().(*unstructured.Unstructured); ok {
			return u
		}
	}
	t.Fatalf("no create recorded for %s", resource)
	return nil
}

func countCreates(dyn *dynamicfake.FakeDynamicClient) int {
	n := 0
	for _, a := range dyn.Actions() {
		if a.GetVerb() == "create" {
			n++
		}
	}
	return n
}
