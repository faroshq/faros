// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package repositorysync

import (
	"context"
	"errors"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	codeRepositoryCheckoutAPIVersion = "code.faros.sh/v1alpha1"
	checkoutOwnerAnnotation          = "deployments.faros.sh/repository-sync"
	legacyCheckoutOwnerAnnotation    = "code.faros.sh/repository-sync"
	checkoutPathAnnotation           = "deployments.faros.sh/source-path"
	checkoutNameSuffix               = "-checkout"
)

var codeRepositoryCheckoutGVK = schema.GroupVersionKind{Group: "code.faros.sh", Version: "v1alpha1", Kind: "RepositoryCheckout"}

var (
	errCheckoutPending       = errors.New("repository checkout is pending")
	errCapabilityUnavailable = errors.New("repository checkout capability transfer is unavailable")
	errCapabilityRejected    = errors.New("repository checkout capability was rejected")
)

// SourceReader is the controller's source transfer seam. Production wiring
// supplies a capability-backed bundle fetcher; tests can inject an in-memory
// fetcher without network or provider-code imports.
type SourceReader interface {
	Checkout(context.Context, client.Client, RepositorySource) (CheckoutResult, error)
}

type SourceCleaner interface {
	Cleanup(context.Context, client.Client, string) error
}

// BundleFetcher is deliberately separate from claimed-resource orchestration.
// Code owns credentials and source bytes; Deployments only receives the
// already-authorized, transient result through this seam.
type BundleFetcher interface {
	Fetch(context.Context, string, *unstructured.Unstructured) (CheckoutResult, error)
}

type RepositorySource struct {
	SyncName      string
	RepositoryRef string
	Ref           string
	Path          string
	Scope         string
}

type SourceFile struct {
	Path    string
	Content string
	Delete  bool
}

type CheckoutResult struct {
	Ref       string
	CommitSHA string
	Files     []SourceFile
	Skipped   []string
}

// CodeRepositoryCheckoutReader coordinates the claimed Code helper resource.
// It owns no source credentials and performs no Git operations.
type CodeRepositoryCheckoutReader struct {
	Fetcher BundleFetcher
}

func NewCodeRepositoryCheckoutReader(fetcher BundleFetcher) *CodeRepositoryCheckoutReader {
	return &CodeRepositoryCheckoutReader{Fetcher: fetcher}
}

func (r *CodeRepositoryCheckoutReader) Cleanup(ctx context.Context, c client.Client, syncName string) error {
	name := checkoutName(syncName)
	current := &unstructured.Unstructured{}
	current.SetGroupVersionKind(codeRepositoryCheckoutGVK)
	if err := c.Get(ctx, types.NamespacedName{Name: name}, current); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get repository checkout %q for cleanup: %w", name, err)
	}
	if !checkoutOwnedBy(current.GetAnnotations(), syncName) {
		return fmt.Errorf("repository checkout %q is not owned by RepositorySync %q", name, syncName)
	}
	if err := c.Delete(ctx, current); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete repository checkout %q: %w", name, err)
	}
	return nil
}

