// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package repositorysync

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	apiMeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	deploymentsv1alpha1 "github.com/faroshq/provider-deployments/apis/v1alpha1"
)

var (
	instanceGVK = schema.GroupVersionKind{Group: "infrastructure.faros.sh", Version: "v1alpha1", Kind: "Instance"}
	instanceGVR = schema.GroupVersionResource{Group: "infrastructure.faros.sh", Version: "v1alpha1", Resource: "instances"}
	configGVK   = schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	configGVR   = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}
)

type restMappedClient struct {
	client.Client
	mapper apiMeta.RESTMapper
}

func (c restMappedClient) RESTMapper() apiMeta.RESTMapper { return c.mapper }

func TestParseDocumentsAcceptsTargetNeutralObjects(t *testing.T) {
	docs, err := parseDocuments([]SourceFile{{
		Path: ".faros/desired.yaml",
		Content: `apiVersion: v1
kind: ConfigMap
metadata:
  name: settings
  namespace: product
  labels:
    app: pen-store
data:
  color: blue
---
apiVersion: infrastructure.faros.sh/v1alpha1
kind: Instance
metadata:
  name: pen-store
spec:
  template: application
`,
	}}, ".faros")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 || docs[0].GetKind() != "ConfigMap" || docs[1].GetKind() != "Instance" {
		t.Fatalf("parsed documents = %#v", docs)
	}
	if docs[0].GetNamespace() != "product" || docs[0].GetLabels()["app"] != "pen-store" {
		t.Fatalf("ConfigMap metadata was not preserved: %#v", docs[0].Object["metadata"])
	}
	if _, found, _ := unstructured.NestedMap(docs[0].Object, "spec"); found {
		t.Fatal("ConfigMap unexpectedly requires spec")
	}
}

func TestParseDocumentsRejectsControllerOwnedMetadata(t *testing.T) {
	_, err := parseDocuments([]SourceFile{{Path: ".faros/unsafe.yaml", Content: `apiVersion: v1
kind: ConfigMap
metadata:
  name: unsafe
  finalizers: [foreign.example/finalizer]
data: {}
`}}, ".faros")
	if err == nil || !strings.Contains(err.Error(), "finalizers") {
		t.Fatalf("unsafe metadata error = %v", err)
	}
}

func TestPreflightAllDocumentsBeforeAnyWrite(t *testing.T) {
	c := newMappedFakeClient(t, unstructuredObject(configGVK, "occupied", "default", map[string]any{"data": map[string]any{"owner": "human"}}))
	sync := testRepositorySync()
	docs, err := parseDocuments([]SourceFile{{Path: ".faros/all.yaml", Content: `apiVersion: infrastructure.faros.sh/v1alpha1
kind: Instance
metadata:
  name: pen-store
spec:
  template: application
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: occupied
data:
  value: desired
`}}, ".faros")
	if err != nil {
		t.Fatal(err)
	}
	_, requirements, err := preflightDocuments(context.Background(), c, sync, "abc123", docs)
	if err == nil || !strings.Contains(err.Error(), "not owned") {
		t.Fatalf("preflight error = %v", err)
	}
	if len(requirements) != 2 {
		t.Fatalf("requirements = %#v", requirements)
	}
	created := &unstructured.Unstructured{}
	created.SetGroupVersionKind(instanceGVK)
	if err := c.Get(context.Background(), client.ObjectKey{Name: "pen-store"}, created); client.IgnoreNotFound(err) != nil || err == nil {
		t.Fatalf("preflight wrote the first document: err=%v object=%#v", err, created.Object)
	}
}

