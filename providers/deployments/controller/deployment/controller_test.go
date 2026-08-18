// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

func TestDesiredInstanceReservedFieldsAndArtifactMapping(t *testing.T) {
	config, _ := json.Marshal(map[string]any{"name": "attacker", "farosMode": "development", "farosRedeployRevision": "old", "webImage": "mutable", "replicas": float64(2)})
	d := &deploymentsv1alpha1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "demo"}, Spec: deploymentsv1alpha1.DeploymentSpec{RolloutID: "roll-2", Configuration: &runtime.RawExtension{Raw: config}}}
	release := testRelease()
	want, ref, err := DesiredInstance(d, release, testTemplate())
	if err != nil {
		t.Fatal(err)
	}
	spec, _, _ := unstructured.NestedMap(want.Object, "spec")
	if spec["template"] != "application" {
		t.Fatalf("template was not selected: %#v", spec)
	}
	values, _, _ := unstructured.NestedMap(spec, "values")
	if values["name"] != "demo" || values["farosMode"] != "production" || values["farosRedeployRevision"] != "roll-2" {
		t.Fatalf("reserved values did not win: %#v", values)
	}
	if values["webImage"] != "ghcr.io/acme/web@sha256:1" || values["apiImage"] != "ghcr.io/acme/api@sha256:2" {
		t.Fatalf("artifacts not mapped: %#v", values)
	}
	if values["replicas"] != float64(2) {
		t.Fatalf("configuration lost: %#v", values)
	}
	if want.GroupVersionKind() != instanceGVK || ref.Resource != "instances" || ref.Kind != "Instance" || ref.APIVersion != "infrastructure.faros.sh/v1alpha1" {
		t.Fatalf("unexpected backend ref: %#v", ref)
	}
}

func TestBuiltinTemplateContractAdmitsSupportedBlueprints(t *testing.T) {
	for name, wantComponents := range map[string]map[string]string{
		"application":   {"web": "webImage", "api": "apiImage"},
		"simple-webapp": {"app": "image"},
	} {
		t.Run(name, func(t *testing.T) {
			template, err := builtinTemplateContract(name)
			if err != nil {
				t.Fatalf("builtinTemplateContract(%q): %v", name, err)
			}
			if template.GetName() != name {
				t.Fatalf("template name = %q, want %q", template.GetName(), name)
			}
			components, ok, err := unstructured.NestedMap(template.Object, "spec", "development", "components")
			if err != nil || !ok {
				t.Fatalf("components = %#v, err = %v", components, err)
			}
			if len(components) != len(wantComponents) {
				t.Fatalf("components = %#v, want %#v", components, wantComponents)
			}
			for componentName, imageInput := range wantComponents {
				component, ok := components[componentName].(map[string]any)
				if !ok || component["imageInput"] != imageInput {
					t.Fatalf("component %q = %#v, want imageInput %q", componentName, components[componentName], imageInput)
				}
			}
		})
	}
}

func TestBuiltinTemplateContractRejectsUnknownBlueprint(t *testing.T) {
	if _, err := builtinTemplateContract("unknown"); err == nil {
		t.Fatal("unknown blueprint was admitted")
	}
}

