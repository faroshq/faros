/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package repositorysync applies reviewed desired-state YAML from Git to the
// tenant workspace. It deliberately does not interpret target APIs or project
// their runtime readiness: target providers own those responsibilities.
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
	"k8s.io/apimachinery/pkg/types"
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
	ownerAnnotation          = "deployments.faros.sh/repository-sync"
	pathAnnotation           = "deployments.faros.sh/source-path"
	revisionAnnotation       = "deployments.faros.sh/config-revision"
	repositorySyncFinalizer  = "deployments.faros.sh/repository-sync-cleanup"
	fieldOwner               = "deployments-repository-sync"
	legacyOwnerAnnotation    = "code.faros.sh/repository-sync"
	legacyPathAnnotation     = "code.faros.sh/source-path"
	legacyRevisionAnnotation = "code.faros.sh/config-revision"

	conditionSourceReady        = "SourceReady"
	conditionAuthorizationReady = "AuthorizationReady"
	conditionApplied            = "Applied"
	defaultPath                 = ".faros"
	defaultNamespace            = "default"
	defaultInterval             = 30 * time.Second
)

var applyVerbs = []string{"get", "create", "update", "patch", "delete"}

var errPrunePending = errors.New("pruned target objects are still terminating")

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
		return r.finalize(ctx, c, &sync)
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
	result, reconcileErr := r.sync(ctx, c, &sync, string(req.ClusterName))
	next := sync.DeepCopy()
	next.Status.ObservedGeneration = sync.Generation
	next.Status.ObservedRevision = result.revision
	next.Status.TargetRequirements = result.requirements
	if reconcileErr != nil && len(result.inventory) > 0 {
		// Preserve both newly-applied and previously-recorded objects until the
		// whole revision (including pruning) commits. This keeps finalization
		// complete if the RepositorySync is deleted during a partial apply.
		next.Status.Inventory = mergeInventory(sync.Status.Inventory, result.inventory)
	}
	setSourceCondition(next, result.sourceReady, reconcileErr)

	if errors.Is(reconcileErr, errCheckoutPending) {
		next.Status.Phase = deploymentsv1alpha1.RepositorySyncPhaseReconciling
		setCondition(next, conditionAuthorizationReady, metav1.ConditionUnknown, "WaitingForSource", "Target authorization has not been evaluated.")
		setCondition(next, conditionApplied, metav1.ConditionUnknown, "WaitingForSource", "Desired objects have not been evaluated.")
		if err := updateStatus(ctx, c, next); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}
	if errors.Is(reconcileErr, errPrunePending) {
		next.Status.Phase = deploymentsv1alpha1.RepositorySyncPhaseReconciling
		setCondition(next, conditionAuthorizationReady, metav1.ConditionTrue, "Authorized", "All desired target APIs are available and authorized.")
		setCondition(next, conditionApplied, metav1.ConditionFalse, "PrunePending", "Waiting for stale target objects to finish terminating.")
		if err := updateStatus(ctx, c, next); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}
	if reconcileErr != nil {
		var stageErr *syncStageError
		if errors.As(reconcileErr, &stageErr) && stageErr.awaitingAuthorization {
			next.Status.Phase = deploymentsv1alpha1.RepositorySyncPhaseAwaitingAuthorization
			setCondition(next, conditionAuthorizationReady, metav1.ConditionFalse, "PermissionRequired", stageErr.Error())
			setCondition(next, conditionApplied, metav1.ConditionFalse, "AwaitingAuthorization", "Desired objects were not applied; authorize the requested target access and retry.")
		} else {
			next.Status.Phase = deploymentsv1alpha1.RepositorySyncPhaseFailed
			if result.sourceReady {
				setCondition(next, conditionAuthorizationReady, metav1.ConditionFalse, "TargetPreflightFailed", reconcileErr.Error())
				setCondition(next, conditionApplied, metav1.ConditionFalse, "ApplyFailed", reconcileErr.Error())
			} else {
				setCondition(next, conditionAuthorizationReady, metav1.ConditionUnknown, "WaitingForSource", "Target authorization has not been evaluated.")
				setCondition(next, conditionApplied, metav1.ConditionUnknown, "WaitingForSource", "Desired objects have not been evaluated.")
			}
		}
		if err := updateStatus(ctx, c, next); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: interval}, nil
	}

	next.Status.Phase = deploymentsv1alpha1.RepositorySyncPhaseSynced
	next.Status.AppliedRevision = result.revision
	next.Status.Inventory = result.inventory
	setCondition(next, conditionAuthorizationReady, metav1.ConditionTrue, "Authorized", "All desired target APIs are available and authorized.")
	setCondition(next, conditionApplied, metav1.ConditionTrue, "Synced", "The reviewed repository revision was applied. Target runtime readiness is reported by the target resources.")
	if err := updateStatus(ctx, c, next); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: interval}, nil
}

