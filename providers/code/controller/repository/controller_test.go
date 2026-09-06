/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package repository

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	codev1alpha1 "github.com/faroshq/provider-code/apis/v1alpha1"
	"github.com/faroshq/provider-code/backend"
	backendstub "github.com/faroshq/provider-code/backend/stub"
	codescheme "github.com/faroshq/provider-code/scheme"
)

type testCluster struct {
	cluster.Cluster
	client client.Client
}

func (c *testCluster) GetClient() client.Client { return c.client }

type testManager struct {
	mcmanager.Manager
	cl cluster.Cluster
}

func (m *testManager) GetCluster(context.Context, multicluster.ClusterName) (cluster.Cluster, error) {
	return m.cl, nil
}

type recordingBackend struct {
	*backendstub.Backend
	ensured  []*codev1alpha1.Repository
	deleted  []*codev1alpha1.Repository
	ensureID string
}

func (b *recordingBackend) EnsureRepository(_ context.Context, _ *codev1alpha1.Connection, _ backend.Credential, repo *codev1alpha1.Repository) (backend.RepositoryResult, error) {
	b.ensured = append(b.ensured, repo.DeepCopy())
	if b.ensureID == "" {
		b.ensureID = "repo-id"
	}
	return backend.RepositoryResult{
		RepoID:   b.ensureID,
		HTMLURL:  "https://github.example/acme/widgets",
		CloneURL: "https://github.example/acme/widgets.git",
		SSHURL:   "git@github.example:acme/widgets.git",
	}, nil
}

func TestEnsureRejectsExternalRepositoryIdentityReplacement(t *testing.T) {
	r, c, b := newRepositoryTestReconciler(t, repositoryTestObjects()...)
	if _, err := reconcileRepository(t, r); err != nil {
		t.Fatalf("add finalizer reconcile: %v", err)
	}
	if _, err := reconcileRepository(t, r); err != nil {
		t.Fatalf("first ensure reconcile: %v", err)
	}
	b.ensureID = "replacement-id"
	if _, err := reconcileRepository(t, r); err == nil {
		t.Fatal("external identity replacement unexpectedly reached Ready")
	}
	var repo codev1alpha1.Repository
	if err := c.Get(context.Background(), client.ObjectKey{Name: "widgets"}, &repo); err != nil {
		t.Fatalf("get repository: %v", err)
	}
	if repo.Status.RepoID != "repo-id" {
		t.Fatalf("RepoID changed after replacement: %q", repo.Status.RepoID)
	}
	if cond := readyCondition(repo.Status.Conditions); cond == nil || cond.Reason != codev1alpha1.ReasonIdentityConflict {
		t.Fatalf("replacement condition = %+v", cond)
	}
}

func (b *recordingBackend) DeleteRepository(_ context.Context, _ *codev1alpha1.Connection, _ backend.Credential, repo *codev1alpha1.Repository) error {
	b.deleted = append(b.deleted, repo.DeepCopy())
	return nil
}

func newRepositoryTestReconciler(t *testing.T, objects ...client.Object) (*Reconciler, client.Client, *recordingBackend) {
	t.Helper()
	c := fake.NewClientBuilder().
		WithScheme(codescheme.NewScheme()).
		WithStatusSubresource(&codev1alpha1.Repository{}).
		WithObjects(objects...).
		Build()
	mgr := &testManager{cl: &testCluster{client: c}}
	reg := backend.NewRegistry()
	b := &recordingBackend{Backend: backendstub.New()}
	if err := reg.Register(b); err != nil {
		t.Fatalf("register backend: %v", err)
	}
	return &Reconciler{Manager: mgr, Backends: reg}, c, b
}

