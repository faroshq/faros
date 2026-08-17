// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package deployment

import (
	"context"
	"encoding/json"
	"testing"

	apiMeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	deploymentsv1alpha1 "github.com/faroshq/provider-deployments/apis/v1alpha1"
)

var appGVK = schema.GroupVersionKind{Group: "infrastructure.faros.sh", Version: "v1alpha1", Kind: "Application"}

func TestDesiredInstanceReservedFieldsAndArtifactMapping(t *testing.T) {
	config, _ := json.Marshal(map[string]any{"name": "attacker", "farosMode": "development", "farosRedeployRevision": "old", "webImage": "mutable", "replicas": float64(2)})
	d := &deploymentsv1alpha1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "demo"}, Spec: deploymentsv1alpha1.DeploymentSpec{RolloutID: "roll-2", Configuration: &runtime.RawExtension{Raw: config}}}
	release := testRelease()
	want, ref, err := DesiredInstance(d, release, testTemplate())
	if err != nil {
		t.Fatal(err)
	}
	spec, _, _ := unstructured.NestedMap(want.Object, "spec")
	if spec["name"] != "demo" || spec["farosMode"] != "production" || spec["farosRedeployRevision"] != "roll-2" {
		t.Fatalf("reserved fields did not win: %#v", spec)
	}
	if spec["webImage"] != "ghcr.io/acme/web@sha256:1" || spec["apiImage"] != "ghcr.io/acme/api@sha256:2" {
		t.Fatalf("artifacts not mapped: %#v", spec)
	}
	if spec["replicas"] != float64(2) {
		t.Fatalf("configuration lost: %#v", spec)
	}
	if ref.Resource != "applications" || ref.APIVersion != "infrastructure.faros.sh/v1alpha1" {
		t.Fatalf("unexpected backend ref: %#v", ref)
	}
}

func TestDesiredInstanceRejectsMissingArtifact(t *testing.T) {
	d := &deploymentsv1alpha1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "demo"}, Spec: deploymentsv1alpha1.DeploymentSpec{RolloutID: "roll-1"}}
	release := testRelease()
	release.Spec.Artifacts = release.Spec.Artifacts[:1]
	if _, _, err := DesiredInstance(d, release, testTemplate()); err == nil {
		t.Fatal("expected missing launchable component artifact to fail")
	}
}

func TestDesiredInstanceDevelopmentSkipsArtifactMapping(t *testing.T) {
	config, _ := json.Marshal(map[string]any{"replicas": float64(1)})
	d := &deploymentsv1alpha1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "demo"}, Spec: deploymentsv1alpha1.DeploymentSpec{Mode: deploymentsv1alpha1.DeploymentModeDevelopment, RolloutID: "workspace-2", Configuration: &runtime.RawExtension{Raw: config}}}
	release := testRelease()
	release.Spec.Artifacts = nil
	want, _, err := DesiredInstance(d, release, testTemplate())
	if err != nil {
		t.Fatal(err)
	}
	spec, _, _ := unstructured.NestedMap(want.Object, "spec")
	if spec["farosMode"] != "development" || spec["farosRedeployRevision"] != "workspace-2" {
		t.Fatalf("development reserved fields not applied: %#v", spec)
	}
	if _, found := spec["webImage"]; found {
		t.Fatalf("development deployment mapped release image: %#v", spec)
	}
	if _, found := spec["apiImage"]; found {
		t.Fatalf("development deployment mapped release image: %#v", spec)
	}
	if len(want.GetOwnerReferences()) != 0 {
		t.Fatalf("Retain deployment unexpectedly owns backend: %#v", want.GetOwnerReferences())
	}
}

func TestDesiredInstanceDeletePolicyOwnsBackend(t *testing.T) {
	d := &deploymentsv1alpha1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "demo", UID: "deployment-uid"}, Spec: deploymentsv1alpha1.DeploymentSpec{DeletionPolicy: deploymentsv1alpha1.DeploymentDeletionPolicyDelete, RolloutID: "roll-1"}}
	want, _, err := DesiredInstance(d, testRelease(), testTemplate())
	if err != nil {
		t.Fatal(err)
	}
	owners := want.GetOwnerReferences()
	if len(owners) != 1 || owners[0].Name != "demo" || owners[0].UID != "deployment-uid" {
		t.Fatalf("Delete deployment did not own backend: %#v", owners)
	}
}