func (r *Reconciler) finalize(ctx context.Context, c client.Client, sync *deploymentsv1alpha1.RepositorySync) (ctrl.Result, error) {
	if !containsString(sync.Finalizers, repositorySyncFinalizer) {
		return ctrl.Result{}, nil
	}
	pending, requirements, err := cleanupInventory(ctx, c, sync)
	if err != nil {
		next := sync.DeepCopy()
		next.Status.TargetRequirements = requirements
		var stageErr *syncStageError
		if errors.As(err, &stageErr) && stageErr.awaitingAuthorization {
			next.Status.Phase = deploymentsv1alpha1.RepositorySyncPhaseAwaitingAuthorization
			setCondition(next, conditionAuthorizationReady, metav1.ConditionFalse, "PermissionRequired", stageErr.Error())
			setCondition(next, conditionApplied, metav1.ConditionFalse, "CleanupAwaitingAuthorization", "Cleanup is waiting for target access.")
			if statusErr := updateStatus(ctx, c, next); statusErr != nil {
				return ctrl.Result{}, statusErr
			}
			return ctrl.Result{RequeueAfter: defaultInterval}, nil
		}
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
	if err := c.Update(ctx, sync); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

type syncResult struct {
	revision     string
	sourceReady  bool
	inventory    []deploymentsv1alpha1.RepositorySyncInventoryItem
	requirements []deploymentsv1alpha1.RepositorySyncTargetRequirement
}

type syncStageError struct {
	operation             string
	cause                 error
	awaitingAuthorization bool
}

func (e *syncStageError) Error() string { return fmt.Sprintf("%s: %v", e.operation, e.cause) }
func (e *syncStageError) Unwrap() error { return e.cause }

func (r *Reconciler) sync(ctx context.Context, c client.Client, sync *deploymentsv1alpha1.RepositorySync, scope string) (syncResult, error) {
	var result syncResult
	if r.Source == nil {
		return result, fmt.Errorf("Code repository checkout reader is unavailable")
	}
	root, err := cleanRoot(sync.Spec.Path)
	if err != nil {
		return result, err
	}
	checkout, err := r.Source.Checkout(ctx, c, RepositorySource{SyncName: sync.Name, RepositoryRef: sync.Spec.RepositoryRef, Ref: sync.Spec.Ref, Path: root, Scope: scope})
	if err != nil {
		return result, err
	}
	result.revision = checkout.CommitSHA
	if strings.TrimSpace(checkout.CommitSHA) == "" {
		return result, fmt.Errorf("git backend resolved the repository without a commit SHA")
	}
	for _, skipped := range checkout.Skipped {
		if strings.Contains(skipped, "tree truncated") || withinRoot(skipped, root) {
			return result, fmt.Errorf("source tree is incomplete: %q was skipped", skipped)
		}
	}
	docs, err := parseDocuments(checkout.Files, root)
	if err != nil {
		return result, err
	}
	result.sourceReady = true
	resolved, requirements, err := preflightDocuments(ctx, c, sync, checkout.CommitSHA, docs)
	result.requirements = requirements
	if err != nil {
		return result, err
	}
	result.inventory, err = applyDocuments(ctx, c, sync, checkout.CommitSHA, resolved)
	if err != nil {
		if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
			requirements = markRequirementForError(requirements, err, deploymentsv1alpha1.RepositorySyncTargetAwaitingAuthorization)
			result.requirements = requirements
			return result, &syncStageError{operation: "apply target objects", cause: err, awaitingAuthorization: true}
		}
		return result, &syncStageError{operation: "apply target objects", cause: err}
	}
	if sync.Spec.Prune {
		if err := prune(ctx, c, sync, result.inventory); err != nil {
			if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
				requirements = markRequirementForError(requirements, err, deploymentsv1alpha1.RepositorySyncTargetAwaitingAuthorization)
				result.requirements = requirements
				return result, &syncStageError{operation: "prune target objects", cause: err, awaitingAuthorization: true}
			}
			return result, &syncStageError{operation: "prune target objects", cause: err}
		}
	}
	sortInventory(result.inventory)
	return result, nil
}

