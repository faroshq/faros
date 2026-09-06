/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package repository reconciles Repository CRs: it ensures the repository
// exists on the git host (creating it via the backend) and, on delete, removes
// it before releasing the finalizer.
package repository

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	codev1alpha1 "github.com/faroshq/provider-code/apis/v1alpha1"
	"github.com/faroshq/provider-code/backend"
	"github.com/faroshq/provider-code/controller/shared"
)

// Reconciler ensures Repository CRs against the git host.
type Reconciler struct {
	Manager  mcmanager.Manager
	Backends *backend.Registry
}

// SetupWithManager wires the reconciler into the multicluster manager.
func (r *Reconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.Manager = mgr
	return mcbuilder.ControllerManagedBy(mgr).
		Named("code-repository").
		For(&codev1alpha1.Repository{}).
		Complete(r)
}

// Reconcile ensures one Repository.
func (r *Reconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := klog.FromContext(ctx).WithValues("repository", req.Name, "cluster", req.ClusterName)

	c, err := shared.ClusterClient(ctx, r.Manager, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	var repo codev1alpha1.Repository
	if err := c.Get(ctx, req.NamespacedName, &repo); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Deletion path.
	if !repo.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, c, &repo)
	}

	if controllerutil.AddFinalizer(&repo, codev1alpha1.FinalizerRepository) {
		if err := c.Update(ctx, &repo); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Name and explicit owner identify the external repository and current CRDs
	// reject their mutation with CEL. ConnectionRef has a narrower safe
	// rotation contract enforced below: changing credentials is accepted only
	// when the resolved provider, endpoint, and effective owner stay identical.
	// These checks also cover objects created under an older schema and fake
	// clients used by tests. Never let an edited Repository object become an
	// instruction to adopt or create another host repository.
	if err := validateSpecIdentity(&repo); err != nil {
		return r.fail(ctx, c, &repo, codev1alpha1.ReasonIdentityConflict, err.Error())
	}

	b, conn, cred, notReady := r.resolve(ctx, c, &repo)
	ready := notReady == ""

	if !ready {
		// notReady names exactly which piece is missing (wrong connectionRef,
		// unknown provider, or unreadable credential) so the status is actionable.
		return r.fail(ctx, c, &repo, "ConnectionNotReady", notReady)
	}
	rotateCredentialRef := false
	if repo.Status.Identity != nil {
		if err := validateResolvedIdentity(&repo, conn); err != nil {
			return r.fail(ctx, c, &repo, codev1alpha1.ReasonIdentityConflict, err.Error())
		}
		// Rotating credentials is safe when the resolved external identity stays
		// the same. Keep the old anchor until EnsureRepository succeeds so a
		// failed rotation can still clean up through the last known-good Secret.
		rotateCredentialRef = repo.Status.Identity.ConnectionRef != repo.Spec.ConnectionRef
	} else {
		// Persist the identity before EnsureRepository. A successful remote
		// create followed by a status-write failure must still leave enough
		// information for a later reconcile/delete to target that same repo.
		identity := repositoryIdentity(&repo, conn)
		base := repo.DeepCopy()
		repo.Status.Identity = &identity
		if err := c.Status().Patch(ctx, &repo, client.MergeFrom(base)); err != nil {
			return ctrl.Result{}, fmt.Errorf("record repository identity: %w", err)
		}
	}

	res, err := b.EnsureRepository(ctx, conn, cred, &repo)
	if err != nil {
		return r.fail(ctx, c, &repo, "EnsureFailed", err.Error())
	}
	if repo.Status.RepoID != "" && repo.Status.RepoID != res.RepoID {
		return r.fail(ctx, c, &repo, codev1alpha1.ReasonIdentityConflict,
			fmt.Sprintf("external repository id changed from %q to %q; delete and recreate the Repository", repo.Status.RepoID, res.RepoID))
	}
	if rotateCredentialRef {
		repo.Status.Identity.ConnectionRef = repo.Spec.ConnectionRef
	}

	repo.Status.ObservedGeneration = repo.Generation
	repo.Status.RepoID = res.RepoID
	repo.Status.HTMLURL = res.HTMLURL
	repo.Status.CloneURL = res.CloneURL
	repo.Status.SSHURL = res.SSHURL
	shared.SetCondition(&repo.Status.Conditions, codev1alpha1.ConditionReady, metav1.ConditionTrue, codev1alpha1.ReasonReady, "", repo.Generation)
	if err := c.Status().Update(ctx, &repo); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("Repository ensured", "url", res.HTMLURL)
	return ctrl.Result{}, nil
}

