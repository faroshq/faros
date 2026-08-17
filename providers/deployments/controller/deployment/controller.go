// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

// Package deployment reconciles immutable Releases into Infrastructure
// template instances. It is intentionally the only package that knows the
// kro-direct compatibility contract.
package deployment

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apiMeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	deploymentsv1alpha1 "github.com/faroshq/provider-deployments/apis/v1alpha1"
)

const (
	Finalizer          = "deployments.faros.sh/backend"
	ConditionReady     = "Ready"
	ConditionApplied   = "Applied"
	lastAppliedSpecKey = "deployments.faros.sh/last-applied-spec"
	pollInterval       = 15 * time.Second
	deletePollInterval = 2 * time.Second
)

var templateGVK = schema.GroupVersionKind{Group: "infrastructure.faros.sh", Version: "v1alpha1", Kind: "Template"}

type Reconciler struct{ Manager mcmanager.Manager }

func (r *Reconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.Manager = mgr
	return mcbuilder.ControllerManagedBy(mgr).Named("deployments-deployment").For(&deploymentsv1alpha1.Deployment{}).Complete(r)
}

func (r *Reconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	cluster, err := r.Manager.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("cluster %q: %w", req.ClusterName, err)
	}
	return ReconcileClient(ctx, cluster.GetClient(), req.NamespacedName)
}

// ReconcileClient contains the single-workspace operation and is exported as a
// narrow test seam; production obtains the client from the multicluster manager.
func ReconcileClient(ctx context.Context, c client.Client, key types.NamespacedName) (ctrl.Result, error) {
	var d deploymentsv1alpha1.Deployment
	if err := c.Get(ctx, key, &d); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if !d.DeletionTimestamp.IsZero() {
		return finalize(ctx, c, &d)
	}
	if !controllerutil.ContainsFinalizer(&d, Finalizer) {
		controllerutil.AddFinalizer(&d, Finalizer)
		if err := c.Update(ctx, &d); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}
	if d.Spec.ClassName != "" && d.Spec.ClassName != deploymentsv1alpha1.DeploymentClassKRODirect {
		return invalid(ctx, c, &d, "UnsupportedClass", fmt.Sprintf("deployment class %q is not supported", d.Spec.ClassName))
	}

	var release deploymentsv1alpha1.Release
	if err := c.Get(ctx, client.ObjectKey{Name: strings.TrimSpace(d.Spec.ReleaseRef)}, &release); err != nil {
		if apierrors.IsNotFound(err) {
			return dependencyPending(ctx, c, &d, "ReleaseNotFound", fmt.Sprintf("waiting for Release %q", d.Spec.ReleaseRef))
		}
		return ctrl.Result{}, err
	}
	template, err := builtinTemplateContract(release.Spec.BlueprintRef.Name)
	if err != nil {
		return invalid(ctx, c, &d, "UnsupportedBlueprint", err.Error())
	}
	want, ref, err := DesiredInstance(&d, &release, template)
	if err != nil {
		return invalid(ctx, c, &d, "InvalidRelease", err.Error())
	}
	// Persist the deletion coordinates before creating the backend. If a
	// subsequent create/status write is interrupted, the finalizer can still
	// remove the instance without re-reading a Release or Template.
	if !sameBackendTarget(d.Status.BackendRef, ref) {
		d.Status.BackendRef = ref
		d.Status.ObservedGeneration = d.Generation
		if err := c.Status().Update(ctx, &d); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}
	got, err := ensureInstance(ctx, c, want)
	if err != nil {
		return ctrl.Result{}, err
	}
	ref.UID = string(got.GetUID())
	if err := projectStatus(ctx, c, &d, &release, got, ref); err != nil {
		return ctrl.Result{}, err
	}
	if backendPhase(got) == "Ready" {
		return ctrl.Result{RequeueAfter: 4 * pollInterval}, nil
	}
	return ctrl.Result{RequeueAfter: pollInterval}, nil
}

