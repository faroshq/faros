/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package changerequest reconciles reviewed git-host changes.
package changerequest

import (
	"context"
	"fmt"
	"reflect"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	codev1alpha1 "github.com/faroshq/provider-code/apis/v1alpha1"
	"github.com/faroshq/provider-code/backend"
	"github.com/faroshq/provider-code/controller/shared"
)

type Reconciler struct {
	Manager  mcmanager.Manager
	Backends *backend.Registry
}

func (r *Reconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.Manager = mgr
	return mcbuilder.ControllerManagedBy(mgr).Named("code-changerequests").For(&codev1alpha1.ChangeRequest{}).Complete(r)
}

func (r *Reconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	c, err := shared.ClusterClient(ctx, r.Manager, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}
	var cr codev1alpha1.ChangeRequest
	if err := c.Get(ctx, req.NamespacedName, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if !cr.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	result, reconcileErr := r.ensure(ctx, c, &cr)
	next := cr.DeepCopy()
	next.Status.ObservedGeneration = cr.Generation
	if reconcileErr != nil {
		next.Status.Phase = codev1alpha1.ChangeRequestPhaseFailed
		shared.SetCondition(&next.Status.Conditions, codev1alpha1.ConditionReady, metav1.ConditionFalse, codev1alpha1.ReasonError, reconcileErr.Error(), cr.Generation)
		if err := updateStatus(ctx, c, next); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	next.Status.Number, next.Status.URL, next.Status.HeadSHA = result.Number, result.URL, result.HeadSHA
	next.Status.Approvals, next.Status.MergeSHA = result.Approvals, result.MergeSHA
	switch {
	case result.Merged:
		next.Status.Phase = codev1alpha1.ChangeRequestPhaseMerged
	case !result.Open:
		next.Status.Phase = codev1alpha1.ChangeRequestPhaseClosed
	case result.Approvals >= cr.Spec.RequiredApprovals && cr.Spec.RequiredApprovals > 0:
		next.Status.Phase = codev1alpha1.ChangeRequestPhaseApproved
	default:
		next.Status.Phase = codev1alpha1.ChangeRequestPhaseOpen
	}
	ready := metav1.ConditionFalse
	if result.Merged {
		ready = metav1.ConditionTrue
	}
	shared.SetCondition(&next.Status.Conditions, codev1alpha1.ConditionReady, ready, codev1alpha1.ReasonReconciling, "Waiting for review and merge.", cr.Generation)
	if result.Merged {
		shared.SetCondition(&next.Status.Conditions, codev1alpha1.ConditionReady, ready, codev1alpha1.ReasonReady, "Change request merged.", cr.Generation)
	}
	if err := updateStatus(ctx, c, next); err != nil {
		return ctrl.Result{}, err
	}
	if result.Merged || !result.Open {
		return ctrl.Result{}, nil
	}
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *Reconciler) ensure(ctx context.Context, c client.Client, cr *codev1alpha1.ChangeRequest) (backend.ChangeRequestResult, error) {
	if cr.Spec.MergePolicy == codev1alpha1.ChangeRequestMergePolicyAfterApproval && cr.Spec.RequiredApprovals < 1 {
		return backend.ChangeRequestResult{}, fmt.Errorf("requiredApprovals must be at least 1 for mergePolicy AfterApproval")
	}
	if r.Backends == nil {
		return backend.ChangeRequestResult{}, fmt.Errorf("git backends are unavailable")
	}
	repo, err := shared.ResolveRepository(ctx, c, cr.Spec.RepositoryRef)
	if err != nil {
		return backend.ChangeRequestResult{}, err
	}
	conn, err := shared.ResolveConnection(ctx, c, repo.Spec.ConnectionRef)
	if err != nil {
		return backend.ChangeRequestResult{}, err
	}
	b, ok := r.Backends.Get(string(conn.Spec.Provider))
	if !ok {
		return backend.ChangeRequestResult{}, fmt.Errorf("git provider %q is not registered", conn.Spec.Provider)
	}
	requester, ok := b.(backend.ChangeRequester)
	if !ok {
		return backend.ChangeRequestResult{}, fmt.Errorf("git provider %q does not support change requests", conn.Spec.Provider)
	}
	cred, err := shared.ResolveCredential(ctx, c, conn)
	if err != nil {
		return backend.ChangeRequestResult{}, err
	}
	result, err := requester.EnsureChangeRequest(ctx, conn, cred, repo, backend.ChangeRequestInput{BaseBranch: cr.Spec.BaseBranch, HeadBranch: cr.Spec.HeadBranch, Title: cr.Spec.Title, Body: cr.Spec.Body})
	if err != nil {
		return result, err
	}
	if cr.Spec.MergePolicy == codev1alpha1.ChangeRequestMergePolicyAfterApproval && result.Open && result.Approvals >= cr.Spec.RequiredApprovals {
		return requester.MergeChangeRequest(ctx, conn, cred, repo, result.Number, result.HeadSHA)
	}
	return result, nil
}

func updateStatus(ctx context.Context, c client.Client, next *codev1alpha1.ChangeRequest) error {
	var current codev1alpha1.ChangeRequest
	if err := c.Get(ctx, client.ObjectKey{Name: next.Name}, &current); err != nil {
		return err
	}
	if reflect.DeepEqual(current.Status, next.Status) {
		return nil
	}
	current.Status = next.Status
	return c.Status().Update(ctx, &current)
}