func (r *CodeRepositoryCheckoutReader) Checkout(ctx context.Context, c client.Client, source RepositorySource) (CheckoutResult, error) {
	name := checkoutName(source.SyncName)
	want := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": codeRepositoryCheckoutAPIVersion,
		"kind":       codeRepositoryCheckoutGVK.Kind,
		"metadata": map[string]any{
			"name": name,
			"annotations": map[string]any{
				checkoutOwnerAnnotation: source.SyncName,
				checkoutPathAnnotation:  source.Path,
			},
		},
		"spec": map[string]any{
			"repositoryRef": source.RepositoryRef,
			"ref":           source.Ref,
			"path":          source.Path,
		},
	}}
	want.SetGroupVersionKind(codeRepositoryCheckoutGVK)
	current := &unstructured.Unstructured{}
	current.SetGroupVersionKind(codeRepositoryCheckoutGVK)
	if err := c.Get(ctx, types.NamespacedName{Name: name}, current); err != nil {
		if apierrors.IsNotFound(err) {
			if err := c.Create(ctx, want); err != nil {
				return CheckoutResult{}, fmt.Errorf("create repository checkout %q: %w", name, err)
			}
			return CheckoutResult{}, errCheckoutPending
		}
		return CheckoutResult{}, fmt.Errorf("get repository checkout %q: %w", name, err)
	}
	if !checkoutOwnedBy(current.GetAnnotations(), source.SyncName) {
		return CheckoutResult{}, fmt.Errorf("repository checkout %q is not owned by RepositorySync %q", name, source.SyncName)
	}
	if !sameCheckoutSpec(current, want) {
		current.Object["spec"] = want.Object["spec"]
		annotations := current.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations[checkoutOwnerAnnotation] = source.SyncName
		annotations[checkoutPathAnnotation] = source.Path
		delete(annotations, legacyCheckoutOwnerAnnotation)
		current.SetAnnotations(annotations)
		if err := c.Update(ctx, current); err != nil {
			return CheckoutResult{}, fmt.Errorf("update repository checkout %q: %w", name, err)
		}
		return CheckoutResult{}, errCheckoutPending
	}
	phase, _, _ := unstructured.NestedString(current.Object, "status", "phase")
	switch phase {
	case "", "Pending", "Running":
		return CheckoutResult{}, errCheckoutPending
	case "Failed":
		if err := c.Delete(ctx, current); err != nil && !apierrors.IsNotFound(err) {
			return CheckoutResult{}, fmt.Errorf("delete failed repository checkout %q: %w", name, err)
		}
		return CheckoutResult{}, fmt.Errorf("Code RepositoryCheckout %q failed", name)
	case "Succeeded":
		if r == nil || r.Fetcher == nil {
			return CheckoutResult{}, errCapabilityUnavailable
		}
		result, err := r.Fetcher.Fetch(ctx, source.Scope, current)
		if err != nil {
			if errors.Is(err, errCapabilityRejected) {
				if deleteErr := c.Delete(ctx, current); deleteErr != nil && !apierrors.IsNotFound(deleteErr) {
					return CheckoutResult{}, fmt.Errorf("replace expired repository checkout %q: %w", name, deleteErr)
				}
				return CheckoutResult{}, errCheckoutPending
			}
			return CheckoutResult{}, err
		}
		if err := c.Delete(ctx, current); err != nil && !apierrors.IsNotFound(err) {
			return CheckoutResult{}, fmt.Errorf("delete repository checkout %q: %w", name, err)
		}
		return result, nil
	default:
		return CheckoutResult{}, fmt.Errorf("repository checkout %q has unknown phase %q", name, phase)
	}
}

func checkoutName(syncName string) string {
	name := strings.Trim(strings.TrimSpace(syncName), "-")
	if len(name) > 240 {
		name = name[:240]
	}
	return name + checkoutNameSuffix
}

func checkoutOwnedBy(annotations map[string]string, syncName string) bool {
	if annotations == nil {
		return false
	}
	newOwner, newFound := annotations[checkoutOwnerAnnotation]
	legacyOwner, legacyFound := annotations[legacyCheckoutOwnerAnnotation]
	if newFound && newOwner != syncName {
		return false
	}
	if legacyFound && legacyOwner != syncName {
		return false
	}
	return (newFound && newOwner == syncName) || (legacyFound && legacyOwner == syncName)
}

func sameCheckoutSpec(current, want *unstructured.Unstructured) bool {
	a, _, _ := unstructured.NestedMap(current.Object, "spec")
	b, _, _ := unstructured.NestedMap(want.Object, "spec")
	stringField := func(values map[string]any, key string) string {
		value, _ := values[key].(string)
		return value
	}
	return stringField(a, "repositoryRef") == stringField(b, "repositoryRef") &&
		stringField(a, "ref") == stringField(b, "ref") &&
		stringField(a, "path") == stringField(b, "path")
}