// finalize deletes the host-side repository before releasing the Kubernetes
// finalizer. When identity has been recorded, all lookup and delete inputs are
// reconstructed from that identity so edits to the desired object or its
// Connection cannot redirect cleanup. A missing backend/credential leaves the
// finalizer in place: the provider cannot prove the external resource is gone.
func (r *Reconciler) finalize(ctx context.Context, c client.Client, repo *codev1alpha1.Repository) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(repo, codev1alpha1.FinalizerRepository) {
		return ctrl.Result{}, nil
	}

	var (
		b        backend.GitBackend
		conn     *codev1alpha1.Connection
		cred     backend.Credential
		cleanup  *codev1alpha1.Repository
		notReady string
	)
	if repo.Status.Identity != nil {
		b, conn, cred, cleanup, notReady = r.resolveRecordedDelete(ctx, c, repo)
	} else {
		// Legacy objects may have status URLs/ID but no Identity field. A delete
		// can also race the pre-Ensure identity patch, leaving status empty even
		// though EnsureRepository may already have created or adopted the host
		// repository. Resolve the current connection as the only available
		// cleanup authority and retain the finalizer if it is unavailable. GitHub
		// treats a missing repository as success, so this remains idempotent for
		// objects deleted before their first ensure.
		b, conn, cred, notReady = r.resolve(ctx, c, repo)
		cleanup = repo
	}
	if notReady != "" {
		return r.fail(ctx, c, repo, "DeleteNotReady", notReady)
	}
	if err := b.DeleteRepository(ctx, conn, cred, cleanup); err != nil {
		return r.fail(ctx, c, repo, "DeleteFailed", err.Error())
	}
	return r.removeFinalizer(ctx, c, repo)
}

func (r *Reconciler) resolveRecordedDelete(ctx context.Context, c client.Client, repo *codev1alpha1.Repository) (backend.GitBackend, *codev1alpha1.Connection, backend.Credential, *codev1alpha1.Repository, string) {
	identity := repo.Status.Identity
	if identity == nil || identity.ConnectionRef == "" || identity.Name == "" || identity.Owner == "" || identity.Provider == "" {
		return nil, nil, backend.Credential{}, nil, "recorded repository identity is incomplete; restore the identity or remove the resource only after verifying the host repository is absent"
	}
	conn, err := shared.ResolveConnection(ctx, c, identity.ConnectionRef)
	if err != nil {
		return nil, nil, backend.Credential{}, nil, err.Error()
	}
	b, ok := r.Backends.Get(string(identity.Provider))
	if !ok {
		return nil, conn, backend.Credential{}, nil, fmt.Sprintf("no backend registered for recorded provider %q", identity.Provider)
	}
	cred, err := shared.ResolveCredential(ctx, c, conn)
	if err != nil {
		return b, conn, backend.Credential{}, nil, fmt.Sprintf("credential for recorded connection %q unavailable: %v", identity.ConnectionRef, err)
	}

	// Keep the current credential SecretRef (credential rotation remains
	// supported), while restoring all external routing identity from status.
	cleanupConn := conn.DeepCopy()
	cleanupConn.Spec.Provider = identity.Provider
	cleanupConn.Spec.BaseURL = identity.BaseURL
	cleanupConn.Spec.Owner = identity.Owner
	cleanupRepo := repo.DeepCopy()
	cleanupRepo.Spec.ConnectionRef = identity.ConnectionRef
	cleanupRepo.Spec.Name = identity.Name
	// Preserve the raw owner intent as well as the effective owner restored on
	// cleanupConn. An empty spec.owner means the original Repository inherited
	// the Connection owner; replacing it with the effective value would look
	// like an identity mutation to the backend guard.
	cleanupRepo.Spec.Owner = identity.SpecOwner
	return b, cleanupConn, cred, cleanupRepo, ""
}