type sourcedDocument struct {
	*unstructured.Unstructured
	sourcePath string
}

type resolvedDocument struct {
	sourcedDocument
	resource string
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
			if strings.TrimSpace(u.GetAPIVersion()) == "" || strings.TrimSpace(u.GetKind()) == "" {
				return nil, fmt.Errorf("%s document %d: apiVersion and kind are required", file.Path, index)
			}
			if _, err := schema.ParseGroupVersion(u.GetAPIVersion()); err != nil {
				return nil, fmt.Errorf("%s document %d: invalid apiVersion: %w", file.Path, index, err)
			}
			if strings.TrimSpace(u.GetName()) == "" {
				return nil, fmt.Errorf("%s document %d: metadata.name is required", file.Path, index)
			}
			if problems := utilvalidation.IsDNS1123Subdomain(u.GetName()); len(problems) > 0 {
				return nil, fmt.Errorf("%s document %d: metadata.name is invalid: %s", file.Path, index, strings.Join(problems, ", "))
			}
			if err := validateGitMetadata(u); err != nil {
				return nil, fmt.Errorf("%s document %d: %w", file.Path, index, err)
			}
			u.Object["metadata"] = desiredMetadata(u)
			key := strings.Join([]string{u.GetAPIVersion(), u.GetKind(), u.GetNamespace(), u.GetName()}, "/")
			if prior, ok := seen[key]; ok {
				return nil, fmt.Errorf("duplicate %s in %s and %s", key, prior, file.Path)
			}
			seen[key] = file.Path
			out = append(out, sourcedDocument{Unstructured: u, sourcePath: file.Path})
		}
	}
	return out, nil
}

func desiredMetadata(u *unstructured.Unstructured) map[string]any {
	metadata := map[string]any{"name": u.GetName()}
	if namespace := u.GetNamespace(); namespace != "" {
		metadata["namespace"] = namespace
	}
	if labels := u.GetLabels(); len(labels) > 0 {
		metadata["labels"] = stringMapAny(labels)
	}
	if annotations := u.GetAnnotations(); len(annotations) > 0 {
		metadata["annotations"] = stringMapAny(annotations)
	}
	return metadata
}

