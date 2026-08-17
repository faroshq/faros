/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package repositorysync

import (
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	codev1alpha1 "github.com/faroshq/provider-code/apis/v1alpha1"
	"github.com/faroshq/provider-code/backend"
)

func TestParseDocumentsValidatesWholeGitOpsSet(t *testing.T) {
	files := []backend.RepositoryCommitFile{
		{Path: ".faros/release.yaml", Content: "apiVersion: deployments.faros.sh/v1alpha1\nkind: Release\nmetadata:\n  name: r1\nspec:\n  source:\n    repositoryRef: repo\n    revision: abc\n  blueprintRef:\n    name: web\n  artifacts: []\n"},
		{Path: ".faros/deployment.yaml", Content: "apiVersion: deployments.faros.sh/v1alpha1\nkind: Deployment\nmetadata:\n  name: d1\nspec:\n  releaseRef: r1\n  className: apps\n  rolloutID: old\n"},
		{Path: "README.md", Content: "ignored"},
	}
	docs, err := parseDocuments(files, ".faros")
	if err != nil {
		t.Fatalf("parseDocuments returned error: %v", err)
	}
	if len(docs) != 2 {
		t.Fatalf("got %d documents, want 2", len(docs))
	}
	if docs[0].sourcePath != ".faros/release.yaml" || docs[1].GetName() != "d1" {
		t.Fatalf("unexpected parsed documents: %#v", docs)
	}
}

func TestParseDocumentsAcceptsEmptyManagedTreeBeforeFirstPromotion(t *testing.T) {
	files := []backend.RepositoryCommitFile{
		{Path: "README.md", Content: "application source"},
		{Path: ".faros/.gitkeep", Content: ""},
	}
	docs, err := parseDocuments(files, ".faros")
	if err != nil {
		t.Fatalf("parse empty managed tree: %v", err)
	}
	if len(docs) != 0 {
		t.Fatalf("documents = %#v, want empty inventory before first promotion", docs)
	}
}

func TestInvalidLaterDocumentCausesZeroWrites(t *testing.T) {
	files := []backend.RepositoryCommitFile{
		{Path: ".faros/release.yaml", Content: "apiVersion: deployments.faros.sh/v1alpha1\nkind: Release\nmetadata:\n  name: r1\nspec:\n  source:\n    repositoryRef: repo\n    revision: abc\n  blueprintRef:\n    name: web\n  artifacts: []\n"},
		{Path: ".faros/deployment.yaml", Content: "apiVersion: deployments.faros.sh/v1alpha1\nkind: Deployment\nmetadata:\n  name: d1\nspec:\n  releaseRef: r1\n  mode: definitely-invalid\n"},
	}
	docs, err := parseDocuments(files, ".faros")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	c := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()
	if _, err := applyDocuments(context.Background(), c, &codev1alpha1.RepositorySync{}, "abc", docs); err == nil {
		t.Fatal("expected validation error")
	}
	for _, gvk := range []struct{ kind, name string }{{"Release", "r1"}, {"Deployment", "d1"}} {
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(deploymentsGV.WithKind(gvk.kind))
		err := c.Get(context.Background(), client.ObjectKey{Name: gvk.name}, u)
		if !apierrors.IsNotFound(err) {
			t.Fatalf("%s %s exists after rejected set: %v", gvk.kind, gvk.name, err)
		}
	}
}

func TestApplyDocumentsCreatesReleaseBeforeDeployment(t *testing.T) {
	files := []backend.RepositoryCommitFile{
		{Path: ".faros/environments/production.yaml", Content: "apiVersion: deployments.faros.sh/v1alpha1\nkind: Deployment\nmetadata:\n  name: d1\nspec:\n  releaseRef: r1\n  mode: production\n"},
		{Path: ".faros/releases/r1.yaml", Content: "apiVersion: deployments.faros.sh/v1alpha1\nkind: Release\nmetadata:\n  name: r1\nspec:\n  source:\n    repositoryRef: repo\n    revision: abc\n  blueprintRef:\n    name: web\n  artifacts: []\n"},
	}
	docs, err := parseDocuments(files, ".faros")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	c := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()
	inv, err := applyDocuments(context.Background(), c, &codev1alpha1.RepositorySync{ObjectMeta: metav1.ObjectMeta{Name: "sync"}}, "abc", docs)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(inv) != 2 || inv[0].Kind != "Release" || inv[1].Kind != "Deployment" {
		t.Fatalf("inventory order = %#v", inv)
	}
}

