/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package repositorysync projects reviewed deployment YAML from Git into the
// tenant workspace. It owns desired-state ingestion only; the Deployments
// provider remains the sole runtime reconciler.
package repositorysync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"reflect"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiMeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	deploymentsv1alpha1 "github.com/faroshq/provider-deployments/apis/v1alpha1"
)

const (
	ownerAnnotation                = "deployments.faros.sh/repository-sync"
	pathAnnotation                 = "deployments.faros.sh/source-path"
	revisionAnnotation             = "deployments.faros.sh/config-revision"
	deletionPolicyAnnotation       = "deployments.faros.sh/applied-deletion-policy"
	repositorySyncFinalizer        = "deployments.faros.sh/repository-sync-cleanup"
	legacyOwnerAnnotation          = "code.faros.sh/repository-sync"
	legacyPathAnnotation           = "code.faros.sh/source-path"
	legacyRevisionAnnotation       = "code.faros.sh/config-revision"
	legacyDeletionPolicyAnnotation = "code.faros.sh/applied-deletion-policy"
	conditionReady                 = "Ready"
	defaultPath                    = ".faros"
	defaultInterval                = 30 * time.Second
)

var deploymentsGV = schema.GroupVersion{Group: "deployments.faros.sh", Version: "v1alpha1"}

type Reconciler struct {
	Manager mcmanager.Manager
	Source  SourceReader
}

func (r *Reconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.Manager = mgr
	return mcbuilder.ControllerManagedBy(mgr).Named("deployments-repositorysyncs").For(&deploymentsv1alpha1.RepositorySync{}).Complete(r)
}

func (r *Reconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	cluster, err := r.Manager.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("get tenant cluster %s: %w", req.ClusterName, err)
	}
	c := cluster.GetClient()
	var sync deploymentsv1alpha1.RepositorySync
	if err := c.Get(ctx, req.NamespacedName, &sync); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if !sync.DeletionTimestamp.IsZero() {
		if !containsString(sync.Finalizers, repositorySyncFinalizer) {
			return ctrl.Result{}, nil
		}
		pending, err := cleanupInventory(ctx, c, &sync)
		if err != nil {
			return ctrl.Result{}, err
		}
		if pending {
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		if cleaner, ok := r.Source.(SourceCleaner); ok {
			if err := cleaner.Cleanup(ctx, c, sync.Name); err != nil {
				return ctrl.Result{}, err
			}
		}
		sync.Finalizers = removeString(sync.Finalizers, repositorySyncFinalizer)
		if err := c.Update(ctx, &sync); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	if !containsString(sync.Finalizers, repositorySyncFinalizer) {
		sync.Finalizers = append(sync.Finalizers, repositorySyncFinalizer)
		if err := c.Update(ctx, &sync); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}
	interval := defaultInterval
	if sync.Spec.IntervalSeconds > 0 {
		interval = time.Duration(sync.Spec.IntervalSeconds) * time.Second
	}
	revision, inventory, reconcileErr := r.sync(ctx, c, &sync, string(req.ClusterName))
	next := sync.DeepCopy()
	next.Status.ObservedGeneration = sync.Generation
	next.Status.ObservedRevision = revision
	if errors.Is(reconcileErr, errCheckoutPending) {
		next.Status.Phase = deploymentsv1alpha1.RepositorySyncPhaseReconciling
		apiMeta.SetStatusCondition(&next.Status.Conditions, metav1.Condition{Type: conditionReady, Status: metav1.ConditionFalse, Reason: "CheckoutPending", Message: "Waiting for Code to produce the repository checkout.", ObservedGeneration: sync.Generation})
		if err := updateStatus(ctx, c, next); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}
	if reconcileErr != nil {
		next.Status.Phase = deploymentsv1alpha1.RepositorySyncPhaseFailed
		apiMeta.SetStatusCondition(&next.Status.Conditions, metav1.Condition{Type: conditionReady, Status: metav1.ConditionFalse, Reason: "Error", Message: reconcileErr.Error(), ObservedGeneration: sync.Generation})
		if err := updateStatus(ctx, c, next); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: interval}, nil
	}
	next.Status.Phase = deploymentsv1alpha1.RepositorySyncPhaseReady
	next.Status.AppliedRevision = revision
	next.Status.Inventory = inventory
	apiMeta.SetStatusCondition(&next.Status.Conditions, metav1.Condition{Type: conditionReady, Status: metav1.ConditionTrue, Reason: "Ready", Message: "Repository configuration applied.", ObservedGeneration: sync.Generation})
	if err := updateStatus(ctx, c, next); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: interval}, nil
}