func stringMapAny(values map[string]string) map[string]any {
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func validateGitMetadata(u *unstructured.Unstructured) error {
	if _, found := u.Object["status"]; found {
		return fmt.Errorf("status is controller-owned and must not be supplied")
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
	for key := range metadata {
		switch key {
		case "name", "namespace", "labels", "annotations":
		default:
			return fmt.Errorf("metadata.%s is not authored from Git", key)
		}
	}
	if _, found, err := unstructured.NestedStringMap(u.Object, "metadata", "labels"); err != nil {
		return fmt.Errorf("metadata.labels must contain string values: %w", err)
	} else if found && u.GetLabels() == nil {
		return fmt.Errorf("metadata.labels must be an object")
	}
	if _, found, err := unstructured.NestedStringMap(u.Object, "metadata", "annotations"); err != nil {
		return fmt.Errorf("metadata.annotations must contain string values: %w", err)
	} else if found && u.GetAnnotations() == nil {
		return fmt.Errorf("metadata.annotations must be an object")
	}
	return nil
}

func preflightDocuments(ctx context.Context, c client.Client, sync *deploymentsv1alpha1.RepositorySync, revision string, docs []sourcedDocument) ([]resolvedDocument, []deploymentsv1alpha1.RepositorySyncTargetRequirement, error) {
	resolved := make([]resolvedDocument, 0, len(docs))
	requirementsByKey := map[string]*deploymentsv1alpha1.RepositorySyncTargetRequirement{}
	resolvedIdentities := map[string]string{}
	var firstErr error
	awaiting := false
	hardFailure := false
	for _, doc := range docs {
		gv, _ := schema.ParseGroupVersion(doc.GetAPIVersion())
		gvk := gv.WithKind(doc.GetKind())
		plural, _ := apiMeta.UnsafeGuessKindToResource(gvk)
		requirement := targetRequirement(gvk, plural.Resource, doc.GetNamespace())
		mapping, err := c.RESTMapper().RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			if apiMeta.IsNoMatchError(err) && requirement.Claim != nil {
				requirement.State = deploymentsv1alpha1.RepositorySyncTargetAwaitingAuthorization
				requirement.Message = "Target API is not available to Deployments in this workspace; authorize the optional provider claim."
				awaiting = true
			} else {
				requirement.State = deploymentsv1alpha1.RepositorySyncTargetUnavailable
				requirement.Message = fmt.Sprintf("Target API is unavailable in this workspace: %v", err)
				hardFailure = true
			}
			upsertRequirement(requirementsByKey, requirement)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		doc.SetGroupVersionKind(gvk)
		namespace := doc.GetNamespace()
		if mapping.Scope.Name() == apiMeta.RESTScopeNameNamespace {
			if namespace == "" {
				namespace = defaultNamespace
				doc.SetNamespace(namespace)
			}
		} else if namespace != "" {
			err := fmt.Errorf("%s %s is cluster-scoped and cannot set metadata.namespace", doc.GetAPIVersion(), doc.GetKind())
			requirement.Resource = mapping.Resource.Resource
			requirement.Namespace = namespace
			requirement.State = deploymentsv1alpha1.RepositorySyncTargetUnavailable
			requirement.Message = err.Error()
			hardFailure = true
			upsertRequirement(requirementsByKey, requirement)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		requirement.Resource = mapping.Resource.Resource
		requirement.Namespace = namespace
		requirement.Claim = targetClaim(mapping.Resource)
		// Different served versions of one API resource address the same stored
		// object. Reject that ambiguity before dry-run so a single revision can
		// never apply two documents to the same group/resource/namespace/name.
		identity := strings.Join([]string{mapping.Resource.Group, mapping.Resource.Resource, namespace, doc.GetName()}, "/")
		if prior, found := resolvedIdentities[identity]; found {
			err := fmt.Errorf("duplicate target %s in %s and %s", identity, prior, doc.sourcePath)
			requirement.State = deploymentsv1alpha1.RepositorySyncTargetConflict
			requirement.Message = err.Error()
			hardFailure = true
			upsertRequirement(requirementsByKey, requirement)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		resolvedIdentities[identity] = doc.sourcePath
		prepareDesired(doc.Unstructured, sync.Name, doc.sourcePath, revision)

		current := &unstructured.Unstructured{}
		current.SetGroupVersionKind(gvk)
		key := client.ObjectKey{Namespace: namespace, Name: doc.GetName()}
		err = c.Get(ctx, key, current)
		switch {
		case apierrors.IsForbidden(err), apierrors.IsUnauthorized(err):
			requirement.State = deploymentsv1alpha1.RepositorySyncTargetAwaitingAuthorization
			requirement.Message = err.Error()
			awaiting = true
			if firstErr == nil {
				firstErr = err
			}
		case apierrors.IsNotFound(err):
			if err := c.Create(ctx, doc.DeepCopy(), client.FieldOwner(fieldOwner), client.DryRunAll); err != nil {
				classifyPreflightError(&requirement, err, &awaiting, &hardFailure)
				if firstErr == nil {
					firstErr = err
				}
			} else {
				requirement.State = deploymentsv1alpha1.RepositorySyncTargetAuthorized
			}
		case err != nil:
			requirement.State = deploymentsv1alpha1.RepositorySyncTargetUnavailable
			requirement.Message = err.Error()
			hardFailure = true
			if firstErr == nil {
				firstErr = err
			}
		default:
			if repositorySyncOwner(current.GetAnnotations()) != sync.Name {
				err := fmt.Errorf("%s %q already exists and is not owned by RepositorySync %q", doc.GetKind(), doc.GetName(), sync.Name)
				requirement.State = deploymentsv1alpha1.RepositorySyncTargetConflict
				requirement.Message = err.Error()
				hardFailure = true
				if firstErr == nil {
					firstErr = err
				}
			} else if err := c.Patch(ctx, doc.DeepCopy(), client.Apply, client.FieldOwner(fieldOwner), client.ForceOwnership, client.DryRunAll); err != nil {
				classifyPreflightError(&requirement, err, &awaiting, &hardFailure)
				if firstErr == nil {
					firstErr = err
				}
			} else {
				requirement.State = deploymentsv1alpha1.RepositorySyncTargetAuthorized
			}
		}
		upsertRequirement(requirementsByKey, requirement)
		resolved = append(resolved, resolvedDocument{sourcedDocument: doc, resource: mapping.Resource.Resource})
	}
	requirements := sortedRequirements(requirementsByKey)
	if firstErr != nil {
		return nil, requirements, &syncStageError{operation: "preflight target objects", cause: firstErr, awaitingAuthorization: awaiting && !hardFailure}
	}
	return resolved, requirements, nil
}

func classifyPreflightError(requirement *deploymentsv1alpha1.RepositorySyncTargetRequirement, err error, awaiting, hardFailure *bool) {
	requirement.Message = err.Error()
	if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
		requirement.State = deploymentsv1alpha1.RepositorySyncTargetAwaitingAuthorization
		*awaiting = true
		return
	}
	requirement.State = deploymentsv1alpha1.RepositorySyncTargetUnavailable
	*hardFailure = true
}

func prepareDesired(u *unstructured.Unstructured, syncName, sourcePath, revision string) {
	annotations := u.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[ownerAnnotation] = syncName
	annotations[pathAnnotation] = sourcePath
	annotations[revisionAnnotation] = revision
	delete(annotations, legacyOwnerAnnotation)
	delete(annotations, legacyPathAnnotation)
	delete(annotations, legacyRevisionAnnotation)
	u.SetAnnotations(annotations)
}

func applyDocuments(ctx context.Context, c client.Client, sync *deploymentsv1alpha1.RepositorySync, revision string, docs []resolvedDocument) ([]deploymentsv1alpha1.RepositorySyncInventoryItem, error) {
	inventory := make([]deploymentsv1alpha1.RepositorySyncInventoryItem, 0, len(docs))
	for _, doc := range docs {
		item, err := applyDocument(ctx, c, sync, revision, doc)
		if err != nil {
			return inventory, &targetOperationError{apiVersion: doc.GetAPIVersion(), kind: doc.GetKind(), resource: doc.resource, namespace: doc.GetNamespace(), cause: err}
		}
		inventory = append(inventory, item)
	}
	return inventory, nil
}

func applyDocument(ctx context.Context, c client.Client, sync *deploymentsv1alpha1.RepositorySync, revision string, doc resolvedDocument) (deploymentsv1alpha1.RepositorySyncInventoryItem, error) {
	prepareDesired(doc.Unstructured, sync.Name, doc.sourcePath, revision)
	key := client.ObjectKey{Namespace: doc.GetNamespace(), Name: doc.GetName()}
	current := &unstructured.Unstructured{}
	current.SetGroupVersionKind(doc.GroupVersionKind())
	err := c.Get(ctx, key, current)
	if apierrors.IsNotFound(err) {
		if err := c.Create(ctx, doc.Unstructured, client.FieldOwner(fieldOwner)); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				return deploymentsv1alpha1.RepositorySyncInventoryItem{}, fmt.Errorf("create %s %q: %w", doc.GetKind(), doc.GetName(), err)
			}
			if err := c.Get(ctx, key, current); err != nil {
				return deploymentsv1alpha1.RepositorySyncInventoryItem{}, err
			}
			if repositorySyncOwner(current.GetAnnotations()) != sync.Name {
				return deploymentsv1alpha1.RepositorySyncInventoryItem{}, fmt.Errorf("%s %q appeared during apply and is not owned by RepositorySync %q", doc.GetKind(), doc.GetName(), sync.Name)
			}
			if err := c.Patch(ctx, doc.Unstructured, client.Apply, client.FieldOwner(fieldOwner), client.ForceOwnership); err != nil {
				return deploymentsv1alpha1.RepositorySyncInventoryItem{}, err
			}
		} else {
			current = doc.Unstructured
		}
	} else if err != nil {
		return deploymentsv1alpha1.RepositorySyncInventoryItem{}, err
	} else {
		if repositorySyncOwner(current.GetAnnotations()) != sync.Name {
			return deploymentsv1alpha1.RepositorySyncInventoryItem{}, fmt.Errorf("%s %q is not owned by RepositorySync %q", doc.GetKind(), doc.GetName(), sync.Name)
		}
		if err := c.Patch(ctx, doc.Unstructured, client.Apply, client.FieldOwner(fieldOwner), client.ForceOwnership); err != nil {
			return deploymentsv1alpha1.RepositorySyncInventoryItem{}, fmt.Errorf("apply %s %q: %w", doc.GetKind(), doc.GetName(), err)
		}
	}
	observed := &unstructured.Unstructured{}
	observed.SetGroupVersionKind(doc.GroupVersionKind())
	if err := c.Get(ctx, key, observed); err != nil {
		return deploymentsv1alpha1.RepositorySyncInventoryItem{}, err
	}
	return deploymentsv1alpha1.RepositorySyncInventoryItem{
		APIVersion: doc.GetAPIVersion(), Kind: doc.GetKind(), Resource: doc.resource,
		Namespace: doc.GetNamespace(), Name: doc.GetName(), UID: string(observed.GetUID()), SourcePath: doc.sourcePath,
	}, nil
}

func prune(ctx context.Context, c client.Client, sync *deploymentsv1alpha1.RepositorySync, desired []deploymentsv1alpha1.RepositorySyncInventoryItem) error {
	wanted := map[string]bool{}
	for _, item := range desired {
		wanted[inventoryKey(item)] = true
	}
	for _, old := range sync.Status.Inventory {
		if wanted[inventoryKey(old)] {
			continue
		}
		pending, err := deleteInventoryItem(ctx, c, sync.Name, old)
		if err != nil {
			return &targetOperationError{apiVersion: old.APIVersion, kind: old.Kind, resource: old.Resource, namespace: old.Namespace, cause: err}
		}
		if pending {
			return fmt.Errorf("%w: %s %q", errPrunePending, old.Kind, old.Name)
		}
	}
	return nil
}

// cleanupInventory deletes owned targets when prune is enabled. When prune is
// disabled it removes only this sync's tracking annotations and leaves the
// target intact. UID preconditions prevent a stale inventory entry from
// deleting a replacement object with the same name.
func cleanupInventory(ctx context.Context, c client.Client, sync *deploymentsv1alpha1.RepositorySync) (bool, []deploymentsv1alpha1.RepositorySyncTargetRequirement, error) {
	pending := false
	requirements := map[string]*deploymentsv1alpha1.RepositorySyncTargetRequirement{}
	for _, item := range sync.Status.Inventory {
		gv, err := schema.ParseGroupVersion(item.APIVersion)
		if err != nil {
			continue
		}
		gvk := gv.WithKind(item.Kind)
		requirement := targetRequirement(gvk, item.Resource, item.Namespace)
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(gvk)
		if err := c.Get(ctx, client.ObjectKey{Namespace: item.Namespace, Name: item.Name}, u); err != nil {
			if apierrors.IsNotFound(err) || apiMeta.IsNoMatchError(err) {
				continue
			}
			if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
				requirement.State = deploymentsv1alpha1.RepositorySyncTargetAwaitingAuthorization
				requirement.Message = err.Error()
				upsertRequirement(requirements, requirement)
				return false, sortedRequirements(requirements), &syncStageError{operation: "clean up target objects", cause: err, awaitingAuthorization: true}
			}
			return false, sortedRequirements(requirements), err
		}
		if repositorySyncOwner(u.GetAnnotations()) != sync.Name || (item.UID != "" && string(u.GetUID()) != item.UID) {
			continue
		}
		requirement.State = deploymentsv1alpha1.RepositorySyncTargetAuthorized
		upsertRequirement(requirements, requirement)
		if sync.Spec.Prune {
			if u.GetDeletionTimestamp() != nil {
				pending = true
				continue
			}
			uid := types.UID(item.UID)
			var options []client.DeleteOption
			if uid != "" {
				options = append(options, client.Preconditions{UID: &uid})
			}
			if err := c.Delete(ctx, u, options...); err != nil && !apierrors.IsNotFound(err) {
				return false, sortedRequirements(requirements), err
			}
			pending = true
			continue
		}
		before := u.DeepCopy()
		annotations := u.GetAnnotations()
		clearSyncAnnotations(annotations)
		u.SetAnnotations(annotations)
		if err := c.Patch(ctx, u, client.MergeFrom(before)); err != nil {
			return false, sortedRequirements(requirements), err
		}
	}
	return pending, sortedRequirements(requirements), nil
}