// builtinTemplateContract is the admitted deployment contract for the initial
// POC class. Infrastructure Templates use virtual storage and therefore cannot
// be selected by another APIExport through a permission claim. Keep the
// production mapping deterministic here until Release carries an immutable
// snapshot of the resolved Template contract.
func builtinTemplateContract(name string) (*unstructured.Unstructured, error) {
	if strings.TrimSpace(name) != "application" {
		return nil, fmt.Errorf("blueprint %q is not supported by class %q", name, deploymentsv1alpha1.DeploymentClassKRODirect)
	}
	template := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": templateGVK.GroupVersion().String(),
		"kind":       templateGVK.Kind,
		"metadata":   map[string]any{"name": "application"},
		"spec": map[string]any{
			"instanceCRD": map[string]any{
				"group": "infrastructure.faros.sh", "version": "v1alpha1",
				"resource": "applications", "kind": "Application",
			},
			"development": map[string]any{"components": map[string]any{
				"web": map[string]any{"imageInput": "webImage"},
				"api": map[string]any{"imageInput": "apiImage"},
			}},
		},
	}}
	template.SetGroupVersionKind(templateGVK)
	return template, nil
}

func sameBackendTarget(a, b *deploymentsv1alpha1.BackendReference) bool {
	return a != nil && b != nil && a.APIVersion == b.APIVersion && a.Kind == b.Kind && a.Resource == b.Resource && a.Name == b.Name
}

// DesiredInstance validates the Template contract and constructs the complete
// platform-owned desired overlay. User configuration is decoded first, then
// reserved values and exact immutable artifact images take precedence.
func DesiredInstance(d *deploymentsv1alpha1.Deployment, release *deploymentsv1alpha1.Release, template *unstructured.Unstructured) (*unstructured.Unstructured, *deploymentsv1alpha1.BackendReference, error) {
	if d == nil || release == nil || template == nil {
		return nil, nil, fmt.Errorf("deployment, release, and template are required")
	}
	group, _, _ := unstructured.NestedString(template.Object, "spec", "instanceCRD", "group")
	version, _, _ := unstructured.NestedString(template.Object, "spec", "instanceCRD", "version")
	resource, _, _ := unstructured.NestedString(template.Object, "spec", "instanceCRD", "resource")
	kind, _, _ := unstructured.NestedString(template.Object, "spec", "instanceCRD", "kind")
	if group != "infrastructure.faros.sh" || version == "" || resource == "" || kind == "" {
		return nil, nil, fmt.Errorf("Template %q has an invalid spec.instanceCRD", template.GetName())
	}
	configuration := map[string]any{}
	if d.Spec.Configuration != nil && len(d.Spec.Configuration.Raw) > 0 {
		if err := json.Unmarshal(d.Spec.Configuration.Raw, &configuration); err != nil {
			return nil, nil, fmt.Errorf("configuration must be a JSON object: %w", err)
		}
		if configuration == nil {
			configuration = map[string]any{}
		}
	}
	mode := effectiveMode(d.Spec.Mode)
	if mode != deploymentsv1alpha1.DeploymentModeDevelopment && mode != deploymentsv1alpha1.DeploymentModeProduction {
		return nil, nil, fmt.Errorf("deployment mode %q is not supported", d.Spec.Mode)
	}
	if mode == deploymentsv1alpha1.DeploymentModeProduction {
		components, _, _ := unstructured.NestedMap(template.Object, "spec", "development", "components")
		artifacts := map[string]string{}
		for _, artifact := range release.Spec.Artifacts {
			name := strings.TrimSpace(artifact.Name)
			if name == "" || strings.TrimSpace(artifact.Image) == "" {
				return nil, nil, fmt.Errorf("release artifact name and image are required")
			}
			if _, exists := artifacts[name]; exists {
				return nil, nil, fmt.Errorf("release contains duplicate artifact %q", name)
			}
			artifacts[name] = strings.TrimSpace(artifact.Image)
		}
		for componentName, raw := range components {
			component, ok := raw.(map[string]any)
			if !ok {
				return nil, nil, fmt.Errorf("Template component %q is invalid", componentName)
			}
			imageInput, _ := component["imageInput"].(string)
			imageInput = strings.TrimSpace(imageInput)
			if imageInput == "" {
				continue
			}
			image, ok := artifacts[componentName]
			if !ok {
				return nil, nil, fmt.Errorf("release has no artifact for launchable component %q", componentName)
			}
			configuration[imageInput] = image
		}
	}
	configuration["name"] = d.Name
	configuration["farosMode"] = string(mode)
	configuration["farosRedeployRevision"] = d.Spec.RolloutID

	gvk := schema.GroupVersionKind{Group: group, Version: version, Kind: kind}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": gvk.GroupVersion().String(), "kind": kind,
		"metadata": map[string]any{"name": d.Name, "labels": map[string]any{"deployments.faros.sh/deployment": d.Name}},
		"spec":     configuration,
	}}
	deletionPolicy := effectiveDeletionPolicy(d.Spec.DeletionPolicy)
	if deletionPolicy != deploymentsv1alpha1.DeploymentDeletionPolicyRetain && deletionPolicy != deploymentsv1alpha1.DeploymentDeletionPolicyDelete {
		return nil, nil, fmt.Errorf("deployment deletion policy %q is not supported", d.Spec.DeletionPolicy)
	}
	if deletionPolicy == deploymentsv1alpha1.DeploymentDeletionPolicyDelete {
		obj.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: deploymentsv1alpha1.GroupVersion.String(), Kind: "Deployment", Name: d.Name, UID: d.UID, Controller: boolPtr(true), BlockOwnerDeletion: boolPtr(true)}})
	}
	return obj, &deploymentsv1alpha1.BackendReference{APIVersion: gvk.GroupVersion().String(), Kind: kind, Resource: resource, Name: d.Name}, nil
}