func repositoryTestObjects() []client.Object {
	return []client.Object{
		&codev1alpha1.Connection{
			ObjectMeta: metav1.ObjectMeta{Name: "conn"},
			Spec: codev1alpha1.ConnectionSpec{
				Provider: codev1alpha1.ProviderGitHub,
				Type:     codev1alpha1.CredentialTypePAT,
				Owner:    "acme",
				SecretRef: codev1alpha1.LocalSecretReference{
					Name:      "git-token",
					Namespace: "default",
					Key:       "token",
				},
			},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "git-token", Namespace: "default"},
			Data:       map[string][]byte{"token": []byte("token")},
		},
		&codev1alpha1.Repository{
			ObjectMeta: metav1.ObjectMeta{Name: "widgets"},
			Spec: codev1alpha1.RepositorySpec{
				ConnectionRef: "conn",
				Name:          "widgets",
				Description:   "initial",
			},
		},
	}
}

func reconcileRepository(t *testing.T, r *Reconciler) (ctrl.Result, error) {
	t.Helper()
	return r.Reconcile(context.Background(), mcreconcile.Request{
		Request:     ctrl.Request{NamespacedName: types.NamespacedName{Name: "widgets"}},
		ClusterName: mcmanager.LocalCluster,
	})
}

func TestIdentityMutationCannotRedirectEnsureAndDelete(t *testing.T) {
	objects := repositoryTestObjects()
	r, c, b := newRepositoryTestReconciler(t, objects...)
	if _, err := reconcileRepository(t, r); err != nil {
		t.Fatalf("add finalizer reconcile: %v", err)
	}
	if _, err := reconcileRepository(t, r); err != nil {
		t.Fatalf("ensure reconcile: %v", err)
	}
	if len(b.ensured) != 1 {
		t.Fatalf("ensure calls = %d, want 1", len(b.ensured))
	}

	var repo codev1alpha1.Repository
	if err := c.Get(context.Background(), client.ObjectKey{Name: "widgets"}, &repo); err != nil {
		t.Fatalf("get repository: %v", err)
	}
	if repo.Status.Identity == nil || repo.Status.Identity.Name != "widgets" || repo.Status.Identity.Owner != "acme" {
		t.Fatalf("recorded identity = %+v", repo.Status.Identity)
	}
	// A fake client bypasses CEL admission, exercising the controller defense.
	repo.Spec.Name = "other"
	if err := c.Update(context.Background(), &repo); err != nil {
		t.Fatalf("mutate repository: %v", err)
	}
	if _, err := reconcileRepository(t, r); err == nil {
		t.Fatal("identity mutation unexpectedly reconciled")
	}
	if len(b.ensured) != 1 {
		t.Fatalf("identity mutation made ensure call: %d", len(b.ensured))
	}

	var conflicted codev1alpha1.Repository
	if err := c.Get(context.Background(), client.ObjectKey{Name: "widgets"}, &conflicted); err != nil {
		t.Fatalf("get conflicted repository: %v", err)
	}
	if cond := readyCondition(conflicted.Status.Conditions); cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != codev1alpha1.ReasonIdentityConflict {
		t.Fatalf("identity conflict condition = %+v", cond)
	}
	if err := c.Delete(context.Background(), &conflicted); err != nil {
		t.Fatalf("delete repository: %v", err)
	}
	if _, err := reconcileRepository(t, r); err != nil {
		t.Fatalf("delete reconcile: %v", err)
	}
	if len(b.deleted) != 1 {
		t.Fatalf("delete calls = %d, want 1", len(b.deleted))
	}
	if got := b.deleted[0].Spec.Name; got != "widgets" {
		t.Fatalf("delete targeted repository %q, want recorded widgets", got)
	}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "widgets"}, &conflicted); err == nil {
		t.Fatal("repository still exists after finalizer removal")
	}
}