func TestExistingOwnershipConflictCausesZeroWrites(t *testing.T) {
	files := []backend.RepositoryCommitFile{
		{Path: ".faros/release.yaml", Content: "apiVersion: deployments.faros.sh/v1alpha1\nkind: Release\nmetadata:\n  name: r-new\nspec:\n  source:\n    repositoryRef: repo\n    revision: abc\n  blueprintRef:\n    name: web\n  artifacts: []\n"},
		{Path: ".faros/deployment.yaml", Content: "apiVersion: deployments.faros.sh/v1alpha1\nkind: Deployment\nmetadata:\n  name: occupied\nspec:\n  releaseRef: r-new\n"},
	}
	docs, err := parseDocuments(files, ".faros")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	existing := &unstructured.Unstructured{Object: map[string]any{"apiVersion": deploymentsGV.String(), "kind": "Deployment", "metadata": map[string]any{"name": "occupied", "annotations": map[string]any{ownerAnnotation: "other-sync"}}, "spec": map[string]any{"releaseRef": "old"}}}
	c := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(existing).Build()
	if _, err := applyDocuments(context.Background(), c, &codev1alpha1.RepositorySync{ObjectMeta: metav1.ObjectMeta{Name: "mine"}}, "abc", docs); err == nil {
		t.Fatal("expected ownership conflict")
	}
	release := &unstructured.Unstructured{}
	release.SetGroupVersionKind(deploymentsGV.WithKind("Release"))
	err = c.Get(context.Background(), client.ObjectKey{Name: "r-new"}, release)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("Release was partially created: %v", err)
	}
}

func TestCleanupInventoryDetachesRetainedObjects(t *testing.T) {
	objects := []client.Object{
		ownedObject("Release", "r1", "sync", ""),
		ownedObject("Deployment", "d1", "sync", "Retain"),
	}
	c := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(objects...).Build()
	sync := &codev1alpha1.RepositorySync{ObjectMeta: metav1.ObjectMeta{Name: "sync"}, Status: codev1alpha1.RepositorySyncStatus{Inventory: []codev1alpha1.RepositorySyncInventoryItem{{Kind: "Release", Name: "r1"}, {Kind: "Deployment", Name: "d1"}}}}
	pending, err := cleanupInventory(context.Background(), c, sync)
	if err != nil || pending {
		t.Fatalf("cleanup pending=%v err=%v", pending, err)
	}
	for _, item := range sync.Status.Inventory {
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(deploymentsGV.WithKind(item.Kind))
		if err := c.Get(context.Background(), client.ObjectKey{Name: item.Name}, u); err != nil {
			t.Fatalf("get retained %s: %v", item.Name, err)
		}
		if _, ok := u.GetAnnotations()[ownerAnnotation]; ok {
			t.Fatalf("%s retained stale ownership: %#v", item.Name, u.GetAnnotations())
		}
		if _, ok := u.GetAnnotations()[revisionAnnotation]; ok {
			t.Fatalf("%s retained stale config revision", item.Name)
		}
	}
}

func TestCleanupInventoryDeletesOnlyExplicitDeleteDeployment(t *testing.T) {
	deployment := ownedObject("Deployment", "d1", "sync", "Delete")
	c := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(deployment).Build()
	sync := &codev1alpha1.RepositorySync{ObjectMeta: metav1.ObjectMeta{Name: "sync"}, Status: codev1alpha1.RepositorySyncStatus{Inventory: []codev1alpha1.RepositorySyncInventoryItem{{Kind: "Deployment", Name: "d1"}}}}
	pending, err := cleanupInventory(context.Background(), c, sync)
	if err != nil || !pending {
		t.Fatalf("first cleanup pending=%v err=%v", pending, err)
	}
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(deploymentsGV.WithKind("Deployment"))
	if err := c.Get(context.Background(), client.ObjectKey{Name: "d1"}, u); !apierrors.IsNotFound(err) {
		t.Fatalf("delete-managed Deployment remains: %v", err)
	}
	pending, err = cleanupInventory(context.Background(), c, sync)
	if err != nil || pending {
		t.Fatalf("second cleanup pending=%v err=%v", pending, err)
	}
}

func ownedObject(kind, name, owner, policy string) *unstructured.Unstructured {
	annotations := map[string]any{ownerAnnotation: owner, pathAnnotation: ".faros/test.yaml", revisionAnnotation: "config-sha"}
	if kind == "Deployment" {
		annotations[deletionPolicyAnnotation] = policy
	}
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": deploymentsGV.String(), "kind": kind, "metadata": map[string]any{"name": name, "annotations": annotations}, "spec": map[string]any{}}}
}

func TestParseDocumentsRejectsForeignResourceBeforeApply(t *testing.T) {
	files := []backend.RepositoryCommitFile{
		{Path: ".faros/good.yaml", Content: "apiVersion: deployments.faros.sh/v1alpha1\nkind: Deployment\nmetadata:\n  name: d1\nspec: {}\n"},
		{Path: ".faros/bad.yaml", Content: "apiVersion: v1\nkind: Secret\nmetadata:\n  name: credentials\nspec: {}\n"},
	}
	if _, err := parseDocuments(files, ".faros"); err == nil {
		t.Fatal("expected foreign resource to reject the complete set")
	}
}

func TestCleanRootRejectsRepositoryRootAndTraversal(t *testing.T) {
	for _, value := range []string{".", "/", "../outside", "/absolute"} {
		if _, err := cleanRoot(value); err == nil {
			t.Errorf("cleanRoot(%q) unexpectedly succeeded", value)
		}
	}
	if got, err := cleanRoot(""); err != nil || got != ".faros" {
		t.Fatalf("default root = %q, %v", got, err)
	}
}