func TestMergeManagedSpecAddsChangesAndDeletesOnlyPreviouslyManagedFields(t *testing.T) {
	observed := map[string]any{
		"removed":  "old",
		"changed":  "old",
		"computed": "provider-owned",
		"nested": map[string]any{
			"removed":  "old",
			"changed":  "old",
			"computed": "provider-owned",
		},
	}
	previous := map[string]any{
		"removed": "old",
		"changed": "old",
		"nested": map[string]any{
			"removed": "old",
			"changed": "old",
		},
	}
	desired := map[string]any{
		"added":   "new",
		"changed": "new",
		"nested": map[string]any{
			"added":   "new",
			"changed": "new",
		},
	}

	got := MergeManagedSpec(observed, previous, desired)
	if _, found := got["removed"]; found {
		t.Fatalf("removed managed field survived: %#v", got)
	}
	if got["added"] != "new" || got["changed"] != "new" || got["computed"] != "provider-owned" {
		t.Fatalf("top-level ownership merge failed: %#v", got)
	}
	nested := got["nested"].(map[string]any)
	if _, found := nested["removed"]; found {
		t.Fatalf("removed nested managed field survived: %#v", nested)
	}
	if nested["added"] != "new" || nested["changed"] != "new" || nested["computed"] != "provider-owned" {
		t.Fatalf("nested ownership merge failed: %#v", nested)
	}
}

func TestMergeManagedSpecWithoutOwnershipSnapshotDoesNotDeleteObservedFields(t *testing.T) {
	got := MergeManagedSpec(map[string]any{"existing": "keep"}, nil, map[string]any{"desired": "set"})
	if got["existing"] != "keep" || got["desired"] != "set" {
		t.Fatalf("safe adoption overlay failed: %#v", got)
	}
}

func TestReconcileCreatesThenUpdatesPreservingComputedFieldsAndProjectsStatus(t *testing.T) {
	ctx := context.Background()
	s := testScheme(t)
	d := testDeployment()
	initialConfig, _ := json.Marshal(map[string]any{"obsolete": "remove", "nested": map[string]any{"obsolete": "remove", "desired": "old"}})
	d.Spec.Configuration = &runtime.RawExtension{Raw: initialConfig}
	release := testRelease()
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&deploymentsv1alpha1.Deployment{}).WithObjects(d, release).Build()
	key := types.NamespacedName{Name: d.Name}
	if _, err := ReconcileClient(ctx, c, key); err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileClient(ctx, c, key); err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileClient(ctx, c, key); err != nil {
		t.Fatal(err)
	}
	instance := getUnstructured(t, ctx, c, appGVK, "demo")
	spec, _, _ := unstructured.NestedMap(instance.Object, "spec")
	if spec["webImage"] != "ghcr.io/acme/web@sha256:1" {
		t.Fatalf("create did not map image: %#v", spec)
	}

	spec["computed"] = "provider-owned"
	spec["nested"] = map[string]any{"computed": "keep"}
	instance.Object["spec"] = spec
	instance.Object["status"] = map[string]any{"phase": "Ready", "url": "https://demo.example", "outputs": map[string]any{"service": "demo"}}
	if err := c.Update(ctx, instance); err != nil {
		t.Fatal(err)
	}

	var current deploymentsv1alpha1.Deployment
	if err := c.Get(ctx, key, &current); err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(map[string]any{"nested": map[string]any{"desired": "set"}})
	current.Spec.Configuration = &runtime.RawExtension{Raw: config}
	current.Spec.RolloutID = "roll-2"
	if err := c.Update(ctx, &current); err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileClient(ctx, c, key); err != nil {
		t.Fatal(err)
	}
	instance = getUnstructured(t, ctx, c, appGVK, "demo")
	spec, _, _ = unstructured.NestedMap(instance.Object, "spec")
	if spec["computed"] != "provider-owned" {
		t.Fatalf("provider field erased: %#v", spec)
	}
	nested := spec["nested"].(map[string]any)
	if nested["computed"] != "keep" || nested["desired"] != "set" {
		t.Fatalf("nested merge failed: %#v", nested)
	}
	if _, found := spec["obsolete"]; found {
		t.Fatalf("removed configuration field survived: %#v", spec)
	}
	if _, found := nested["obsolete"]; found {
		t.Fatalf("removed nested configuration field survived: %#v", nested)
	}
	if err := c.Get(ctx, key, &current); err != nil {
		t.Fatal(err)
	}
	if current.Status.Phase != "Ready" || current.Status.URL != "https://demo.example" || current.Status.Outputs["service"] != "demo" {
		t.Fatalf("status not projected: %#v", current.Status)
	}
	if current.Status.LastSuccessfulReleaseRef != release.Name || current.Status.ObservedRolloutID != "roll-2" || current.Status.BackendRef == nil {
		t.Fatalf("release status not projected: %#v", current.Status)
	}
	if cond := apiMeta.FindStatusCondition(current.Status.Conditions, ConditionReady); cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("ready condition missing: %#v", current.Status.Conditions)
	}
}