func effectiveMode(mode deploymentsv1alpha1.DeploymentMode) deploymentsv1alpha1.DeploymentMode {
	if mode == "" {
		return deploymentsv1alpha1.DeploymentModeProduction
	}
	return mode
}

func effectiveDeletionPolicy(policy deploymentsv1alpha1.DeploymentDeletionPolicy) deploymentsv1alpha1.DeploymentDeletionPolicy {
	if policy == "" {
		return deploymentsv1alpha1.DeploymentDeletionPolicyRetain
	}
	return policy
}

func boolPtr(v bool) *bool { return &v }

func ensureInstance(ctx context.Context, c client.Client, want *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	desired, _, _ := unstructured.NestedMap(want.Object, "spec")
	encodedDesired, err := json.Marshal(desired)
	if err != nil {
		return nil, fmt.Errorf("encode managed backend spec: %w", err)
	}
	wantAnnotations := want.GetAnnotations()
	if wantAnnotations == nil {
		wantAnnotations = map[string]string{}
	}
	wantAnnotations[lastAppliedSpecKey] = string(encodedDesired)
	want.SetAnnotations(wantAnnotations)

	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(want.GroupVersionKind())
	err = c.Get(ctx, client.ObjectKey{Name: want.GetName()}, got)
	if apierrors.IsNotFound(err) {
		created := want.DeepCopy()
		if err := c.Create(ctx, created); err != nil {
			return nil, fmt.Errorf("create backend instance: %w", err)
		}
		return created, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get backend instance: %w", err)
	}
	next := got.DeepCopy()
	observed, _, _ := unstructured.NestedMap(got.Object, "spec")
	previous := lastAppliedSpec(got)
	next.Object["spec"] = MergeManagedSpec(observed, previous, desired)
	labels := next.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels["deployments.faros.sh/deployment"] = want.GetName()
	next.SetLabels(labels)
	annotations := next.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[lastAppliedSpecKey] = string(encodedDesired)
	next.SetAnnotations(annotations)
	next.SetOwnerReferences(want.GetOwnerReferences())
	if equality.Semantic.DeepEqual(got.Object["spec"], next.Object["spec"]) && equality.Semantic.DeepEqual(got.GetLabels(), next.GetLabels()) && equality.Semantic.DeepEqual(got.GetAnnotations(), next.GetAnnotations()) && equality.Semantic.DeepEqual(got.GetOwnerReferences(), next.GetOwnerReferences()) {
		return got, nil
	}
	if err := c.Update(ctx, next); err != nil {
		return nil, fmt.Errorf("update backend instance: %w", err)
	}
	return next, nil
}

func lastAppliedSpec(obj *unstructured.Unstructured) map[string]any {
	encoded := obj.GetAnnotations()[lastAppliedSpecKey]
	if encoded == "" {
		return nil
	}
	previous := map[string]any{}
	if err := json.Unmarshal([]byte(encoded), &previous); err != nil {
		return nil
	}
	return previous
}