func TestPreflightRejectsTwoVersionsOfTheSameStoredTarget(t *testing.T) {
	v1beta1GVK := schema.GroupVersionKind{Group: "", Version: "v1beta1", Kind: "ConfigMap"}
	mapper := apiMeta.NewDefaultRESTMapper([]schema.GroupVersion{configGVK.GroupVersion(), v1beta1GVK.GroupVersion()})
	mapper.AddSpecific(configGVK, configGVR, schema.GroupVersionResource{Version: "v1", Resource: "configmap"}, apiMeta.RESTScopeNamespace)
	mapper.AddSpecific(v1beta1GVK, schema.GroupVersionResource{Version: "v1beta1", Resource: "configmaps"}, schema.GroupVersionResource{Version: "v1beta1", Resource: "configmap"}, apiMeta.RESTScopeNamespace)
	c := restMappedClient{Client: fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build(), mapper: mapper}
	docs, err := parseDocuments([]SourceFile{{Path: ".faros/duplicates.yaml", Content: `apiVersion: v1
kind: ConfigMap
metadata:
  name: settings
  namespace: product
data: {}
---
apiVersion: v1beta1
kind: ConfigMap
metadata:
  name: settings
  namespace: product
data: {}
`}}, ".faros")
	if err != nil {
		t.Fatal(err)
	}
	_, requirements, err := preflightDocuments(context.Background(), c, testRepositorySync(), "abc123", docs)
	if err == nil || !strings.Contains(err.Error(), "duplicate target") {
		t.Fatalf("preflight error = %v", err)
	}
	conflicts := 0
	for _, requirement := range requirements {
		if requirement.State == deploymentsv1alpha1.RepositorySyncTargetConflict {
			conflicts++
		}
	}
	if len(requirements) != 2 || conflicts != 1 {
		t.Fatalf("requirements = %#v", requirements)
	}
}

func TestGenericApplyRecordsExactTargetInventory(t *testing.T) {
	c := newMappedFakeClient(t)
	sync := testRepositorySync()
	docs, err := parseDocuments([]SourceFile{{Path: ".faros/targets.yaml", Content: `apiVersion: v1
kind: ConfigMap
metadata:
  name: settings
  namespace: product
data:
  color: blue
---
apiVersion: infrastructure.faros.sh/v1alpha1
kind: Instance
metadata:
  name: pen-store
spec:
  template: application
`}}, ".faros")
	if err != nil {
		t.Fatal(err)
	}
	resolved, requirements, err := preflightDocuments(context.Background(), c, sync, "abc123", docs)
	if err != nil {
		t.Fatal(err)
	}
	for _, requirement := range requirements {
		if requirement.State != deploymentsv1alpha1.RepositorySyncTargetAuthorized {
			t.Fatalf("requirement = %#v", requirement)
		}
	}
	inventory, err := applyDocuments(context.Background(), c, sync, "abc123", resolved)
	if err != nil {
		t.Fatal(err)
	}
	sortInventory(inventory)
	if len(inventory) != 2 {
		t.Fatalf("inventory = %#v", inventory)
	}
	byResource := map[string]deploymentsv1alpha1.RepositorySyncInventoryItem{}
	for _, item := range inventory {
		byResource[item.Resource] = item
	}
	if item := byResource["configmaps"]; item.Namespace != "product" || item.SourcePath != ".faros/targets.yaml" {
		t.Fatalf("ConfigMap inventory = %#v", item)
	}
	if item := byResource["instances"]; item.APIVersion != "infrastructure.faros.sh/v1alpha1" {
		t.Fatalf("Instance inventory = %#v", item)
	}
	instance := &unstructured.Unstructured{}
	instance.SetGroupVersionKind(instanceGVK)
	if err := c.Get(context.Background(), client.ObjectKey{Name: "pen-store"}, instance); err != nil {
		t.Fatal(err)
	}
	if instance.GetAnnotations()[ownerAnnotation] != sync.Name || instance.GetAnnotations()[revisionAnnotation] != "abc123" {
		t.Fatalf("apply annotations = %#v", instance.GetAnnotations())
	}
}

func TestMissingArbitraryProviderTargetReportsAwaitingAuthorization(t *testing.T) {
	mapper := apiMeta.NewDefaultRESTMapper([]schema.GroupVersion{{Version: "v1"}})
	c := restMappedClient{Client: fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build(), mapper: mapper}
	sync := testRepositorySync()
	docs, err := parseDocuments([]SourceFile{{Path: ".faros/widget.yaml", Content: `apiVersion: example.faros.sh/v1alpha1
kind: Widget
metadata:
  name: storefront
spec:
  size: medium
`}}, ".faros")
	if err != nil {
		t.Fatal(err)
	}
	_, requirements, err := preflightDocuments(context.Background(), c, sync, "abc123", docs)
	var stageErr *syncStageError
	if err == nil || !errorsAs(err, &stageErr) || !stageErr.awaitingAuthorization {
		t.Fatalf("preflight error = %#v", err)
	}
	if len(requirements) != 1 || requirements[0].State != deploymentsv1alpha1.RepositorySyncTargetAwaitingAuthorization || requirements[0].Claim == nil {
		t.Fatalf("requirements = %#v", requirements)
	}
	if requirements[0].Claim.Group != "example.faros.sh" || requirements[0].Claim.Resource != "widgets" ||
		!slices.Equal(requirements[0].Claim.Verbs, applyVerbs) {
		t.Fatalf("claim = %#v", requirements[0].Claim)
	}
}