func TestOwnerPresenceMutationCannotRedirectEnsureAndDelete(t *testing.T) {
	r, c, b := newRepositoryTestReconciler(t, repositoryTestObjects()...)
	if _, err := reconcileRepository(t, r); err != nil {
		t.Fatalf("add finalizer reconcile: %v", err)
	}
	if _, err := reconcileRepository(t, r); err != nil {
		t.Fatalf("ensure reconcile: %v", err)
	}
	if len(b.ensured) != 1 {
		t.Fatalf("ensure calls = %d, want 1", len(b.ensured))
	}

	var repo codev1alpha1.Repository
	if err := c.Get(context.Background(), client.ObjectKey{Name: "widgets"}, &repo); err != nil {
		t.Fatalf("get repository: %v", err)
	}
	if repo.Spec.Owner != "" || repo.Status.Identity == nil || repo.Status.Identity.SpecOwner != "" {
		t.Fatalf("initial owner identity = spec=%q status=%+v", repo.Spec.Owner, repo.Status.Identity)
	}
	// A fake client bypasses CEL admission. This specifically exercises the
	// absent-to-present optional-owner transition that field-level CEL rules can
	// miss on older apiservers.
	repo.Spec.Owner = "other"
	if err := c.Update(context.Background(), &repo); err != nil {
		t.Fatalf("mutate repository owner: %v", err)
	}
	if _, err := reconcileRepository(t, r); err == nil {
		t.Fatal("owner presence mutation unexpectedly reconciled")
	}
	if len(b.ensured) != 1 {
		t.Fatalf("owner presence mutation made ensure call: %d", len(b.ensured))
	}

	var conflicted codev1alpha1.Repository
	if err := c.Get(context.Background(), client.ObjectKey{Name: "widgets"}, &conflicted); err != nil {
		t.Fatalf("get conflicted repository: %v", err)
	}
	if cond := readyCondition(conflicted.Status.Conditions); cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != codev1alpha1.ReasonIdentityConflict {
		t.Fatalf("owner presence conflict condition = %+v", cond)
	}
	if err := c.Delete(context.Background(), &conflicted); err != nil {
		t.Fatalf("delete conflicted repository: %v", err)
	}
	if _, err := reconcileRepository(t, r); err != nil {
		t.Fatalf("delete reconcile: %v", err)
	}
	if len(b.deleted) != 1 {
		t.Fatalf("delete calls = %d, want 1", len(b.deleted))
	}
	if got := b.deleted[0].Spec.Owner; got != "" {
		t.Fatalf("cleanup owner = %q, want original inherited owner intent", got)
	}
}

func TestSameOwnerConnectionRotationPreservesRepositoryIdentity(t *testing.T) {
	objects := repositoryTestObjects()
	objects = append(objects,
		&codev1alpha1.Connection{
			ObjectMeta: metav1.ObjectMeta{Name: "conn-rotated"},
			Spec: codev1alpha1.ConnectionSpec{
				Provider: codev1alpha1.ProviderGitHub,
				Type:     codev1alpha1.CredentialTypePAT,
				Owner:    "acme",
				SecretRef: codev1alpha1.LocalSecretReference{
					Name: "git-token-rotated", Namespace: "default", Key: "token",
				},
			},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "git-token-rotated", Namespace: "default"},
			Data:       map[string][]byte{"token": []byte("rotated-token")},
		},
	)
	r, c, b := newRepositoryTestReconciler(t, objects...)
	if _, err := reconcileRepository(t, r); err != nil {
		t.Fatalf("add finalizer reconcile: %v", err)
	}
	if _, err := reconcileRepository(t, r); err != nil {
		t.Fatalf("first ensure reconcile: %v", err)
	}

	var repo codev1alpha1.Repository
	if err := c.Get(context.Background(), client.ObjectKey{Name: "widgets"}, &repo); err != nil {
		t.Fatalf("get repository: %v", err)
	}
	repo.Spec.ConnectionRef = "conn-rotated"
	if err := c.Update(context.Background(), &repo); err != nil {
		t.Fatalf("rotate repository connection: %v", err)
	}
	if _, err := reconcileRepository(t, r); err != nil {
		t.Fatalf("same-owner connection rotation rejected: %v", err)
	}
	if len(b.ensured) != 2 {
		t.Fatalf("ensure calls after credential rotation = %d, want 2", len(b.ensured))
	}
	if repo.Status.Identity == nil {
		t.Fatal("repository identity disappeared after connection rotation")
	}
	var rotated codev1alpha1.Repository
	if err := c.Get(context.Background(), client.ObjectKey{Name: "widgets"}, &rotated); err != nil {
		t.Fatalf("get rotated repository: %v", err)
	}
	if rotated.Status.Identity == nil || rotated.Status.Identity.ConnectionRef != "conn-rotated" || rotated.Status.Identity.Owner != "acme" {
		t.Fatalf("rotated identity = %+v, want current credential anchor and acme owner", rotated.Status.Identity)
	}

	if err := c.Delete(context.Background(), &rotated); err != nil {
		t.Fatalf("delete rotated repository: %v", err)
	}
	if _, err := reconcileRepository(t, r); err != nil {
		t.Fatalf("rotated repository delete reconcile: %v", err)
	}
	if len(b.deleted) != 1 || b.deleted[0].Spec.ConnectionRef != "conn-rotated" {
		t.Fatalf("cleanup credential anchor = %q, want conn-rotated", b.deleted[0].Spec.ConnectionRef)
	}
}