func (r *Reconciler) sync(ctx context.Context, c client.Client, sync *deploymentsv1alpha1.RepositorySync, scope string) (string, []deploymentsv1alpha1.RepositorySyncInventoryItem, error) {
	if r.Source == nil {
		return "", nil, fmt.Errorf("Code repository checkout reader is unavailable")
	}
	root, err := cleanRoot(sync.Spec.Path)
	if err != nil {
		return "", nil, err
	}
	checkout, err := r.Source.Checkout(ctx, c, RepositorySource{SyncName: sync.Name, RepositoryRef: sync.Spec.RepositoryRef, Ref: sync.Spec.Ref, Path: root, Scope: scope})
	if err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(checkout.CommitSHA) == "" {
		return "", nil, fmt.Errorf("git backend resolved the repository without a commit SHA")
	}
	for _, skipped := range checkout.Skipped {
		if strings.Contains(skipped, "tree truncated") || withinRoot(skipped, root) {
			return checkout.CommitSHA, nil, fmt.Errorf("source tree is incomplete: %q was skipped", skipped)
		}
	}
	docs, err := parseDocuments(checkout.Files, root)
	if err != nil {
		return checkout.CommitSHA, nil, err
	}
	for _, doc := range docs {
		if doc.GetKind() == "Deployment" {
			if err := unstructured.SetNestedField(doc.Object, checkout.CommitSHA, "spec", "rolloutID"); err != nil {
				return checkout.CommitSHA, nil, err
			}
		}
	}
	inventory, err := applyDocuments(ctx, c, sync, checkout.CommitSHA, docs)
	if err != nil {
		return checkout.CommitSHA, nil, err
	}
	if sync.Spec.Prune {
		if err := prune(ctx, c, sync, inventory); err != nil {
			return checkout.CommitSHA, nil, err
		}
	}
	sort.Slice(inventory, func(i, j int) bool {
		if inventory[i].Kind == inventory[j].Kind {
			return inventory[i].Name < inventory[j].Name
		}
		return inventory[i].Kind < inventory[j].Kind
	})
	return checkout.CommitSHA, inventory, nil
}

func applyDocuments(ctx context.Context, c client.Client, sync *deploymentsv1alpha1.RepositorySync, revision string, docs []sourcedDocument) ([]deploymentsv1alpha1.RepositorySyncInventoryItem, error) {
	if err := validateDocuments(ctx, c, sync, docs); err != nil {
		return nil, err
	}
	ordered := append([]sourcedDocument(nil), docs...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].GetKind() == "Release" && ordered[j].GetKind() != "Release" })
	inventory := make([]deploymentsv1alpha1.RepositorySyncInventoryItem, 0, len(docs))
	for _, doc := range ordered {
		item, err := applyDocument(ctx, c, sync, revision, doc)
		if err != nil {
			return nil, err
		}
		inventory = append(inventory, item)
	}
	return inventory, nil
}

type sourcedDocument struct {
	*unstructured.Unstructured
	sourcePath string
}