func TestDesiredInstanceSimpleWebAppMapsAppArtifact(t *testing.T) {
	d := &deploymentsv1alpha1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "demo"}, Spec: deploymentsv1alpha1.DeploymentSpec{RolloutID: "roll-1"}}
	release := testRelease()
	release.Spec.BlueprintRef.Name = "simple-webapp"
	release.Spec.Artifacts = []deploymentsv1alpha1.ReleaseArtifact{{Name: "app", Image: "ghcr.io/acme/app@sha256:1"}}
	template, err := builtinTemplateContract("simple-webapp")
	if err != nil {
		t.Fatal(err)
	}
	want, _, err := DesiredInstance(d, release, template)
	if err != nil {
		t.Fatal(err)
	}
	spec, _, _ := unstructured.NestedMap(want.Object, "spec")
	if spec["template"] != "simple-webapp" {
		t.Fatalf("template = %#v, want simple-webapp", spec["template"])
	}
	values, _, _ := unstructured.NestedMap(spec, "values")
	if values["image"] != "ghcr.io/acme/app@sha256:1" {
		t.Fatalf("simple-webapp artifact was not mapped to image: %#v", values)
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
	values, _, _ := unstructured.NestedMap(spec, "values")
	if values["farosMode"] != "development" || values["farosRedeployRevision"] != "workspace-2" {
		t.Fatalf("development reserved values not applied: %#v", values)
	}
	if _, found := values["webImage"]; found {
		t.Fatalf("development deployment mapped release image: %#v", values)
	}
	if _, found := values["apiImage"]; found {
		t.Fatalf("development deployment mapped release image: %#v", values)
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

func TestEnsureInstancePreservesForeignOwnerReferences(t *testing.T) {
	ctx := context.Background()
	s := testScheme(t)
	d := testDeployment()
	d.Spec.DeletionPolicy = deploymentsv1alpha1.DeploymentDeletionPolicyDelete
	want, _, err := DesiredInstance(d, testRelease(), testTemplate())
	if err != nil {
		t.Fatal(err)
	}
	foreign := metav1.OwnerReference{APIVersion: "other.faros.sh/v1alpha1", Kind: "Other", Name: "other", UID: "foreign-uid", Controller: boolPtr(true)}
	own := metav1.OwnerReference{APIVersion: deploymentsv1alpha1.GroupVersion.String(), Kind: "Deployment", Name: d.Name, UID: d.UID}
	instance := testBackendInstance()
	instance.SetOwnerReferences([]metav1.OwnerReference{foreign, own})
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(instance).Build()

	if _, err := ensureInstance(ctx, c, want, d.UID); err != nil {
		t.Fatal(err)
	}
	got := getUnstructured(t, ctx, c, instanceGVK, d.Name)
	owners := got.GetOwnerReferences()
	if len(owners) != 2 || owners[0].APIVersion != foreign.APIVersion || owners[0].Kind != foreign.Kind || owners[0].Name != foreign.Name || owners[0].UID != foreign.UID || owners[0].Controller == nil || !*owners[0].Controller || owners[1].APIVersion != own.APIVersion || owners[1].Kind != own.Kind || owners[1].Name != own.Name || owners[1].UID != own.UID || owners[1].Controller == nil || *owners[1].Controller {
		t.Fatalf("foreign owner reference was not preserved: %#v", owners)
	}
}

func TestEnsureInstanceRemovesOnlyOwnOwnerReferenceForRetain(t *testing.T) {
	ctx := context.Background()
	s := testScheme(t)
	d := testDeployment()
	want, _, err := DesiredInstance(d, testRelease(), testTemplate())
	if err != nil {
		t.Fatal(err)
	}
	foreign := metav1.OwnerReference{APIVersion: "other.faros.sh/v1alpha1", Kind: "Other", Name: "other", UID: "foreign-uid"}
	own := metav1.OwnerReference{APIVersion: deploymentsv1alpha1.GroupVersion.String(), Kind: "Deployment", Name: d.Name, UID: d.UID}
	instance := testBackendInstance()
	instance.SetOwnerReferences([]metav1.OwnerReference{foreign, own})
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(instance).Build()

	if _, err := ensureInstance(ctx, c, want, d.UID); err != nil {
		t.Fatal(err)
	}
	got := getUnstructured(t, ctx, c, instanceGVK, d.Name)
	owners := got.GetOwnerReferences()
	if len(owners) != 1 || owners[0].APIVersion != foreign.APIVersion || owners[0].Kind != foreign.Kind || owners[0].Name != foreign.Name || owners[0].UID != foreign.UID {
		t.Fatalf("own owner reference was not removed while preserving foreign ownership: %#v", owners)
	}
}

func TestEnsureInstanceRetriesMetadataConflictWithFreshObject(t *testing.T) {
	ctx := context.Background()
	s := testScheme(t)
	d := testDeployment()
	want, _, err := DesiredInstance(d, testRelease(), testTemplate())
	if err != nil {
		t.Fatal(err)
	}
	instance := testBackendInstance()
	instance.SetLabels(map[string]string{"provider": "keep"})
	base := fake.NewClientBuilder().WithScheme(s).WithObjects(instance).Build()
	c := &conflictOnceClient{Client: base, conflicts: 1, beforeConflict: addRaceLabel}

	if _, err := ensureInstance(ctx, c, want, d.UID); err != nil {
		t.Fatal(err)
	}
	got := getUnstructured(t, ctx, c, instanceGVK, d.Name)
	if got.GetLabels()["race"] != "keep" {
		t.Fatalf("retry did not re-read the object after conflict: %#v", got.GetLabels())
	}
	if got.GetLabels()["deployments.faros.sh/deployment"] != d.Name {
		t.Fatalf("desired deployment label missing after retry: %#v", got.GetLabels())
	}
}

func TestDetachBackendRetriesMetadataConflictWithFreshObject(t *testing.T) {
	ctx := context.Background()
	s := testScheme(t)
	d := testDeployment()
	d.Status.BackendRef = &deploymentsv1alpha1.BackendReference{APIVersion: instanceGVK.GroupVersion().String(), Kind: instanceGVK.Kind, Resource: instanceResource, Name: d.Name, UID: "backend-uid"}
	instance := testBackendInstance()
	instance.SetLabels(map[string]string{"deployments.faros.sh/deployment": d.Name, "provider": "keep"})
	instance.SetAnnotations(map[string]string{lastAppliedSpecKey: `{"name":"demo"}`, "provider": "keep"})
	instance.SetOwnerReferences([]metav1.OwnerReference{{APIVersion: deploymentsv1alpha1.GroupVersion.String(), Kind: "Deployment", Name: d.Name, UID: d.UID}})
	base := fake.NewClientBuilder().WithScheme(s).WithObjects(d, instance).Build()
	c := &conflictOnceClient{Client: base, conflicts: 1, beforeConflict: addRaceLabel}

	if err := detachBackend(ctx, c, d); err != nil {
		t.Fatal(err)
	}
	got := getUnstructured(t, ctx, c, instanceGVK, d.Name)
	if got.GetLabels()["race"] != "keep" || got.GetLabels()["deployments.faros.sh/deployment"] != "" || got.GetAnnotations()[lastAppliedSpecKey] != "" || len(got.GetOwnerReferences()) != 0 {
		t.Fatalf("detach retry did not merge against fresh metadata: labels=%#v annotations=%#v owners=%#v", got.GetLabels(), got.GetAnnotations(), got.GetOwnerReferences())
	}
}

func TestRemoveFinalizerRetriesMetadataConflict(t *testing.T) {
	ctx := context.Background()
	s := testScheme(t)
	d := testDeployment()
	d.Finalizers = []string{Finalizer}
	base := fake.NewClientBuilder().WithScheme(s).WithObjects(d).Build()
	c := &conflictOnceClient{Client: base, conflicts: 1, conflictResource: schema.GroupResource{Group: deploymentsv1alpha1.GroupVersion.Group, Resource: "deployments"}, beforeConflict: addDeploymentRaceAnnotation}

	if err := removeFinalizer(ctx, c, d); err != nil {
		t.Fatal(err)
	}
	var current deploymentsv1alpha1.Deployment
	if err := c.Get(ctx, client.ObjectKey{Name: d.Name}, &current); err != nil {
		t.Fatal(err)
	}
	if controllerContainsFinalizer(&current, Finalizer) {
		t.Fatalf("finalizer remains after conflict retry: %#v", current.Finalizers)
	}
	if current.Annotations["race"] != "keep" {
		t.Fatalf("finalizer retry did not preserve concurrently written annotation: %#v", current.Annotations)
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
	instance := getUnstructured(t, ctx, c, instanceGVK, "demo")
	spec, _, _ := unstructured.NestedMap(instance.Object, "spec")
	values, _, _ := unstructured.NestedMap(spec, "values")
	if values["webImage"] != "ghcr.io/acme/web@sha256:1" {
		t.Fatalf("create did not map image: %#v", values)
	}

	values["computed"] = "provider-owned"
	values["nested"] = map[string]any{"computed": "keep"}
	spec["values"] = values
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
	instance = getUnstructured(t, ctx, c, instanceGVK, "demo")
	spec, _, _ = unstructured.NestedMap(instance.Object, "spec")
	values, _, _ = unstructured.NestedMap(spec, "values")
	if values["computed"] != "provider-owned" {
		t.Fatalf("provider field erased: %#v", values)
	}
	nested := values["nested"].(map[string]any)
	if nested["computed"] != "keep" || nested["desired"] != "set" {
		t.Fatalf("nested merge failed: %#v", nested)
	}
	if _, found := values["obsolete"]; found {
		t.Fatalf("removed configuration field survived: %#v", values)
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
	d.Status.BackendRef = &deploymentsv1alpha1.BackendReference{APIVersion: instanceGVK.GroupVersion().String(), Kind: instanceGVK.Kind, Resource: "instances", Name: "demo", UID: "backend-uid"}
	instance := &unstructured.Unstructured{}
	instance.SetGroupVersionKind(instanceGVK)
	instance.SetName("demo")
	instance.SetUID("backend-uid")
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&deploymentsv1alpha1.Deployment{}).WithObjects(d, instance).Build()
	if _, err := ReconcileClient(ctx, c, types.NamespacedName{Name: d.Name}); err != nil {
		t.Fatal(err)
	}
	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(instanceGVK)
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

func TestFinalizeDeleteUsesOwnerUIDWhenStatusUIDWriteWasInterrupted(t *testing.T) {
	ctx := context.Background()
	s := testScheme(t)
	now := metav1.Now()
	d := testDeployment()
	d.Spec.DeletionPolicy = deploymentsv1alpha1.DeploymentDeletionPolicyDelete
	d.Finalizers = []string{Finalizer}
	d.DeletionTimestamp = &now
	d.Status.BackendRef = &deploymentsv1alpha1.BackendReference{APIVersion: instanceGVK.GroupVersion().String(), Kind: instanceGVK.Kind, Resource: instanceResource, Name: d.Name}
	instance := testBackendInstance()
	instance.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: deploymentsv1alpha1.GroupVersion.String(), Kind: "Deployment", Name: d.Name, UID: d.UID,
	}})
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&deploymentsv1alpha1.Deployment{}).WithObjects(d, instance).Build()

	result, err := ReconcileClient(ctx, c, types.NamespacedName{Name: d.Name})
	if err != nil {
		t.Fatal(err)
	}
	if result.RequeueAfter != deletePollInterval {
		t.Fatalf("interrupted status delete requeue = %s, want %s", result.RequeueAfter, deletePollInterval)
	}
	backend := &unstructured.Unstructured{}
	backend.SetGroupVersionKind(instanceGVK)
	if err := c.Get(ctx, client.ObjectKey{Name: d.Name}, backend); !apierrors.IsNotFound(err) {
		t.Fatalf("owner-identified backend remains after Delete finalization: %v", err)
	}
	var current deploymentsv1alpha1.Deployment
	if err := c.Get(ctx, client.ObjectKey{Name: d.Name}, &current); err != nil {
		t.Fatal(err)
	}
	if !controllerContainsFinalizer(&current, Finalizer) {
		t.Fatal("Deployment finalizer was removed before backend NotFound observation")
	}
}