func TestConnectionRotationToDifferentOwnerIsRejected(t *testing.T) {
	objects := repositoryTestObjects()
	objects = append(objects,
		&codev1alpha1.Connection{
			ObjectMeta: metav1.ObjectMeta{Name: "conn-other"},
			Spec: codev1alpha1.ConnectionSpec{
				Provider: codev1alpha1.ProviderGitHub,
				Type:     codev1alpha1.CredentialTypePAT,
				Owner:    "other",
				SecretRef: codev1alpha1.LocalSecretReference{
					Name: "git-token-other", Namespace: "default", Key: "token",
				},
			},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "git-token-other", Namespace: "default"},
			Data:       map[string][]byte{"token": []byte("other-token")},
		},
	)
	r, c, b := newRepositoryTestReconciler(t, objects...)
	if _, err := reconcileRepository(t, r); err != nil {
		t.Fatalf("add finalizer reconcile: %v", err)
	}
	if _, err := reconcileRepository(t, r); err != nil {
		t.Fatalf("first ensure reconcile: %v", err)
	}

	var repo codev1alpha1.Repository
	if err := c.Get(context.Background(), client.ObjectKey{Name: "widgets"}, &repo); err != nil {
		t.Fatalf("get repository: %v", err)
	}
	repo.Spec.ConnectionRef = "conn-other"
	if err := c.Update(context.Background(), &repo); err != nil {
		t.Fatalf("mutate repository connection: %v", err)
	}
	if _, err := reconcileRepository(t, r); err == nil {
		t.Fatal("different-owner connection rotation unexpectedly reconciled")
	}
	if len(b.ensured) != 1 {
		t.Fatalf("different-owner mutation made ensure call: %d", len(b.ensured))
	}

	if err := c.Get(context.Background(), client.ObjectKey{Name: "widgets"}, &repo); err != nil {
		t.Fatalf("get conflicted repository: %v", err)
	}
	if cond := readyCondition(repo.Status.Conditions); cond == nil || cond.Reason != codev1alpha1.ReasonIdentityConflict {
		t.Fatalf("different-owner condition = %+v", cond)
	}
	if err := c.Delete(context.Background(), &repo); err != nil {
		t.Fatalf("delete conflicted repository: %v", err)
	}
	if _, err := reconcileRepository(t, r); err != nil {
		t.Fatalf("conflicted repository delete reconcile: %v", err)
	}
	if len(b.deleted) != 1 || b.deleted[0].Spec.ConnectionRef != "conn" {
		t.Fatalf("conflicted cleanup credential anchor = %q, want original conn", b.deleted[0].Spec.ConnectionRef)
	}
}