func parseDocuments(files []SourceFile, root string) ([]sourcedDocument, error) {
	var out []sourcedDocument
	seen := map[string]string{}
	for _, file := range files {
		if file.Delete || !withinRoot(file.Path, root) || (path.Ext(file.Path) != ".yaml" && path.Ext(file.Path) != ".yml") {
			continue
		}
		dec := utilyaml.NewYAMLOrJSONDecoder(bytes.NewBufferString(file.Content), 64<<10)
		for index := 1; ; index++ {
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				if err == io.EOF {
					break
				}
				return nil, fmt.Errorf("parse %s document %d: %w", file.Path, index, err)
			}
			if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
				continue
			}
			var object map[string]any
			if err := json.Unmarshal(raw, &object); err != nil {
				return nil, fmt.Errorf("decode %s document %d: %w", file.Path, index, err)
			}
			u := &unstructured.Unstructured{Object: object}
			if u.GetAPIVersion() != deploymentsGV.String() || (u.GetKind() != "Release" && u.GetKind() != "Deployment") {
				return nil, fmt.Errorf("%s document %d: only %s Release and Deployment are allowed", file.Path, index, deploymentsGV.String())
			}
			if strings.TrimSpace(u.GetName()) == "" {
				return nil, fmt.Errorf("%s document %d: metadata.name is required", file.Path, index)
			}
			if problems := utilvalidation.IsDNS1123Subdomain(u.GetName()); len(problems) > 0 {
				return nil, fmt.Errorf("%s document %d: metadata.name is invalid: %s", file.Path, index, strings.Join(problems, ", "))
			}
			if _, found, err := unstructured.NestedMap(u.Object, "spec"); err != nil || !found {
				return nil, fmt.Errorf("%s document %d: spec is required", file.Path, index)
			}
			if err := validateGitMetadata(u); err != nil {
				return nil, fmt.Errorf("%s document %d: %w", file.Path, index, err)
			}
			// Git controls identity + spec, not Kubernetes lifecycle metadata.
			u.Object["metadata"] = map[string]any{"name": u.GetName()}
			key := u.GetKind() + "/" + u.GetName()
			if prior, ok := seen[key]; ok {
				return nil, fmt.Errorf("duplicate %s in %s and %s", key, prior, file.Path)
			}
			seen[key] = file.Path
			out = append(out, sourcedDocument{Unstructured: u, sourcePath: file.Path})
		}
	}
	return out, nil
}

func validateGitMetadata(u *unstructured.Unstructured) error {
	if _, found := u.Object["status"]; found {
		return fmt.Errorf("status is controller-owned and must not be supplied")
	}
	if u.GetNamespace() != "" {
		return fmt.Errorf("metadata.namespace is not allowed for cluster-scoped resources")
	}
	if len(u.GetOwnerReferences()) > 0 {
		return fmt.Errorf("metadata.ownerReferences is not allowed")
	}
	if len(u.GetFinalizers()) > 0 {
		return fmt.Errorf("metadata.finalizers is not allowed")
	}
	for key := range u.GetAnnotations() {
		if strings.HasPrefix(key, "code.faros.sh/") || strings.HasPrefix(key, "deployments.faros.sh/") {
			return fmt.Errorf("reserved annotation %q is not allowed", key)
		}
	}
	metadata, _, _ := unstructured.NestedMap(u.Object, "metadata")
	for _, field := range []string{"uid", "resourceVersion", "generation", "creationTimestamp", "deletionTimestamp", "managedFields"} {
		if _, ok := metadata[field]; ok {
			return fmt.Errorf("metadata.%s is server-owned and must not be supplied", field)
		}
	}
	return nil
}

// validateDocuments performs every deterministic and workspace lookup before
// the first write, so a malformed later document cannot partially apply an
// otherwise valid-looking revision.
func validateDocuments(ctx context.Context, c client.Client, sync *deploymentsv1alpha1.RepositorySync, docs []sourcedDocument) error {
	releases := map[string]bool{}
	for _, doc := range docs {
		if doc.GetKind() == "Release" {
			releases[doc.GetName()] = true
			if err := validateRelease(doc.Unstructured); err != nil {
				return fmt.Errorf("%s: %w", doc.sourcePath, err)
			}
		}
	}
	// Preflight collisions for the complete desired set before any create or
	// update. applyDocument repeats these checks to close the read/write race.
	for _, doc := range docs {
		existing := &unstructured.Unstructured{}
		existing.SetGroupVersionKind(deploymentsGV.WithKind(doc.GetKind()))
		err := c.Get(ctx, client.ObjectKey{Name: doc.GetName()}, existing)
		if apierrors.IsNotFound(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("preflight %s %q: %w", doc.GetKind(), doc.GetName(), err)
		}
		if repositorySyncOwner(existing.GetAnnotations()) != sync.Name {
			return fmt.Errorf("%s %q is not owned by RepositorySync %q", doc.GetKind(), doc.GetName(), sync.Name)
		}
		if doc.GetKind() == "Release" && !reflect.DeepEqual(existing.Object["spec"], doc.Object["spec"]) {
			return fmt.Errorf("immutable Release %q already exists with different spec", doc.GetName())
		}
	}
	for _, doc := range docs {
		if doc.GetKind() != "Deployment" {
			continue
		}
		releaseRef, err := validateDeployment(doc.Unstructured)
		if err != nil {
			return fmt.Errorf("%s: %w", doc.sourcePath, err)
		}
		if releases[releaseRef] {
			continue
		}
		existing := &unstructured.Unstructured{}
		existing.SetGroupVersionKind(deploymentsGV.WithKind("Release"))
		if err := c.Get(ctx, client.ObjectKey{Name: releaseRef}, existing); err != nil {
			if apierrors.IsNotFound(err) {
				return fmt.Errorf("%s: Deployment references Release %q absent from this revision and workspace", doc.sourcePath, releaseRef)
			}
			return err
		}
	}
	return nil
}