func TestCoreConfigMapUsesGenericAuthorizationHint(t *testing.T) {
	claim := targetClaim(configGVR)
	if claim == nil || claim.Group != "" || claim.Resource != "configmaps" {
		t.Fatalf("ConfigMap claim = %#v", claim)
	}
	if !slices.Equal(claim.Verbs, applyVerbs) {
		t.Fatalf("ConfigMap verbs = %#v", claim.Verbs)
	}
}

func TestCleanupInventoryUsesUIDAndPrunePolicy(t *testing.T) {
	owned := unstructuredObject(instanceGVK, "pen-store", "", map[string]any{"spec": map[string]any{"template": "application"}})
	owned.SetUID(types.UID("replacement-uid"))
	owned.SetAnnotations(map[string]string{ownerAnnotation: "sync-a"})
	c := newMappedFakeClient(t, owned)
	sync := testRepositorySync()
	sync.Spec.Prune = true
	sync.Status.Inventory = []deploymentsv1alpha1.RepositorySyncInventoryItem{{
		APIVersion: instanceGVK.GroupVersion().String(), Kind: instanceGVK.Kind, Resource: instanceGVR.Resource,
		Name: owned.GetName(), UID: "old-uid",
	}}
	pending, _, err := cleanupInventory(context.Background(), c, sync)
	if err != nil || pending {
		t.Fatalf("cleanup with replacement UID: pending=%t err=%v", pending, err)
	}
	remaining := &unstructured.Unstructured{}
	remaining.SetGroupVersionKind(instanceGVK)
	if err := c.Get(context.Background(), client.ObjectKey{Name: owned.GetName()}, remaining); err != nil {
		t.Fatalf("replacement was deleted: %v", err)
	}

	sync.Status.Inventory[0].UID = "replacement-uid"
	sync.Spec.Prune = false
	pending, _, err = cleanupInventory(context.Background(), c, sync)
	if err != nil || pending {
		t.Fatalf("retain cleanup: pending=%t err=%v", pending, err)
	}
	if err := c.Get(context.Background(), client.ObjectKey{Name: owned.GetName()}, remaining); err != nil {
		t.Fatal(err)
	}
	if repositorySyncOwner(remaining.GetAnnotations()) != "" {
		t.Fatalf("retained object still owned by sync: %#v", remaining.GetAnnotations())
	}
}

func newMappedFakeClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	mapper := apiMeta.NewDefaultRESTMapper([]schema.GroupVersion{instanceGVK.GroupVersion(), configGVK.GroupVersion()})
	mapper.AddSpecific(instanceGVK, instanceGVR, schema.GroupVersionResource{Group: instanceGVR.Group, Version: instanceGVR.Version, Resource: "instance"}, apiMeta.RESTScopeRoot)
	mapper.AddSpecific(configGVK, configGVR, schema.GroupVersionResource{Version: "v1", Resource: "configmap"}, apiMeta.RESTScopeNamespace)
	return restMappedClient{Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build(), mapper: mapper}
}

func unstructuredObject(gvk schema.GroupVersionKind, name, namespace string, fields map[string]any) *unstructured.Unstructured {
	object := map[string]any{
		"apiVersion": gvk.GroupVersion().String(),
		"kind":       gvk.Kind,
		"metadata":   map[string]any{"name": name},
	}
	for key, value := range fields {
		object[key] = value
	}
	u := &unstructured.Unstructured{Object: object}
	u.SetGroupVersionKind(gvk)
	u.SetNamespace(namespace)
	return u
}

func testRepositorySync() *deploymentsv1alpha1.RepositorySync {
	return &deploymentsv1alpha1.RepositorySync{ObjectMeta: metav1.ObjectMeta{Name: "sync-a", UID: types.UID("sync-uid")}}
}

func errorsAs(err error, target any) bool {
	return errors.As(err, target)
}