func TestReconcileMissingReleaseRecordsPendingConditionAndRequeues(t *testing.T) {
	ctx := context.Background()
	s := testScheme(t)
	d := testDeployment()
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&deploymentsv1alpha1.Deployment{}).WithObjects(d).Build()
	key := types.NamespacedName{Name: d.Name}
	if _, err := ReconcileClient(ctx, c, key); err != nil {
		t.Fatal(err)
	}
	result, err := ReconcileClient(ctx, c, key)
	if err != nil {
		t.Fatal(err)
	}
	if result.RequeueAfter != pollInterval {
		t.Fatalf("missing release requeue = %s, want %s", result.RequeueAfter, pollInterval)
	}
	var current deploymentsv1alpha1.Deployment
	if err := c.Get(ctx, key, &current); err != nil {
		t.Fatal(err)
	}
	cond := apiMeta.FindStatusCondition(current.Status.Conditions, ConditionApplied)
	if current.Status.Phase != "Pending" || cond == nil || cond.Reason != "ReleaseNotFound" || cond.Status != metav1.ConditionFalse {
		t.Fatalf("missing ref was not surfaced: %#v", current.Status)
	}
}

func TestReconcileUnsupportedBlueprintRecordsInvalidCondition(t *testing.T) {
	ctx := context.Background()
	s := testScheme(t)
	d := testDeployment()
	release := testRelease()
	release.Spec.BlueprintRef.Name = "unknown"
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&deploymentsv1alpha1.Deployment{}).WithObjects(d, release).Build()
	key := types.NamespacedName{Name: d.Name}
	if _, err := ReconcileClient(ctx, c, key); err != nil {
		t.Fatal(err)
	}
	result, err := ReconcileClient(ctx, c, key)
	if err != nil {
		t.Fatal(err)
	}
	if result != (ctrl.Result{}) {
		t.Fatalf("unsupported blueprint result = %#v, want no retry", result)
	}
	var current deploymentsv1alpha1.Deployment
	if err := c.Get(ctx, key, &current); err != nil {
		t.Fatal(err)
	}
	cond := apiMeta.FindStatusCondition(current.Status.Conditions, ConditionApplied)
	if current.Status.Phase != "Invalid" || cond == nil || cond.Reason != "UnsupportedBlueprint" || cond.Status != metav1.ConditionFalse {
		t.Fatalf("unsupported blueprint was not surfaced: %#v", current.Status)
	}
}

func TestFinalizeRetainsFinalizerUntilBackendDeletionObserved(t *testing.T) {
	ctx := context.Background()
	s := testScheme(t)
	now := metav1.Now()
	d := testDeployment()
	d.Spec.DeletionPolicy = deploymentsv1alpha1.DeploymentDeletionPolicyDelete
	d.Finalizers = []string{Finalizer}
	d.DeletionTimestamp = &now
	d.Status.BackendRef = &deploymentsv1alpha1.BackendReference{APIVersion: appGVK.GroupVersion().String(), Kind: appGVK.Kind, Resource: "applications", Name: "demo"}
	instance := &unstructured.Unstructured{}
	instance.SetGroupVersionKind(appGVK)
	instance.SetName("demo")
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&deploymentsv1alpha1.Deployment{}).WithObjects(d, instance).Build()
	if _, err := ReconcileClient(ctx, c, types.NamespacedName{Name: d.Name}); err != nil {
		t.Fatal(err)
	}
	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(appGVK)
	if err := c.Get(ctx, client.ObjectKey{Name: "demo"}, got); err == nil {
		t.Fatal("backend instance still exists")
	}
	var current deploymentsv1alpha1.Deployment
	if err := c.Get(ctx, client.ObjectKey{Name: d.Name}, &current); err != nil {
		t.Fatal(err)
	}
	if len(current.Finalizers) == 0 {
		t.Fatal("finalizer removed before backend NotFound was observed")
	}
	if _, err := ReconcileClient(ctx, c, types.NamespacedName{Name: d.Name}); err != nil {
		t.Fatal(err)
	}
	err := c.Get(ctx, client.ObjectKey{Name: d.Name}, &current)
	if err == nil && len(current.Finalizers) != 0 {
		t.Fatalf("finalizer remains: %#v", current.Finalizers)
	}
}