func validateRelease(u *unstructured.Unstructured) error {
	repositoryRef, found, _ := unstructured.NestedString(u.Object, "spec", "source", "repositoryRef")
	if !found || strings.TrimSpace(repositoryRef) == "" {
		return fmt.Errorf("Release spec.source.repositoryRef is required")
	}
	revision, found, _ := unstructured.NestedString(u.Object, "spec", "source", "revision")
	if !found || strings.TrimSpace(revision) == "" {
		return fmt.Errorf("Release spec.source.revision is required")
	}
	blueprint, found, _ := unstructured.NestedString(u.Object, "spec", "blueprintRef", "name")
	if !found || strings.TrimSpace(blueprint) == "" {
		return fmt.Errorf("Release spec.blueprintRef.name is required")
	}
	artifacts, found, err := unstructured.NestedSlice(u.Object, "spec", "artifacts")
	if err != nil || !found {
		return fmt.Errorf("Release spec.artifacts must be a list")
	}
	seen := map[string]bool{}
	for i, raw := range artifacts {
		item, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("Release spec.artifacts[%d] must be an object", i)
		}
		name, _ := item["name"].(string)
		image, _ := item["image"].(string)
		if strings.TrimSpace(name) == "" || strings.TrimSpace(image) == "" {
			return fmt.Errorf("Release spec.artifacts[%d].name and image are required", i)
		}
		if seen[name] {
			return fmt.Errorf("Release artifact name %q is duplicated", name)
		}
		seen[name] = true
	}
	return nil
}

func validateDeployment(u *unstructured.Unstructured) (string, error) {
	releaseRef, found, _ := unstructured.NestedString(u.Object, "spec", "releaseRef")
	if !found || strings.TrimSpace(releaseRef) == "" {
		return "", fmt.Errorf("Deployment spec.releaseRef is required")
	}
	class, _, _ := unstructured.NestedString(u.Object, "spec", "className")
	if class != "" && class != "kro-direct" {
		return "", fmt.Errorf("Deployment spec.className %q is unsupported", class)
	}
	mode, _, _ := unstructured.NestedString(u.Object, "spec", "mode")
	if mode != "" && mode != "development" && mode != "production" {
		return "", fmt.Errorf("Deployment spec.mode %q is invalid", mode)
	}
	policy, _, _ := unstructured.NestedString(u.Object, "spec", "deletionPolicy")
	if policy != "" && policy != "Retain" && policy != "Delete" {
		return "", fmt.Errorf("Deployment spec.deletionPolicy %q is invalid", policy)
	}
	if config, found, err := unstructured.NestedFieldNoCopy(u.Object, "spec", "configuration"); err != nil {
		return "", err
	} else if found {
		if _, ok := config.(map[string]any); !ok {
			return "", fmt.Errorf("Deployment spec.configuration must be an object")
		}
	}
	return releaseRef, nil
}