func TestDeleteRetainsFinalizerWhenRecordedBackendMissing(t *testing.T) {
	r, c, _ := newRepositoryTestReconciler(t, repositoryTestObjects()...)
	if _, err := reconcileRepository(t, r); err != nil {
		t.Fatalf("add finalizer reconcile: %v", err)
	}
	if _, err := reconcileRepository(t, r); err != nil {
		t.Fatalf("ensure reconcile: %v", err)
	}
	var repo codev1alpha1.Repository
	if err := c.Get(context.Background(), client.ObjectKey{Name: "widgets"}, &repo); err != nil {
		t.Fatalf("get repository: %v", err)
	}
	if err := c.Delete(context.Background(), &repo); err != nil {
		t.Fatalf("delete repository: %v", err)
	}
	r.Backends = backend.NewRegistry()
	if _, err := reconcileRepository(t, r); err == nil {
		t.Fatal("missing backend cleanup unexpectedly succeeded")
	}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "widgets"}, &repo); err != nil {
		t.Fatalf("get pending repository: %v", err)
	}
	if !controllerutil.ContainsFinalizer(&repo, codev1alpha1.FinalizerRepository) {
		t.Fatalf("missing backend released finalizer: %v", repo.Finalizers)
	}
}

func TestDeleteWithoutRecordedIdentityUsesCurrentBackend(t *testing.T) {
	r, c, b := newRepositoryTestReconciler(t, repositoryTestObjects()...)
	if _, err := reconcileRepository(t, r); err != nil {
		t.Fatalf("add finalizer reconcile: %v", err)
	}
	var repo codev1alpha1.Repository
	if err := c.Get(context.Background(), client.ObjectKey{Name: "widgets"}, &repo); err != nil {
		t.Fatalf("get repository: %v", err)
	}
	if repo.Status.Identity != nil || repo.Status.RepoID != "" {
		t.Fatalf("pre-ensure status = %+v, want no recorded identity", repo.Status)
	}
	if err := c.Delete(context.Background(), &repo); err != nil {
		t.Fatalf("delete repository: %v", err)
	}
	if _, err := reconcileRepository(t, r); err != nil {
		t.Fatalf("legacy delete reconcile: %v", err)
	}
	if len(b.deleted) != 1 || b.deleted[0].Spec.Name != "widgets" {
		t.Fatalf("current-identity cleanup = %+v, want widgets", b.deleted)
	}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "widgets"}, &repo); err == nil {
		t.Fatal("repository still exists after cleanup")
	}
}

func TestDeleteWithoutRecordedIdentityRetainsFinalizerWhenBackendMissing(t *testing.T) {
	r, c, _ := newRepositoryTestReconciler(t, repositoryTestObjects()...)
	if _, err := reconcileRepository(t, r); err != nil {
		t.Fatalf("add finalizer reconcile: %v", err)
	}
	var repo codev1alpha1.Repository
	if err := c.Get(context.Background(), client.ObjectKey{Name: "widgets"}, &repo); err != nil {
		t.Fatalf("get repository: %v", err)
	}
	if err := c.Delete(context.Background(), &repo); err != nil {
		t.Fatalf("delete repository: %v", err)
	}
	r.Backends = backend.NewRegistry()
	if _, err := reconcileRepository(t, r); err == nil {
		t.Fatal("legacy cleanup unexpectedly succeeded without backend")
	}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "widgets"}, &repo); err != nil {
		t.Fatalf("get pending repository: %v", err)
	}
	if !controllerutil.ContainsFinalizer(&repo, codev1alpha1.FinalizerRepository) {
		t.Fatalf("legacy missing-backend cleanup released finalizer: %v", repo.Finalizers)
	}
}

func readyCondition(conditions []metav1.Condition) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == codev1alpha1.ConditionReady {
			return &conditions[i]
		}
	}
	return nil
}
