// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package repositorysync

import (
	"context"
	"errors"
	"fmt"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type sourceTestFetcher struct {
	called bool
	err    error
}

func (f *sourceTestFetcher) Fetch(_ context.Context, _ string, checkout *unstructured.Unstructured) (CheckoutResult, error) {
	f.called = true
	if checkout.GetName() != "sync-a-checkout" {
		return CheckoutResult{}, fmt.Errorf("checkout name = %q", checkout.GetName())
	}
	if f.err != nil {
		return CheckoutResult{}, f.err
	}
	return CheckoutResult{CommitSHA: "abc123", Files: []SourceFile{{Path: ".faros/release.yaml", Content: "release"}}}, nil
}

func TestSameCheckoutSpecTreatsOmittedOptionalRefAsEmpty(t *testing.T) {
	current := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{
			"repositoryRef": "demo",
			"path":          ".faros",
		},
	}}
	want := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{
			"repositoryRef": "demo",
			"ref":           "",
			"path":          ".faros",
		},
	}}
	if !sameCheckoutSpec(current, want) {
		t.Fatal("sameCheckoutSpec treated an omitted optional ref as drift")
	}
}

func TestCheckoutOwnedByAcceptsLegacyAnnotationOnlyDuringMove(t *testing.T) {
	if !checkoutOwnedBy(map[string]string{legacyCheckoutOwnerAnnotation: "sync-a"}, "sync-a") {
		t.Fatal("legacy owner annotation was not accepted for migration")
	}
	if checkoutOwnedBy(map[string]string{legacyCheckoutOwnerAnnotation: "sync-a"}, "sync-b") {
		t.Fatal("legacy owner annotation was accepted for a different sync")
	}
	if checkoutOwnedBy(map[string]string{
		checkoutOwnerAnnotation:       "sync-a",
		legacyCheckoutOwnerAnnotation: "sync-b",
	}, "sync-a") {
		t.Fatal("conflicting current and legacy owner annotations were not rejected")
	}
}

func TestCodeRepositoryCheckoutReaderTransfersThenDeletesSucceededCheckout(t *testing.T) {
	checkout := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": codeRepositoryCheckoutAPIVersion,
		"kind":       "RepositoryCheckout",
		"metadata": map[string]any{
			"name": "sync-a-checkout",
			"annotations": map[string]any{
				legacyCheckoutOwnerAnnotation: "sync-a",
			},
		},
		"spec": map[string]any{
			"repositoryRef": "repo-a",
			"ref":           "main",
			"path":          ".faros",
		},
		"status": map[string]any{
			"phase":     "Succeeded",
			"commitSHA": "abc123",
		},
	}}
	checkout.SetGroupVersionKind(codeRepositoryCheckoutGVK)
	c := fake.NewClientBuilder().WithObjects(checkout).Build()
	fetcher := &sourceTestFetcher{}
	reader := NewCodeRepositoryCheckoutReader(fetcher)
	result, err := reader.Checkout(context.Background(), c, RepositorySource{
		SyncName:      "sync-a",
		RepositoryRef: "repo-a",
		Ref:           "main",
		Path:          ".faros",
		Scope:         "tenant-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !fetcher.called || result.CommitSHA != "abc123" || len(result.Files) != 1 {
		t.Fatalf("fetcher called=%t result=%+v", fetcher.called, result)
	}
	remaining := &unstructured.Unstructured{}
	remaining.SetGroupVersionKind(codeRepositoryCheckoutGVK)
	if err := c.Get(context.Background(), types.NamespacedName{Name: "sync-a-checkout"}, remaining); !apierrors.IsNotFound(err) {
		t.Fatalf("checkout after successful transfer: err=%v, object=%#v", err, remaining.Object)
	}
}

func TestCodeRepositoryCheckoutReaderReplacesRejectedCapability(t *testing.T) {
	checkout := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": codeRepositoryCheckoutAPIVersion,
		"kind":       "RepositoryCheckout",
		"metadata": map[string]any{
			"name": "sync-a-checkout",
			"annotations": map[string]any{
				checkoutOwnerAnnotation: "sync-a",
			},
		},
		"spec": map[string]any{
			"repositoryRef": "repo-a",
			"ref":           "main",
			"path":          ".faros",
		},
		"status": map[string]any{
			"phase":     "Succeeded",
			"commitSHA": "abc123",
		},
	}}
	checkout.SetGroupVersionKind(codeRepositoryCheckoutGVK)
	c := fake.NewClientBuilder().WithObjects(checkout).Build()
	reader := NewCodeRepositoryCheckoutReader(&sourceTestFetcher{err: fmt.Errorf("%w: HTTP 401", errCapabilityRejected)})
	if _, err := reader.Checkout(context.Background(), c, RepositorySource{
		SyncName:      "sync-a",
		RepositoryRef: "repo-a",
		Ref:           "main",
		Path:          ".faros",
		Scope:         "tenant-a",
	}); !errors.Is(err, errCheckoutPending) {
		t.Fatalf("rejected capability error = %v, want checkout pending after replacement", err)
	}
	remaining := &unstructured.Unstructured{}
	remaining.SetGroupVersionKind(codeRepositoryCheckoutGVK)
	if err := c.Get(context.Background(), types.NamespacedName{Name: "sync-a-checkout"}, remaining); !apierrors.IsNotFound(err) {
		t.Fatalf("rejected checkout was not deleted for replacement: err=%v", err)
	}
}