func applyDocument(ctx context.Context, c client.Client, sync *deploymentsv1alpha1.RepositorySync, revision string, doc sourcedDocument) (deploymentsv1alpha1.RepositorySyncInventoryItem, error) {
	doc.SetGroupVersionKind(deploymentsGV.WithKind(doc.GetKind()))
	annotations := doc.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[ownerAnnotation], annotations[pathAnnotation], annotations[revisionAnnotation] = sync.Name, doc.sourcePath, revision
	delete(annotations, legacyOwnerAnnotation)
	delete(annotations, legacyPathAnnotation)
	delete(annotations, legacyRevisionAnnotation)
	delete(annotations, legacyDeletionPolicyAnnotation)
	if doc.GetKind() == "Deployment" {
		policy, _, _ := unstructured.NestedString(doc.Object, "spec", "deletionPolicy")
		annotations[deletionPolicyAnnotation] = policy
	}
	doc.SetAnnotations(annotations)
	key := client.ObjectKey{Name: doc.GetName()}
	current := &unstructured.Unstructured{}
	current.SetGroupVersionKind(doc.GroupVersionKind())
	err := c.Get(ctx, key, current)
	if apierrors.IsNotFound(err) {
		if err := c.Create(ctx, doc.Unstructured); err != nil {
			return deploymentsv1alpha1.RepositorySyncInventoryItem{}, fmt.Errorf("create %s %q: %w", doc.GetKind(), doc.GetName(), err)
		}
		current = doc.Unstructured
	} else if err != nil {
		return deploymentsv1alpha1.RepositorySyncInventoryItem{}, err
	} else if doc.GetKind() == "Release" {
		if !reflect.DeepEqual(current.Object["spec"], doc.Object["spec"]) {
			return deploymentsv1alpha1.RepositorySyncInventoryItem{}, fmt.Errorf("immutable Release %q already exists with different spec", doc.GetName())
		}
		if owner := repositorySyncOwner(current.GetAnnotations()); owner != sync.Name {
			return deploymentsv1alpha1.RepositorySyncInventoryItem{}, fmt.Errorf("Release %q is owned by RepositorySync %q", doc.GetName(), owner)
		}
		merged := current.GetAnnotations()
		if merged == nil {
			merged = map[string]string{}
		}
		clearLegacySyncAnnotations(merged)
		for k, v := range annotations {
			merged[k] = v
		}
		current.SetAnnotations(merged)
		if err := c.Update(ctx, current); err != nil {
			return deploymentsv1alpha1.RepositorySyncInventoryItem{}, err
		}
	} else {
		if owner := repositorySyncOwner(current.GetAnnotations()); owner != sync.Name {
			return deploymentsv1alpha1.RepositorySyncInventoryItem{}, fmt.Errorf("Deployment %q is owned by RepositorySync %q", doc.GetName(), owner)
		}
		current.Object["spec"] = doc.Object["spec"]
		merged := current.GetAnnotations()
		if merged == nil {
			merged = map[string]string{}
		}
		clearLegacySyncAnnotations(merged)
		for k, v := range annotations {
			merged[k] = v
		}
		current.SetAnnotations(merged)
		if err := c.Update(ctx, current); err != nil {
			return deploymentsv1alpha1.RepositorySyncInventoryItem{}, fmt.Errorf("update Deployment %q: %w", doc.GetName(), err)
		}
	}
	return deploymentsv1alpha1.RepositorySyncInventoryItem{APIVersion: deploymentsGV.String(), Kind: doc.GetKind(), Resource: strings.ToLower(doc.GetKind()) + "s", Name: doc.GetName(), UID: string(current.GetUID())}, nil
}

func prune(ctx context.Context, c client.Client, sync *deploymentsv1alpha1.RepositorySync, desired []deploymentsv1alpha1.RepositorySyncInventoryItem) error {
	wanted := map[string]bool{}
	for _, item := range desired {
		wanted[item.Kind+"/"+item.Name] = true
	}
	for _, old := range sync.Status.Inventory {
		if wanted[old.Kind+"/"+old.Name] {
			continue
		}
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(deploymentsGV.WithKind(old.Kind))
		if err := c.Get(ctx, client.ObjectKey{Name: old.Name}, u); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return err
		}
		if repositorySyncOwner(u.GetAnnotations()) != sync.Name {
			continue
		}
		if old.Kind == "Deployment" && repositorySyncAnnotation(u.GetAnnotations(), deletionPolicyAnnotation, legacyDeletionPolicyAnnotation) == "Delete" {
			if err := c.Delete(ctx, u); err != nil && !apierrors.IsNotFound(err) {
				return err
			}
			continue
		}
		annotations := u.GetAnnotations()
		delete(annotations, ownerAnnotation)
		delete(annotations, pathAnnotation)
		delete(annotations, revisionAnnotation)
		delete(annotations, deletionPolicyAnnotation)
		delete(annotations, legacyOwnerAnnotation)
		delete(annotations, legacyPathAnnotation)
		delete(annotations, legacyRevisionAnnotation)
		delete(annotations, legacyDeletionPolicyAnnotation)
		u.SetAnnotations(annotations)
		if err := c.Update(ctx, u); err != nil {
			return err
		}
	}
	return nil
}