func deleteInventoryItem(ctx context.Context, c client.Client, syncName string, item deploymentsv1alpha1.RepositorySyncInventoryItem) (bool, error) {
	gv, err := schema.ParseGroupVersion(item.APIVersion)
	if err != nil {
		return false, nil
	}
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gv.WithKind(item.Kind))
	if err := c.Get(ctx, client.ObjectKey{Namespace: item.Namespace, Name: item.Name}, u); err != nil {
		if apierrors.IsNotFound(err) || apiMeta.IsNoMatchError(err) {
			return false, nil
		}
		return false, err
	}
	if repositorySyncOwner(u.GetAnnotations()) != syncName || (item.UID != "" && string(u.GetUID()) != item.UID) {
		return false, nil
	}
	if u.GetDeletionTimestamp() != nil {
		return true, nil
	}
	uid := types.UID(item.UID)
	var options []client.DeleteOption
	if uid != "" {
		options = append(options, client.Preconditions{UID: &uid})
	}
	if err := c.Delete(ctx, u, options...); err != nil && !apierrors.IsNotFound(err) {
		return false, err
	}
	return true, nil
}

func targetRequirement(gvk schema.GroupVersionKind, resource, namespace string) deploymentsv1alpha1.RepositorySyncTargetRequirement {
	return deploymentsv1alpha1.RepositorySyncTargetRequirement{
		APIVersion: gvk.GroupVersion().String(), Kind: gvk.Kind, Resource: resource, Namespace: namespace,
		State: deploymentsv1alpha1.RepositorySyncTargetUnavailable, Claim: targetClaim(schema.GroupVersionResource{Group: gvk.Group, Version: gvk.Version, Resource: resource}),
	}
}