func TestFinalizeDeleteEmptyStatusUIDDoesNotTouchUnownedBackend(t *testing.T) {
	for _, tc := range []struct {
		name  string
		owner *metav1.OwnerReference
	}{
		{name: "no owner"},
		{name: "different deployment UID", owner: &metav1.OwnerReference{
			APIVersion: deploymentsv1alpha1.GroupVersion.String(), Kind: "Deployment", Name: "demo", UID: "different-deployment-uid",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s := testScheme(t)
			now := metav1.Now()
			d := testDeployment()
			d.Spec.DeletionPolicy = deploymentsv1alpha1.DeploymentDeletionPolicyDelete
			d.Finalizers = []string{Finalizer}
			d.DeletionTimestamp = &now
			d.Status.BackendRef = &deploymentsv1alpha1.BackendReference{APIVersion: instanceGVK.GroupVersion().String(), Kind: instanceGVK.Kind, Resource: instanceResource, Name: d.Name}
			backend := testBackendInstance()
			if tc.owner != nil {
				backend.SetOwnerReferences([]metav1.OwnerReference{*tc.owner})
			}
			c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&deploymentsv1alpha1.Deployment{}).WithObjects(d, backend).Build()

			if _, err := ReconcileClient(ctx, c, types.NamespacedName{Name: d.Name}); err != nil {
				t.Fatal(err)
			}
			got := getUnstructured(t, ctx, c, instanceGVK, d.Name)
			if got.GetUID() != backend.GetUID() {
				t.Fatalf("unowned backend changed: UID=%q, want %q", got.GetUID(), backend.GetUID())
			}
		})
	}
}