// cleanupInventory finalizes every object this sync last recorded. The
// applied-deletion-policy annotation is controller-authored from the last
// applied Git spec, so a later out-of-band spec edit cannot turn retention
// into deletion during Project teardown.
func cleanupInventory(ctx context.Context, c client.Client, sync *deploymentsv1alpha1.RepositorySync) (bool, error) {
	pending := false
	for _, item := range sync.Status.Inventory {
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(deploymentsGV.WithKind(item.Kind))
		if err := c.Get(ctx, client.ObjectKey{Name: item.Name}, u); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return false, err
		}
		annotations := u.GetAnnotations()
		if repositorySyncOwner(annotations) != sync.Name {
			continue
		}
		if item.Kind == "Deployment" && repositorySyncAnnotation(annotations, deletionPolicyAnnotation, legacyDeletionPolicyAnnotation) == "Delete" {
			if u.GetDeletionTimestamp() != nil {
				pending = true
				continue
			}
			if err := c.Delete(ctx, u); err != nil && !apierrors.IsNotFound(err) {
				return false, err
			}
			pending = true
			continue
		}
		delete(annotations, ownerAnnotation)
		delete(annotations, pathAnnotation)
		delete(annotations, revisionAnnotation)
		delete(annotations, deletionPolicyAnnotation)
		delete(annotations, legacyOwnerAnnotation)
		delete(annotations, legacyPathAnnotation)
		delete(annotations, legacyRevisionAnnotation)
		delete(annotations, legacyDeletionPolicyAnnotation)
		u.SetAnnotations(annotations)
		if err := c.Update(ctx, u); err != nil {
			return false, err
		}
	}
	return pending, nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
func removeString(values []string, remove string) []string {
	out := values[:0]
	for _, value := range values {
		if value != remove {
			out = append(out, value)
		}
	}
	return out
}

func repositorySyncOwner(annotations map[string]string) string {
	return repositorySyncAnnotation(annotations, ownerAnnotation, legacyOwnerAnnotation)
}

func repositorySyncAnnotation(annotations map[string]string, current, legacy string) string {
	if value := annotations[current]; value != "" {
		return value
	}
	return annotations[legacy]
}

func clearLegacySyncAnnotations(annotations map[string]string) {
	delete(annotations, legacyOwnerAnnotation)
	delete(annotations, legacyPathAnnotation)
	delete(annotations, legacyRevisionAnnotation)
	delete(annotations, legacyDeletionPolicyAnnotation)
}

func cleanRoot(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = defaultPath
	}
	cleaned := path.Clean(value)
	if cleaned == "." || cleaned == "/" || strings.HasPrefix(cleaned, "../") || strings.HasPrefix(cleaned, "/") {
		return "", fmt.Errorf("sync path %q must be a repository-relative subdirectory", value)
	}
	return cleaned, nil
}
func withinRoot(value, root string) bool {
	cleaned := path.Clean(value)
	return cleaned == root || strings.HasPrefix(cleaned, root+"/")
}
func updateStatus(ctx context.Context, c client.Client, next *deploymentsv1alpha1.RepositorySync) error {
	var current deploymentsv1alpha1.RepositorySync
	if err := c.Get(ctx, client.ObjectKey{Name: next.Name}, &current); err != nil {
		return err
	}
	if reflect.DeepEqual(current.Status, next.Status) {
		return nil
	}
	current.Status = next.Status
	return c.Status().Update(ctx, &current)
}