func targetClaim(gvr schema.GroupVersionResource) *deploymentsv1alpha1.RepositorySyncTargetClaim {
	return &deploymentsv1alpha1.RepositorySyncTargetClaim{
		Group: gvr.Group, Resource: gvr.Resource, Verbs: append([]string(nil), applyVerbs...),
	}
}

func upsertRequirement(requirements map[string]*deploymentsv1alpha1.RepositorySyncTargetRequirement, requirement deploymentsv1alpha1.RepositorySyncTargetRequirement) {
	key := strings.Join([]string{requirement.APIVersion, requirement.Kind, requirement.Resource, requirement.Namespace}, "/")
	current, found := requirements[key]
	if !found || requirementPriority(requirement.State) > requirementPriority(current.State) {
		copy := requirement
		requirements[key] = &copy
	}
}

func requirementPriority(state deploymentsv1alpha1.RepositorySyncTargetRequirementState) int {
	switch state {
	case deploymentsv1alpha1.RepositorySyncTargetConflict:
		return 4
	case deploymentsv1alpha1.RepositorySyncTargetUnavailable:
		return 3
	case deploymentsv1alpha1.RepositorySyncTargetAwaitingAuthorization:
		return 2
	default:
		return 1
	}
}

func sortedRequirements(values map[string]*deploymentsv1alpha1.RepositorySyncTargetRequirement) []deploymentsv1alpha1.RepositorySyncTargetRequirement {
	out := make([]deploymentsv1alpha1.RepositorySyncTargetRequirement, 0, len(values))
	for _, value := range values {
		out = append(out, *value)
	}
	sort.Slice(out, func(i, j int) bool {
		left := strings.Join([]string{out[i].APIVersion, out[i].Kind, out[i].Namespace, out[i].Resource}, "/")
		right := strings.Join([]string{out[j].APIVersion, out[j].Kind, out[j].Namespace, out[j].Resource}, "/")
		return left < right
	})
	return out
}