func TestRemoveFinalizerDoesNotTouchReplacementDeployment(t *testing.T) {
	ctx := context.Background()
	s := testScheme(t)
	stale := testDeployment()
	stale.Finalizers = []string{Finalizer}
	replacement := stale.DeepCopy()
	replacement.UID = "replacement-deployment-uid"
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(replacement).Build()

	if err := removeFinalizer(ctx, c, stale); err != nil {
		t.Fatal(err)
	}
	var current deploymentsv1alpha1.Deployment
	if err := c.Get(ctx, client.ObjectKey{Name: replacement.Name}, &current); err != nil {
		t.Fatal(err)
	}
	if !controllerContainsFinalizer(&current, Finalizer) {
		t.Fatal("stale reconcile removed replacement Deployment finalizer")
	}
}

func TestFinalizeRetainDetachesBackendAndRemovesFinalizer(t *testing.T) {
	ctx := context.Background()
	s := testScheme(t)
	now := metav1.Now()
	d := testDeployment()
	d.Finalizers = []string{Finalizer}
	d.DeletionTimestamp = &now
	d.Status.BackendRef = &deploymentsv1alpha1.BackendReference{APIVersion: instanceGVK.GroupVersion().String(), Kind: instanceGVK.Kind, Resource: "instances", Name: "demo", UID: "backend-uid"}
	instance := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": instanceGVK.GroupVersion().String(),
		"kind":       instanceGVK.Kind,
		"metadata": map[string]any{
			"name":        "demo",
			"labels":      map[string]any{"deployments.faros.sh/deployment": "demo", "provider": "keep"},
			"annotations": map[string]any{lastAppliedSpecKey: `{"name":"demo"}`, "provider": "keep"},
			"ownerReferences": []any{
				map[string]any{"apiVersion": "other.faros.sh/v1alpha1", "kind": "Other", "name": "other", "uid": "foreign-uid"},
				map[string]any{"apiVersion": deploymentsv1alpha1.GroupVersion.String(), "kind": "Deployment", "name": "demo", "uid": "deployment-uid", "controller": true, "blockOwnerDeletion": true},
			},
		},
		"spec": map[string]any{"template": "application", "values": map[string]any{"name": "demo"}},
	}}
	instance.SetGroupVersionKind(instanceGVK)
	instance.SetUID("backend-uid")
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&deploymentsv1alpha1.Deployment{}).WithObjects(d, instance).Build()
	if _, err := ReconcileClient(ctx, c, types.NamespacedName{Name: d.Name}); err != nil {
		t.Fatal(err)
	}
	retained := getUnstructured(t, ctx, c, instanceGVK, "demo")
	owners := retained.GetOwnerReferences()
	if len(owners) != 1 || owners[0].UID != "foreign-uid" {
		t.Fatalf("deployment owner reference was not removed while preserving foreign owner: %#v", owners)
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

func TestFinalizeDeleteSkipsSameNameReplacementByUID(t *testing.T) {
	ctx := context.Background()
	s := testScheme(t)
	now := metav1.Now()
	d := testDeployment()
	d.Spec.DeletionPolicy = deploymentsv1alpha1.DeploymentDeletionPolicyDelete
	d.Finalizers = []string{Finalizer}
	d.DeletionTimestamp = &now
	d.Status.BackendRef = &deploymentsv1alpha1.BackendReference{APIVersion: instanceGVK.GroupVersion().String(), Kind: instanceGVK.Kind, Resource: instanceResource, Name: d.Name, UID: "original-uid"}
	replacement := testBackendInstance()
	replacement.SetUID("replacement-uid")
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&deploymentsv1alpha1.Deployment{}).WithObjects(d, replacement).Build()

	if _, err := ReconcileClient(ctx, c, types.NamespacedName{Name: d.Name}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, client.ObjectKey{Name: d.Name}, replacement); err != nil {
		t.Fatal(err)
	}
	var current deploymentsv1alpha1.Deployment
	err := c.Get(ctx, client.ObjectKey{Name: d.Name}, &current)
	if err != nil && !apierrors.IsNotFound(err) {
		t.Fatal(err)
	}
	if err == nil && controllerContainsFinalizer(&current, Finalizer) {
		t.Fatalf("finalizer remained after original backend UID disappeared: %#v", current.Finalizers)
	}
}