func TestFinalizeRetainDetachesBackendAndRemovesFinalizer(t *testing.T) {
	ctx := context.Background()
	s := testScheme(t)
	now := metav1.Now()
	d := testDeployment()
	d.Finalizers = []string{Finalizer}
	d.DeletionTimestamp = &now
	d.Status.BackendRef = &deploymentsv1alpha1.BackendReference{APIVersion: appGVK.GroupVersion().String(), Kind: appGVK.Kind, Resource: "applications", Name: "demo"}
	instance := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": appGVK.GroupVersion().String(),
		"kind":       appGVK.Kind,
		"metadata": map[string]any{
			"name":            "demo",
			"labels":          map[string]any{"deployments.faros.sh/deployment": "demo", "provider": "keep"},
			"annotations":     map[string]any{lastAppliedSpecKey: `{"name":"demo"}`, "provider": "keep"},
			"ownerReferences": []any{map[string]any{"apiVersion": deploymentsv1alpha1.GroupVersion.String(), "kind": "Deployment", "name": "demo", "uid": "deployment-uid", "controller": true, "blockOwnerDeletion": true}},
		},
		"spec": map[string]any{"name": "demo"},
	}}
	instance.SetGroupVersionKind(appGVK)
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&deploymentsv1alpha1.Deployment{}).WithObjects(d, instance).Build()
	if _, err := ReconcileClient(ctx, c, types.NamespacedName{Name: d.Name}); err != nil {
		t.Fatal(err)
	}
	retained := getUnstructured(t, ctx, c, appGVK, "demo")
	if len(retained.GetOwnerReferences()) != 0 {
		t.Fatalf("deployment owner reference remains: %#v", retained.GetOwnerReferences())
	}
	if retained.GetLabels()["deployments.faros.sh/deployment"] != "" || retained.GetLabels()["provider"] != "keep" {
		t.Fatalf("deployment label was not detached cleanly: %#v", retained.GetLabels())
	}
	if retained.GetAnnotations()[lastAppliedSpecKey] != "" || retained.GetAnnotations()["provider"] != "keep" {
		t.Fatalf("ownership annotation was not detached cleanly: %#v", retained.GetAnnotations())
	}
	var current deploymentsv1alpha1.Deployment
	err := c.Get(ctx, client.ObjectKey{Name: d.Name}, &current)
	if err == nil && controllerContainsFinalizer(&current, Finalizer) {
		t.Fatalf("retain finalizer remains: %#v", current.Finalizers)
	}
}

func controllerContainsFinalizer(d *deploymentsv1alpha1.Deployment, finalizer string) bool {
	for _, current := range d.Finalizers {
		if current == finalizer {
			return true
		}
	}
	return false
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := deploymentsv1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	s.AddKnownTypeWithName(templateGVK, &unstructured.Unstructured{})
	s.AddKnownTypeWithName(templateGVK.GroupVersion().WithKind("TemplateList"), &unstructured.UnstructuredList{})
	s.AddKnownTypeWithName(appGVK, &unstructured.Unstructured{})
	s.AddKnownTypeWithName(appGVK.GroupVersion().WithKind("ApplicationList"), &unstructured.UnstructuredList{})
	return s
}

func testDeployment() *deploymentsv1alpha1.Deployment {
	return &deploymentsv1alpha1.Deployment{TypeMeta: metav1.TypeMeta{APIVersion: deploymentsv1alpha1.GroupVersion.String(), Kind: "Deployment"}, ObjectMeta: metav1.ObjectMeta{Name: "demo", UID: "deployment-uid"}, Spec: deploymentsv1alpha1.DeploymentSpec{ReleaseRef: "release-1", ClassName: deploymentsv1alpha1.DeploymentClassKRODirect, RolloutID: "roll-1"}}
}

func testRelease() *deploymentsv1alpha1.Release {
	return &deploymentsv1alpha1.Release{TypeMeta: metav1.TypeMeta{APIVersion: deploymentsv1alpha1.GroupVersion.String(), Kind: "Release"}, ObjectMeta: metav1.ObjectMeta{Name: "release-1"}, Spec: deploymentsv1alpha1.ReleaseSpec{Source: deploymentsv1alpha1.ReleaseSource{RepositoryRef: "repo", Revision: "abc"}, BlueprintRef: deploymentsv1alpha1.LocalObjectRef{Name: "application"}, Artifacts: []deploymentsv1alpha1.ReleaseArtifact{{Name: "web", Image: "ghcr.io/acme/web@sha256:1"}, {Name: "api", Image: "ghcr.io/acme/api@sha256:2"}}}}
}

func testTemplate() *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{"apiVersion": templateGVK.GroupVersion().String(), "kind": "Template", "metadata": map[string]any{"name": "application"}, "spec": map[string]any{"instanceCRD": map[string]any{"group": appGVK.Group, "version": appGVK.Version, "resource": "applications", "kind": appGVK.Kind}, "development": map[string]any{"components": map[string]any{"web": map[string]any{"imageInput": "webImage"}, "api": map[string]any{"imageInput": "apiImage"}}}}}}
	u.SetGroupVersionKind(templateGVK)
	return u
}

func getUnstructured(t *testing.T, ctx context.Context, c client.Client, gvk schema.GroupVersionKind, name string) *unstructured.Unstructured {
	t.Helper()
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	if err := c.Get(ctx, client.ObjectKey{Name: name}, obj); err != nil {
		t.Fatal(err)
	}
	return obj
}