type targetOperationError struct {
	apiVersion string
	kind       string
	resource   string
	namespace  string
	cause      error
}

func (e *targetOperationError) Error() string { return e.cause.Error() }
func (e *targetOperationError) Unwrap() error { return e.cause }

func markRequirementForError(requirements []deploymentsv1alpha1.RepositorySyncTargetRequirement, err error, state deploymentsv1alpha1.RepositorySyncTargetRequirementState) []deploymentsv1alpha1.RepositorySyncTargetRequirement {
	var targetErr *targetOperationError
	if !errors.As(err, &targetErr) {
		return requirements
	}
	for i := range requirements {
		if requirements[i].APIVersion == targetErr.apiVersion && requirements[i].Kind == targetErr.kind && requirements[i].Resource == targetErr.resource && requirements[i].Namespace == targetErr.namespace {
			requirements[i].State = state
			requirements[i].Message = targetErr.Error()
			return requirements
		}
	}
	gv, parseErr := schema.ParseGroupVersion(targetErr.apiVersion)
	if parseErr != nil {
		return requirements
	}
	requirement := targetRequirement(gv.WithKind(targetErr.kind), targetErr.resource, targetErr.namespace)
	requirement.State = state
	requirement.Message = targetErr.Error()
	requirements = append(requirements, requirement)
	sort.Slice(requirements, func(i, j int) bool {
		left := strings.Join([]string{requirements[i].APIVersion, requirements[i].Kind, requirements[i].Namespace, requirements[i].Resource}, "/")
		right := strings.Join([]string{requirements[j].APIVersion, requirements[j].Kind, requirements[j].Namespace, requirements[j].Resource}, "/")
		return left < right
	})
	return requirements
}