func TestFinalizeDeleteUIDPreconditionProtectsReplacementAfterGet(t *testing.T) {
	ctx := context.Background()
	s := testScheme(t)
	now := metav1.Now()
	d := testDeployment()
	d.Spec.DeletionPolicy = deploymentsv1alpha1.DeploymentDeletionPolicyDelete
	d.Finalizers = []string{Finalizer}
	d.DeletionTimestamp = &now
	d.Status.BackendRef = &deploymentsv1alpha1.BackendReference{APIVersion: instanceGVK.GroupVersion().String(), Kind: instanceGVK.Kind, Resource: instanceResource, Name: d.Name, UID: "backend-uid"}
	original := testBackendInstance()
	base := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&deploymentsv1alpha1.Deployment{}).WithObjects(d, original).Build()
	c := &deleteRaceClient{Client: base}

	result, err := ReconcileClient(ctx, c, types.NamespacedName{Name: d.Name})
	if err != nil {
		t.Fatal(err)
	}
	if result.RequeueAfter != deletePollInterval {
		t.Fatalf("UID precondition conflict requeue = %s, want %s", result.RequeueAfter, deletePollInterval)
	}
	if !c.sawUIDPrecondition {
		t.Fatal("delete did not include the observed backend UID precondition")
	}
	replacement := getUnstructured(t, ctx, c, instanceGVK, d.Name)
	if replacement.GetUID() != "replacement-uid" {
		t.Fatalf("same-name replacement was deleted: UID=%q", replacement.GetUID())
	}
	var afterConflict deploymentsv1alpha1.Deployment
	if err := c.Get(ctx, client.ObjectKey{Name: d.Name}, &afterConflict); err != nil {
		t.Fatal(err)
	}
	if !controllerContainsFinalizer(&afterConflict, Finalizer) {
		t.Fatal("Deployment finalizer was removed on UID precondition conflict")
	}

	if _, err := ReconcileClient(ctx, c, types.NamespacedName{Name: d.Name}); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, client.ObjectKey{Name: d.Name}, replacement); err != nil {
		t.Fatal(err)
	}
}