// MergeManagedSpec applies the current desired values exactly for fields the
// Deployments provider managed previously, while retaining backend fields it
// never owned. A missing previous snapshot deliberately degrades to an overlay:
// the controller must not guess at ownership and delete data it did not record.
func MergeManagedSpec(observed, previous, desired map[string]any) map[string]any {
	merged := map[string]any{}
	for k, v := range observed {
		merged[k] = runtime.DeepCopyJSONValue(v)
	}
	removeNoLongerDesired(merged, previous, desired)
	merge(merged, desired)
	return merged
}

func removeNoLongerDesired(dst, previous, desired map[string]any) {
	for key, previousValue := range previous {
		desiredValue, stillDesired := desired[key]
		if !stillDesired {
			delete(dst, key)
			continue
		}
		previousMap, previousIsMap := previousValue.(map[string]any)
		desiredMap, desiredIsMap := desiredValue.(map[string]any)
		observedMap, observedIsMap := dst[key].(map[string]any)
		if previousIsMap && desiredIsMap && observedIsMap {
			removeNoLongerDesired(observedMap, previousMap, desiredMap)
		}
	}
}

func merge(dst, desired map[string]any) {
	for k, v := range desired {
		dm, ok := v.(map[string]any)
		if !ok {
			dst[k] = runtime.DeepCopyJSONValue(v)
			continue
		}
		om, ok := dst[k].(map[string]any)
		if !ok {
			om = map[string]any{}
		}
		out := map[string]any{}
		for nk, nv := range om {
			out[nk] = runtime.DeepCopyJSONValue(nv)
		}
		merge(out, dm)
		dst[k] = out
	}
}

func projectStatus(ctx context.Context, c client.Client, d *deploymentsv1alpha1.Deployment, release *deploymentsv1alpha1.Release, obj *unstructured.Unstructured, ref *deploymentsv1alpha1.BackendReference) error {
	phase := backendPhase(obj)
	d.Status.ObservedGeneration = d.Generation
	d.Status.Phase = phase
	d.Status.ActiveReleaseRef = release.Name
	d.Status.ObservedRolloutID = d.Spec.RolloutID
	d.Status.BackendRef = ref
	d.Status.URL, _, _ = unstructured.NestedString(obj.Object, "status", "url")
	d.Status.Outputs = nestedStringMap(obj.Object, "status", "outputs")
	ready := phase == "Ready"
	if ready {
		d.Status.LastSuccessfulReleaseRef = release.Name
	}
	apiMeta.SetStatusCondition(&d.Status.Conditions, metav1.Condition{Type: ConditionApplied, Status: metav1.ConditionTrue, Reason: "BackendConverged", Message: "desired release has been applied", ObservedGeneration: d.Generation})
	readyStatus := metav1.ConditionFalse
	reason := "BackendReconciling"
	message := "backend instance is reconciling"
	if ready {
		readyStatus = metav1.ConditionTrue
		reason = "BackendReady"
		message = "backend instance is ready"
	}
	apiMeta.SetStatusCondition(&d.Status.Conditions, metav1.Condition{Type: ConditionReady, Status: readyStatus, Reason: reason, Message: message, ObservedGeneration: d.Generation})
	return c.Status().Update(ctx, d)
}

func invalid(ctx context.Context, c client.Client, d *deploymentsv1alpha1.Deployment, reason, message string) (ctrl.Result, error) {
	d.Status.ObservedGeneration = d.Generation
	d.Status.Phase = "Invalid"
	apiMeta.SetStatusCondition(&d.Status.Conditions, metav1.Condition{Type: ConditionApplied, Status: metav1.ConditionFalse, Reason: reason, Message: message, ObservedGeneration: d.Generation})
	apiMeta.SetStatusCondition(&d.Status.Conditions, metav1.Condition{Type: ConditionReady, Status: metav1.ConditionFalse, Reason: reason, Message: message, ObservedGeneration: d.Generation})
	if err := c.Status().Update(ctx, d); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func dependencyPending(ctx context.Context, c client.Client, d *deploymentsv1alpha1.Deployment, reason, message string) (ctrl.Result, error) {
	d.Status.ObservedGeneration = d.Generation
	d.Status.Phase = "Pending"
	apiMeta.SetStatusCondition(&d.Status.Conditions, metav1.Condition{Type: ConditionApplied, Status: metav1.ConditionFalse, Reason: reason, Message: message, ObservedGeneration: d.Generation})
	apiMeta.SetStatusCondition(&d.Status.Conditions, metav1.Condition{Type: ConditionReady, Status: metav1.ConditionFalse, Reason: reason, Message: message, ObservedGeneration: d.Generation})
	if err := c.Status().Update(ctx, d); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: pollInterval}, nil
}