func inventoryKey(item deploymentsv1alpha1.RepositorySyncInventoryItem) string {
	return strings.Join([]string{item.APIVersion, item.Kind, item.Resource, item.Namespace, item.Name}, "/")
}

func sortInventory(inventory []deploymentsv1alpha1.RepositorySyncInventoryItem) {
	sort.Slice(inventory, func(i, j int) bool { return inventoryKey(inventory[i]) < inventoryKey(inventory[j]) })
}

func mergeInventory(left, right []deploymentsv1alpha1.RepositorySyncInventoryItem) []deploymentsv1alpha1.RepositorySyncInventoryItem {
	byKey := make(map[string]deploymentsv1alpha1.RepositorySyncInventoryItem, len(left)+len(right))
	for _, item := range left {
		byKey[inventoryKey(item)] = item
	}
	for _, item := range right {
		byKey[inventoryKey(item)] = item
	}
	out := make([]deploymentsv1alpha1.RepositorySyncInventoryItem, 0, len(byKey))
	for _, item := range byKey {
		out = append(out, item)
	}
	sortInventory(out)
	return out
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
	if value := annotations[ownerAnnotation]; value != "" {
		return value
	}
	return annotations[legacyOwnerAnnotation]
}

func clearSyncAnnotations(annotations map[string]string) {
	delete(annotations, ownerAnnotation)
	delete(annotations, pathAnnotation)
	delete(annotations, revisionAnnotation)
	delete(annotations, legacyOwnerAnnotation)
	delete(annotations, legacyPathAnnotation)
	delete(annotations, legacyRevisionAnnotation)
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

func setSourceCondition(sync *deploymentsv1alpha1.RepositorySync, ready bool, reconcileErr error) {
	if ready {
		setCondition(sync, conditionSourceReady, metav1.ConditionTrue, "Resolved", "The repository revision was fetched and parsed.")
		return
	}
	reason := "SourceError"
	message := "The repository source could not be resolved."
	if errors.Is(reconcileErr, errCheckoutPending) {
		reason = "CheckoutPending"
		message = "Waiting for Code to produce the repository checkout."
	} else if reconcileErr != nil {
		message = reconcileErr.Error()
	}
	setCondition(sync, conditionSourceReady, metav1.ConditionFalse, reason, message)
}

func setCondition(sync *deploymentsv1alpha1.RepositorySync, conditionType string, status metav1.ConditionStatus, reason, message string) {
	apiMeta.SetStatusCondition(&sync.Status.Conditions, metav1.Condition{
		Type: conditionType, Status: status, Reason: reason, Message: message, ObservedGeneration: sync.Generation,
	})
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