func (r *Reconciler) removeFinalizer(ctx context.Context, c client.Client, repo *codev1alpha1.Repository) (ctrl.Result, error) {
	controllerutil.RemoveFinalizer(repo, codev1alpha1.FinalizerRepository)
	if err := c.Update(ctx, repo); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func repositoryIdentity(repo *codev1alpha1.Repository, conn *codev1alpha1.Connection) codev1alpha1.RepositoryIdentity {
	owner := repo.Spec.Owner
	if owner == "" {
		owner = conn.Spec.Owner
	}
	return codev1alpha1.RepositoryIdentity{
		ConnectionRef: repo.Spec.ConnectionRef,
		Provider:      conn.Spec.Provider,
		BaseURL:       conn.Spec.BaseURL,
		Owner:         owner,
		SpecOwner:     repo.Spec.Owner,
		Name:          repo.Spec.Name,
	}
}

func validateSpecIdentity(repo *codev1alpha1.Repository) error {
	identity := repo.Status.Identity
	if identity == nil {
		return nil
	}
	if identity.Name != repo.Spec.Name {
		return fmt.Errorf("spec.name changed from provisioned repository %q to %q; delete and recreate the Repository", identity.Name, repo.Spec.Name)
	}
	if identity.SpecOwner != repo.Spec.Owner {
		return fmt.Errorf("spec.owner changed after provisioning; delete and recreate the Repository")
	}
	return nil
}

func validateResolvedIdentity(repo *codev1alpha1.Repository, conn *codev1alpha1.Connection) error {
	identity := repo.Status.Identity
	if identity == nil {
		return nil
	}
	current := repositoryIdentity(repo, conn)
	if identity.Provider != current.Provider || identity.BaseURL != current.BaseURL || identity.Owner != current.Owner {
		return fmt.Errorf("connection %q no longer resolves to the provisioned git host identity; restore provider, baseURL, and owner or delete and recreate the Repository", repo.Spec.ConnectionRef)
	}
	return nil
}

// resolve loads the backend, Connection, and credential for repo. The returned
// string is empty on success, otherwise a specific human-readable reason naming
// the missing piece (used for the NotReady status; the caller decides whether
// that's fatal for create or tolerable for delete).
func (r *Reconciler) resolve(ctx context.Context, c client.Client, repo *codev1alpha1.Repository) (backend.GitBackend, *codev1alpha1.Connection, backend.Credential, string) {
	conn, err := shared.ResolveConnection(ctx, c, repo.Spec.ConnectionRef)
	if err != nil {
		return nil, nil, backend.Credential{}, err.Error()
	}
	b, ok := r.Backends.Get(string(conn.Spec.Provider))
	if !ok {
		return nil, conn, backend.Credential{}, fmt.Sprintf("no backend registered for provider %q (connection %q)", conn.Spec.Provider, conn.Name)
	}
	cred, err := shared.ResolveCredential(ctx, c, conn)
	if err != nil {
		return b, conn, backend.Credential{}, fmt.Sprintf("credential for connection %q unavailable: %v", conn.Name, err)
	}
	return b, conn, cred, ""
}

func (r *Reconciler) fail(ctx context.Context, c client.Client, repo *codev1alpha1.Repository, reason, msg string) (ctrl.Result, error) {
	repo.Status.ObservedGeneration = repo.Generation
	shared.SetCondition(&repo.Status.Conditions, codev1alpha1.ConditionReady, metav1.ConditionFalse, reason, msg, repo.Generation)
	if err := c.Status().Update(ctx, repo); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, fmt.Errorf("%s: %s", reason, msg)
}