func finalize(ctx context.Context, c client.Client, d *deploymentsv1alpha1.Deployment) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(d, Finalizer) {
		return ctrl.Result{}, nil
	}
	if effectiveDeletionPolicy(d.Spec.DeletionPolicy) == deploymentsv1alpha1.DeploymentDeletionPolicyRetain {
		if err := detachBackend(ctx, c, d); err != nil {
			return ctrl.Result{}, err
		}
		controllerutil.RemoveFinalizer(d, Finalizer)
		if err := c.Update(ctx, d); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	if ref := d.Status.BackendRef; ref != nil && ref.APIVersion != "" && ref.Kind != "" && ref.Name != "" {
		obj := &unstructured.Unstructured{}
		obj.SetAPIVersion(ref.APIVersion)
		obj.SetKind(ref.Kind)
		obj.SetName(ref.Name)
		err := c.Get(ctx, client.ObjectKey{Name: ref.Name}, obj)
		if err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("observe backend instance deletion: %w", err)
		}
		if err == nil {
			if obj.GetDeletionTimestamp().IsZero() {
				if err := c.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
					return ctrl.Result{}, fmt.Errorf("delete backend instance: %w", err)
				}
			}
			// Delete acceptance is not deletion completion. Retain the finalizer
			// until a later observation proves the backend is NotFound.
			return ctrl.Result{RequeueAfter: deletePollInterval}, nil
		}
	}
	controllerutil.RemoveFinalizer(d, Finalizer)
	if err := c.Update(ctx, d); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func detachBackend(ctx context.Context, c client.Client, d *deploymentsv1alpha1.Deployment) error {
	ref := d.Status.BackendRef
	if ref == nil || ref.APIVersion == "" || ref.Kind == "" || ref.Name == "" {
		return nil
	}
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion(ref.APIVersion)
	obj.SetKind(ref.Kind)
	if err := c.Get(ctx, client.ObjectKey{Name: ref.Name}, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("observe retained backend instance: %w", err)
	}
	ownerReferences := obj.GetOwnerReferences()
	retainedReferences := ownerReferences[:0]
	for _, owner := range ownerReferences {
		if owner.APIVersion == deploymentsv1alpha1.GroupVersion.String() && owner.Kind == "Deployment" && owner.Name == d.Name && (d.UID == "" || owner.UID == d.UID) {
			continue
		}
		retainedReferences = append(retainedReferences, owner)
	}
	obj.SetOwnerReferences(retainedReferences)
	labels := obj.GetLabels()
	if labels != nil && labels["deployments.faros.sh/deployment"] == d.Name {
		delete(labels, "deployments.faros.sh/deployment")
		obj.SetLabels(labels)
	}
	annotations := obj.GetAnnotations()
	if annotations != nil {
		delete(annotations, lastAppliedSpecKey)
		obj.SetAnnotations(annotations)
	}
	if err := c.Update(ctx, obj); err != nil {
		return fmt.Errorf("detach retained backend instance: %w", err)
	}
	return nil
}

func backendPhase(obj *unstructured.Unstructured) string {
	if phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase"); strings.TrimSpace(phase) != "" {
		return phase
	}
	conditions, _, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	for _, raw := range conditions {
		if cond, ok := raw.(map[string]any); ok && cond["type"] == "Ready" {
			if cond["status"] == "True" {
				return "Ready"
			}
			if cond["status"] == "False" {
				return "Pending"
			}
		}
	}
	return "Pending"
}

func nestedStringMap(obj map[string]any, fields ...string) map[string]string {
	values, ok, _ := unstructured.NestedMap(obj, fields...)
	if !ok {
		return nil
	}
	out := map[string]string{}
	for k, v := range values {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