func TestFinalizeRetainSkipsSameNameReplacementByUID(t *testing.T) {
	ctx := context.Background()
	s := testScheme(t)
	now := metav1.Now()
	d := testDeployment()
	d.Finalizers = []string{Finalizer}
	d.DeletionTimestamp = &now
	d.Status.BackendRef = &deploymentsv1alpha1.BackendReference{APIVersion: instanceGVK.GroupVersion().String(), Kind: instanceGVK.Kind, Resource: instanceResource, Name: d.Name, UID: "original-uid"}
	replacement := testBackendInstance()
	replacement.SetUID("replacement-uid")
	replacement.SetLabels(map[string]string{"deployments.faros.sh/deployment": d.Name, "provider": "keep"})
	replacement.SetAnnotations(map[string]string{lastAppliedSpecKey: `{"name":"demo"}`, "provider": "keep"})
	foreign := metav1.OwnerReference{APIVersion: "other.faros.sh/v1alpha1", Kind: "Other", Name: "other", UID: "foreign-uid"}
	replacement.SetOwnerReferences([]metav1.OwnerReference{foreign})
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&deploymentsv1alpha1.Deployment{}).WithObjects(d, replacement).Build()

	if _, err := ReconcileClient(ctx, c, types.NamespacedName{Name: d.Name}); err != nil {
		t.Fatal(err)
	}
	retained := getUnstructured(t, ctx, c, instanceGVK, d.Name)
	owners := retained.GetOwnerReferences()
	if retained.GetLabels()["deployments.faros.sh/deployment"] != d.Name || retained.GetAnnotations()[lastAppliedSpecKey] == "" || len(owners) != 1 || owners[0].APIVersion != foreign.APIVersion || owners[0].Kind != foreign.Kind || owners[0].Name != foreign.Name || owners[0].UID != foreign.UID {
		t.Fatalf("same-name replacement was detached: labels=%#v annotations=%#v owners=%#v", retained.GetLabels(), retained.GetAnnotations(), retained.GetOwnerReferences())
	}
	var current deploymentsv1alpha1.Deployment
	err := c.Get(ctx, client.ObjectKey{Name: d.Name}, &current)
	if err != nil && !apierrors.IsNotFound(err) {
		t.Fatal(err)
	}
	if err == nil && controllerContainsFinalizer(&current, Finalizer) {
		t.Fatalf("finalizer remained after original backend UID disappeared: %#v", current.Finalizers)
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

type conflictOnceClient struct {
	client.Client
	conflicts        int
	conflictResource schema.GroupResource
	beforeConflict   func(context.Context, client.Client, client.Object) error
}

func (c *conflictOnceClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if c.conflicts > 0 {
		c.conflicts--
		if c.beforeConflict != nil {
			if err := c.beforeConflict(ctx, c.Client, obj); err != nil {
				return err
			}
		}
		resource := c.conflictResource
		if resource.Group == "" && resource.Resource == "" {
			resource = schema.GroupResource{Group: instanceGVK.Group, Resource: instanceResource}
		}
		return apierrors.NewConflict(resource, obj.GetName(), errors.New("transient conflict"))
	}
	return c.Client.Update(ctx, obj, opts...)
}

type deleteRaceClient struct {
	client.Client
	sawUIDPrecondition bool
	raced              bool
}

func (c *deleteRaceClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	original := &unstructured.Unstructured{}
	original.SetGroupVersionKind(instanceGVK)
	if err := c.Client.Get(ctx, client.ObjectKey{Name: obj.GetName()}, original); err != nil {
		return err
	}
	originalUID := original.GetUID()
	if !c.raced {
		if err := c.Client.Delete(ctx, original); err != nil {
			return err
		}
		replacement := testBackendInstance()
		replacement.SetUID("replacement-uid")
		if err := c.Client.Create(ctx, replacement); err != nil {
			return err
		}
		c.raced = true
	}
	deleteOptions := (&client.DeleteOptions{}).ApplyOptions(opts)
	if deleteOptions.Preconditions != nil && deleteOptions.Preconditions.UID != nil && *deleteOptions.Preconditions.UID == originalUID {
		c.sawUIDPrecondition = true
		return apierrors.NewConflict(schema.GroupResource{Group: instanceGVK.Group, Resource: instanceResource}, obj.GetName(), errors.New("backend UID changed before delete"))
	}
	return c.Client.Delete(ctx, obj, opts...)
}

func addRaceLabel(ctx context.Context, c client.Client, obj client.Object) error {
	latest := &unstructured.Unstructured{}
	latest.SetGroupVersionKind(instanceGVK)
	if err := c.Get(ctx, client.ObjectKey{Name: obj.GetName()}, latest); err != nil {
		return err
	}
	labels := latest.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels["race"] = "keep"
	latest.SetLabels(labels)
	return c.Update(ctx, latest)
}

func addDeploymentRaceAnnotation(ctx context.Context, c client.Client, obj client.Object) error {
	var latest deploymentsv1alpha1.Deployment
	if err := c.Get(ctx, client.ObjectKey{Name: obj.GetName()}, &latest); err != nil {
		return err
	}
	if latest.Annotations == nil {
		latest.Annotations = map[string]string{}
	}
	latest.Annotations["race"] = "keep"
	return c.Update(ctx, &latest)
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := deploymentsv1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	s.AddKnownTypeWithName(templateGVK, &unstructured.Unstructured{})
	s.AddKnownTypeWithName(templateGVK.GroupVersion().WithKind("TemplateList"), &unstructured.UnstructuredList{})
	s.AddKnownTypeWithName(instanceGVK, &unstructured.Unstructured{})
	s.AddKnownTypeWithName(instanceGVK.GroupVersion().WithKind("InstanceList"), &unstructured.UnstructuredList{})
	return s
}

func testDeployment() *deploymentsv1alpha1.Deployment {
	return &deploymentsv1alpha1.Deployment{TypeMeta: metav1.TypeMeta{APIVersion: deploymentsv1alpha1.GroupVersion.String(), Kind: "Deployment"}, ObjectMeta: metav1.ObjectMeta{Name: "demo", UID: "deployment-uid"}, Spec: deploymentsv1alpha1.DeploymentSpec{ReleaseRef: "release-1", ClassName: deploymentsv1alpha1.DeploymentClassKRODirect, RolloutID: "roll-1"}}
}

func testRelease() *deploymentsv1alpha1.Release {
	return &deploymentsv1alpha1.Release{TypeMeta: metav1.TypeMeta{APIVersion: deploymentsv1alpha1.GroupVersion.String(), Kind: "Release"}, ObjectMeta: metav1.ObjectMeta{Name: "release-1"}, Spec: deploymentsv1alpha1.ReleaseSpec{Source: deploymentsv1alpha1.ReleaseSource{RepositoryRef: "repo", Revision: "abc"}, BlueprintRef: deploymentsv1alpha1.LocalObjectRef{Name: "application"}, Artifacts: []deploymentsv1alpha1.ReleaseArtifact{{Name: "web", Image: "ghcr.io/acme/web@sha256:1"}, {Name: "api", Image: "ghcr.io/acme/api@sha256:2"}}}}
}

func testTemplate() *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{"apiVersion": templateGVK.GroupVersion().String(), "kind": "Template", "metadata": map[string]any{"name": "application"}, "spec": map[string]any{"development": map[string]any{"components": map[string]any{"web": map[string]any{"imageInput": "webImage"}, "api": map[string]any{"imageInput": "apiImage"}}}}}}
	u.SetGroupVersionKind(templateGVK)
	return u
}

func testBackendInstance() *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": instanceGVK.GroupVersion().String(),
		"kind":       instanceGVK.Kind,
		"metadata":   map[string]any{"name": "demo"},
		"spec":       map[string]any{"template": "application", "values": map[string]any{"existing": "keep"}},
	}}
	u.SetGroupVersionKind(instanceGVK)
	u.SetUID("backend-uid")
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
