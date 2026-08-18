/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package project

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	apisv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	"github.com/faroshq/provider-app-studio/bindings"
)

func binding(name string) aiv1alpha1.ProjectProviderBindingSpec {
	return aiv1alpha1.ProjectProviderBindingSpec{
		Name:     name,
		Provider: "infrastructure",
		Kind:     aiv1alpha1.ProjectBindingKindProviderResource,
		ResourceRef: &aiv1alpha1.ProjectProviderResourceReference{
			APIVersion: "infrastructure.faros.sh/v1alpha1",
			Kind:       "Instance",
			Resource:   "instances",
		},
		Values: runtime.RawExtension{Raw: []byte(`{}`)},
	}
}

func actionsDevelopmentBinding(values string) aiv1alpha1.ProjectProviderBindingSpec {
	b := binding(projectDevelopmentBindingName)
	b.Provider = projectDevelopmentProvider
	b.ResourceRef.Name = "demo-dev"
	b.Values = runtime.RawExtension{Raw: []byte(values)}
	return b
}

// providerBindings must select provider-resource bindings from EVERY
// environment — promotion appends an artifact-mode production binding and
// relies on this reconciler to provision it (the HTTP layer no longer does).
func TestProviderBindingsSpansAllEnvironmentModes(t *testing.T) {
	p := &aiv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "demo"},
		Spec: aiv1alpha1.ProjectSpec{
			Environments: []aiv1alpha1.ProjectEnvironmentSpec{
				{
					Name:     "development",
					Mode:     aiv1alpha1.ProjectEnvironmentModeLive,
					Bindings: []aiv1alpha1.ProjectProviderBindingSpec{binding("development")},
				},
				{
					Name:     "production",
					Mode:     aiv1alpha1.ProjectEnvironmentModeArtifact,
					Bindings: []aiv1alpha1.ProjectProviderBindingSpec{binding("production")},
				},
				{
					// No resourceRef → not lifecycled.
					Name: "empty",
					Mode: aiv1alpha1.ProjectEnvironmentModeLive,
					Bindings: []aiv1alpha1.ProjectProviderBindingSpec{{
						Name: "unbound", Provider: "infrastructure",
						Kind: aiv1alpha1.ProjectBindingKindProviderResource,
					}},
				},
			},
		},
	}
	got := providerBindings(p)
	if len(got) != 2 {
		t.Fatalf("providerBindings = %d envs, want 2 (development + production)", len(got))
	}
	if got[0].spec.Name != "development" || got[1].spec.Name != "production" {
		t.Fatalf("selected envs = %s, %s", got[0].spec.Name, got[1].spec.Name)
	}
}

func TestProviderBindingsEnforcesDeliveryWriter(t *testing.T) {
	runtimeBinding := binding("development")
	syncBinding := binding("gitops")
	syncBinding.Provider = "deployments"
	syncBinding.ResourceRef.APIVersion = "deployments.faros.sh/v1alpha1"
	syncBinding.ResourceRef.Resource = "repositorysyncs"
	project := &aiv1alpha1.Project{Spec: aiv1alpha1.ProjectSpec{Environments: []aiv1alpha1.ProjectEnvironmentSpec{
		{Name: projectDevelopmentEnvironmentName, Bindings: []aiv1alpha1.ProjectProviderBindingSpec{runtimeBinding}},
		{Name: "configuration", Bindings: []aiv1alpha1.ProjectProviderBindingSpec{syncBinding}},
		{Name: "production", Bindings: []aiv1alpha1.ProjectProviderBindingSpec{{
			Name: "prod", Provider: "infrastructure", Kind: aiv1alpha1.ProjectBindingKindProviderReference,
			ResourceRef: &aiv1alpha1.ProjectProviderResourceReference{APIVersion: "infrastructure.faros.sh/v1alpha1", Kind: "Instance", Resource: "instances", Name: "demo-prod"},
		}}},
	}}}

	direct := providerBindings(project)
	if len(direct) != 1 || len(direct[0].bindings) != 1 || direct[0].bindings[0].Name != "development" {
		t.Fatalf("Direct bindings = %+v, want runtime only", direct)
	}
	project.Spec.Delivery = controllerTestDelivery(aiv1alpha1.ProjectDeliveryModeDirect, aiv1alpha1.ProjectDeliveryModeGitOps)
	gitOps := providerBindings(project)
	if len(gitOps) != 2 || gitOps[0].bindings[0].Name != "development" || gitOps[1].bindings[0].Name != "gitops" {
		t.Fatalf("hybrid bindings = %+v, want direct development plus RepositorySync and no Git-owned production reference", gitOps)
	}
	project.Spec.Delivery = controllerTestDelivery(aiv1alpha1.ProjectDeliveryModeGitOps, aiv1alpha1.ProjectDeliveryModeGitOps)
	allGitOps := providerBindings(project)
	if len(allGitOps) != 1 || allGitOps[0].bindings[0].Name != "gitops" {
		t.Fatalf("all-GitOps bindings = %+v, want RepositorySync only", allGitOps)
	}
}

func TestGitOpsProductionRejectsConflictingDirectWriter(t *testing.T) {
	syncBinding := binding("gitops")
	syncBinding.Provider = "deployments"
	syncBinding.ResourceRef.APIVersion = "deployments.faros.sh/v1alpha1"
	syncBinding.ResourceRef.Resource = "repositorysyncs"
	production := binding("prod")
	production.Provider = "deployments"
	project := &aiv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "demo"},
		Spec: aiv1alpha1.ProjectSpec{
			Delivery:   controllerTestDelivery(aiv1alpha1.ProjectDeliveryModeDirect, aiv1alpha1.ProjectDeliveryModeGitOps),
			Repository: &aiv1alpha1.ProjectRepositoryBinding{RepositoryRef: "demo"},
			Environments: []aiv1alpha1.ProjectEnvironmentSpec{
				{Name: "configuration", Bindings: []aiv1alpha1.ProjectProviderBindingSpec{syncBinding}},
				{Name: "production", Bindings: []aiv1alpha1.ProjectProviderBindingSpec{production}},
			},
		},
	}
	err := validateDeliveryBindings(project, providerBindings(project))
	if err == nil || !strings.Contains(err.Error(), "direct writer") {
		t.Fatalf("validation error = %v, want conflicting production writer failure", err)
	}
}

func controllerTestDelivery(development, production aiv1alpha1.ProjectDeliveryMode) *aiv1alpha1.ProjectDeliverySpec {
	return &aiv1alpha1.ProjectDeliverySpec{
		Development: aiv1alpha1.ProjectEnvironmentDeliverySpec{Mode: development},
		Production:  aiv1alpha1.ProjectEnvironmentDeliverySpec{Mode: production},
	}
}

func TestGitOpsDeliveryRequiresRepositorySync(t *testing.T) {
	project := &aiv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "demo"},
		Spec: aiv1alpha1.ProjectSpec{
			Delivery:   controllerTestDelivery(aiv1alpha1.ProjectDeliveryModeDirect, aiv1alpha1.ProjectDeliveryModeGitOps),
			Repository: &aiv1alpha1.ProjectRepositoryBinding{RepositoryRef: "demo"},
		},
	}
	if err := validateDeliveryBindings(project, providerBindings(project)); err == nil || !strings.Contains(err.Error(), "no owning RepositorySync") {
		t.Fatalf("validation error = %v, want missing RepositorySync", err)
	}
}

func TestDirectDeliveryRejectsStaleRepositorySync(t *testing.T) {
	syncBinding := binding("gitops")
	syncBinding.Provider = "code"
	syncBinding.ResourceRef.Kind = "RepositorySync"
	syncBinding.ResourceRef.APIVersion = "code.faros.sh/v1alpha1"
	syncBinding.ResourceRef.Resource = "repositorysyncs"
	project := &aiv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy"},
		Spec: aiv1alpha1.ProjectSpec{Environments: []aiv1alpha1.ProjectEnvironmentSpec{{
			Name: "configuration", Bindings: []aiv1alpha1.ProjectProviderBindingSpec{syncBinding},
		}}},
	}
	if err := validateDeliveryBindings(project, providerBindings(project)); err == nil || !strings.Contains(err.Error(), "explicit migration or cleanup") {
		t.Fatalf("validation error = %v, want stale RepositorySync failure", err)
	}
}

func TestLegacyCodeRepositorySyncRequiresMigration(t *testing.T) {
	syncBinding := binding("gitops")
	syncBinding.Provider = "code"
	syncBinding.ResourceRef.APIVersion = "code.faros.sh/v1alpha1"
	syncBinding.ResourceRef.Kind = "RepositorySync"
	syncBinding.ResourceRef.Resource = "repositorysyncs"
	project := &aiv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "legacy"},
		Spec: aiv1alpha1.ProjectSpec{
			Delivery:   controllerTestDelivery(aiv1alpha1.ProjectDeliveryModeDirect, aiv1alpha1.ProjectDeliveryModeGitOps),
			Repository: &aiv1alpha1.ProjectRepositoryBinding{RepositoryRef: "demo"},
			Environments: []aiv1alpha1.ProjectEnvironmentSpec{{
				Name: "configuration", Bindings: []aiv1alpha1.ProjectProviderBindingSpec{syncBinding},
			}},
		},
	}
	err := validateDeliveryBindings(project, providerBindings(project))
	if err == nil || !strings.Contains(err.Error(), "legacy Code RepositorySync") || !strings.Contains(err.Error(), "migration") {
		t.Fatalf("validation error = %v, want explicit legacy RepositorySync migration failure", err)
	}
}

func TestGitOpsDeliveryRequiresRepository(t *testing.T) {
	syncBinding := binding("gitops")
	syncBinding.Provider = "deployments"
	syncBinding.ResourceRef.APIVersion = "deployments.faros.sh/v1alpha1"
	syncBinding.ResourceRef.Resource = "repositorysyncs"
	project := &aiv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "demo"},
		Spec: aiv1alpha1.ProjectSpec{
			Delivery: controllerTestDelivery(aiv1alpha1.ProjectDeliveryModeDirect, aiv1alpha1.ProjectDeliveryModeGitOps),
			Environments: []aiv1alpha1.ProjectEnvironmentSpec{{
				Name: "configuration", Bindings: []aiv1alpha1.ProjectProviderBindingSpec{syncBinding},
			}},
		},
	}
	if err := validateDeliveryBindings(project, providerBindings(project)); err == nil || !strings.Contains(err.Error(), "has no repository") {
		t.Fatalf("validation error = %v, want missing repository", err)
	}
}

func TestGitOpsPreviewPolicyDoesNotMutateReadOnlyBinding(t *testing.T) {
	project := &aiv1alpha1.Project{Spec: aiv1alpha1.ProjectSpec{
		Delivery: controllerTestDelivery(aiv1alpha1.ProjectDeliveryModeGitOps, aiv1alpha1.ProjectDeliveryModeDirect),
		Sharing:  aiv1alpha1.ProjectSharingSpec{Preview: aiv1alpha1.ProjectPreviewSharingPolicy{Mode: aiv1alpha1.ProjectSharingModePublic}},
		Environments: []aiv1alpha1.ProjectEnvironmentSpec{{
			Name: projectDevelopmentEnvironmentName,
			Bindings: []aiv1alpha1.ProjectProviderBindingSpec{{
				Name: projectDevelopmentBindingName, Kind: aiv1alpha1.ProjectBindingKindProviderReference,
				Values: runtime.RawExtension{Raw: []byte(`{"access":"private"}`)},
			}},
		}},
	}}
	changed, err := reconcileDevelopmentPreviewPolicy(project)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("GitOps preview reconciliation reported a binding mutation")
	}
	if got := string(project.Spec.Environments[0].Bindings[0].Values.Raw); got != `{"access":"private"}` {
		t.Fatalf("read-only binding values = %s, want unchanged", got)
	}
}

func TestReconcileDevelopmentPreviewPolicy(t *testing.T) {
	projectWith := func(mode aiv1alpha1.ProjectSharingMode, values string, observedURL string) *aiv1alpha1.Project {
		p := &aiv1alpha1.Project{
			Spec: aiv1alpha1.ProjectSpec{
				Sharing: aiv1alpha1.ProjectSharingSpec{Preview: aiv1alpha1.ProjectPreviewSharingPolicy{Mode: mode}},
				Environments: []aiv1alpha1.ProjectEnvironmentSpec{{
					Name:     projectDevelopmentEnvironmentName,
					Bindings: []aiv1alpha1.ProjectProviderBindingSpec{actionsDevelopmentBinding(values)},
				}},
			},
		}
		if observedURL != "" {
			p.Status.Environments = []aiv1alpha1.ProjectEnvironmentStatus{{
				Name:     projectDevelopmentEnvironmentName,
				Bindings: []aiv1alpha1.ProjectProviderBindingStatus{{Name: projectDevelopmentBindingName, URL: observedURL}},
			}}
		}
		return p
	}
	for _, tc := range []struct {
		name       string
		project    *aiv1alpha1.Project
		wantMode   aiv1alpha1.ProjectSharingMode
		wantAccess string
		wantFound  bool
		wantChange bool
	}{
		{name: "new private binding", project: projectWith(aiv1alpha1.ProjectSharingModePrivate, `{"access":"public"}`, ""), wantMode: aiv1alpha1.ProjectSharingModePrivate, wantAccess: "private", wantFound: true, wantChange: true},
		{name: "explicit public", project: projectWith(aiv1alpha1.ProjectSharingModePublic, `{"access":"private"}`, ""), wantMode: aiv1alpha1.ProjectSharingModePublic, wantAccess: "public", wantFound: true, wantChange: true},
		{name: "legacy missing with URL", project: projectWith("", `{}`, "https://preview.example"), wantMode: aiv1alpha1.ProjectSharingModePrivate, wantAccess: "private", wantFound: true, wantChange: true},
		{name: "legacy shared", project: projectWith(aiv1alpha1.ProjectSharingModeShared, `{"access":"private"}`, ""), wantMode: aiv1alpha1.ProjectSharingModePrivate, wantAccess: "private", wantFound: true, wantChange: true},
		{name: "internal template", project: projectWith("", `{}`, ""), wantMode: aiv1alpha1.ProjectSharingModePrivate, wantFound: false, wantChange: true},
		{name: "already converged", project: projectWith(aiv1alpha1.ProjectSharingModePrivate, `{"access":"private"}`, ""), wantMode: aiv1alpha1.ProjectSharingModePrivate, wantAccess: "private", wantFound: true, wantChange: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed, err := reconcileDevelopmentPreviewPolicy(tc.project)
			if err != nil {
				t.Fatal(err)
			}
			if changed != tc.wantChange {
				t.Fatalf("changed = %t, want %t", changed, tc.wantChange)
			}
			if got := tc.project.Spec.Sharing.Preview.Mode; got != tc.wantMode {
				t.Fatalf("mode = %q, want %q", got, tc.wantMode)
			}
			values, err := bindings.Values(tc.project.Spec.Environments[0].Bindings[0])
			if err != nil {
				t.Fatal(err)
			}
			got, found := values[bindings.PreviewAccessField]
			if found != tc.wantFound || (found && got != tc.wantAccess) {
				t.Fatalf("access = %v, found=%t; want %q, found=%t", got, found, tc.wantAccess, tc.wantFound)
			}
		})
	}
}

func TestEqualSpecAndMetaDetectsDrift(t *testing.T) {
	base := func() *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "infrastructure.faros.sh/v1alpha1",
			"kind":       "Instance",
			"metadata": map[string]any{
				"name":   "demo-dev",
				"labels": map[string]any{"app-studio.faros.sh/project": "demo"},
			},
			"spec": map[string]any{"template": "application", "values": map[string]any{"webImage": "x"}},
			// Instance-owned fields must not count as drift.
			"status": map[string]any{"phase": "Ready"},
		}}
	}

	same := base()
	if !equalSpecAndMeta(base(), same) {
		t.Fatal("identical objects reported as drifted")
	}

	specDrift := base()
	specDrift.Object["spec"] = map[string]any{"template": "application", "values": map[string]any{"webImage": "y"}}
	if equalSpecAndMeta(base(), specDrift) {
		t.Fatal("spec drift not detected")
	}

	labelDrift := base()
	labelDrift.SetLabels(map[string]string{"other": "label"})
	if equalSpecAndMeta(base(), labelDrift) {
		t.Fatal("label drift not detected")
	}

	statusOnly := base()
	statusOnly.Object["status"] = map[string]any{"phase": "Failed"}
	if !equalSpecAndMeta(base(), statusOnly) {
		t.Fatal("status-only difference must not count as drift")
	}
}

func TestEnsureInstanceDeepMergesComputedFieldsAndRetriesConflict(t *testing.T) {
	p := &aiv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", UID: "project-uid"},
		Spec: aiv1alpha1.ProjectSpec{
			Template: &aiv1alpha1.ProjectTemplateSpec{Name: "application"},
		},
	}
	b := binding(projectDevelopmentBindingName)
	b.ResourceRef.Name = "demo-dev"
	b.Values = runtime.RawExtension{Raw: []byte(`{
		"name":"demo-dev",
		"expose":{"hostnamePrefix":"desired"},
		"nested":{"input":"new"}
	}`)}
	instance := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": b.ResourceRef.APIVersion,
		"kind":       b.ResourceRef.Kind,
		"metadata": map[string]any{
			"name":   "demo-dev",
			"labels": map[string]any{bindings.ProjectLabel: "demo"},
		},
		"spec": map[string]any{
			"template": "application",
			"values": map[string]any{
				"name": "demo-dev",
				"expose": map[string]any{
					"hostnamePrefix": "old",
					"fqdn":           "provider-computed.example",
					"providerField":  "preserve",
				},
				"credentialsSecretName": "demo-dev-credentials",
				"nested": map[string]any{
					"input":    "old",
					"computed": "preserve-nested",
				},
				bindings.ActionsExchangeURLField: "https://stale.example/exchange",
				"farosActionsFutureField":        "stale",
			},
		},
	}}
	instance.SetGroupVersionKind(instance.GroupVersionKind())
	var updates int
	c := fake.NewClientBuilder().WithObjects(instance).WithInterceptorFuncs(interceptor.Funcs{
		Update: func(ctx context.Context, underlying client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			updates++
			if updates == 1 {
				latest := &unstructured.Unstructured{}
				latest.SetGroupVersionKind(instance.GroupVersionKind())
				if err := underlying.Get(ctx, client.ObjectKey{Name: "demo-dev"}, latest); err != nil {
					return err
				}
				values, _, err := unstructured.NestedMap(latest.Object, "spec", "values")
				if err != nil {
					return err
				}
				values["expose"].(map[string]any)["fqdn"] = "fresh-provider-computed.example"
				if err := unstructured.SetNestedMap(latest.Object, values, "spec", "values"); err != nil {
					return err
				}
				if err := underlying.Update(ctx, latest); err != nil {
					return err
				}
				return apierrors.NewConflict(schemaGroupResourceForTest(), "demo-dev", fmt.Errorf("the object has been modified"))
			}
			return underlying.Update(ctx, obj)
		},
	}).Build()

	got, err := (&Reconciler{}).ensureInstance(context.Background(), c, p, b)
	if err != nil {
		t.Fatalf("ensureInstance after conflict: %v", err)
	}
	if got == nil {
		t.Fatal("ensureInstance returned nil object")
	}
	if updates != 2 {
		t.Fatalf("Update calls = %d, want bounded fresh retry (2)", updates)
	}

	stored := &unstructured.Unstructured{}
	stored.SetGroupVersionKind(instance.GroupVersionKind())
	if err := c.Get(context.Background(), client.ObjectKey{Name: "demo-dev"}, stored); err != nil {
		t.Fatalf("get converged instance: %v", err)
	}
	spec, _, err := unstructured.NestedMap(stored.Object, "spec", "values")
	if err != nil {
		t.Fatalf("get spec.values: %v", err)
	}
	if tmplName, _, _ := unstructured.NestedString(stored.Object, "spec", "template"); tmplName != "application" {
		t.Fatalf("spec.template = %q, want application", tmplName)
	}
	expose := spec["expose"].(map[string]any)
	if expose["hostnamePrefix"] != "desired" || expose["fqdn"] != "fresh-provider-computed.example" || expose["providerField"] != "preserve" {
		t.Fatalf("merged expose = %#v, want desired input + fresh/unknown provider fields", expose)
	}
	if spec["credentialsSecretName"] != "demo-dev-credentials" || spec["nested"].(map[string]any)["computed"] != "preserve-nested" {
		t.Fatalf("computed fields were lost: %#v", spec)
	}
	for key := range spec {
		if strings.HasPrefix(key, bindings.ActionsFieldPrefix) {
			t.Fatalf("stale Provider Actions field %q survived: %#v", key, spec[key])
		}
	}

	// A converged retry is a no-op. An explicit desired update still changes
	// only its requested field and leaves provider-computed fields intact.
	if _, err := (&Reconciler{}).ensureInstance(context.Background(), c, p, b); err != nil {
		t.Fatalf("second ensureInstance: %v", err)
	}
	if updates != 2 {
		t.Fatalf("converged ensure made an Update call: %d", updates)
	}
	b.Values = runtime.RawExtension{Raw: []byte(`{"name":"demo-dev","expose":{"hostnamePrefix":"final"},"nested":{"input":"explicit"}}`)}
	if _, err := (&Reconciler{}).ensureInstance(context.Background(), c, p, b); err != nil {
		t.Fatalf("explicit desired update: %v", err)
	}
	if updates != 3 {
		t.Fatalf("explicit desired update calls = %d, want 3", updates)
	}
}

func TestEnsureRepositorySyncPreservesNativeSpecAcrossReconcile(t *testing.T) {
	p := &aiv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", UID: "project-uid"},
		Spec: aiv1alpha1.ProjectSpec{
			Template: &aiv1alpha1.ProjectTemplateSpec{Name: "application"},
		},
	}
	b := binding("gitops")
	b.Provider = "deployments"
	b.ResourceRef = &aiv1alpha1.ProjectProviderResourceReference{
		Name:       "demo-gitops",
		APIVersion: "deployments.faros.sh/v1alpha1",
		Kind:       "RepositorySync",
		Resource:   "repositorysyncs",
	}
	b.Values = runtime.RawExtension{Raw: []byte(`{"repositoryRef":"demo-repo","ref":"main","path":".faros","prune":true,"intervalSeconds":30}`)}
	instance := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": b.ResourceRef.APIVersion,
		"kind":       b.ResourceRef.Kind,
		"metadata": map[string]any{
			"name":   "demo-gitops",
			"labels": map[string]any{bindings.ProjectLabel: "demo"},
		},
		"spec": map[string]any{
			"repositoryRef":    "demo-repo",
			"ref":              "old",
			"path":             ".faros",
			"prune":            true,
			"intervalSeconds":  float64(10),
			"providerComputed": "preserve",
		},
	}}
	instance.SetGroupVersionKind(instance.GroupVersionKind())
	updates := 0
	c := fake.NewClientBuilder().WithObjects(instance).WithInterceptorFuncs(interceptor.Funcs{
		Update: func(ctx context.Context, underlying client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			updates++
			return underlying.Update(ctx, obj)
		},
	}).Build()

	if _, err := (&Reconciler{}).ensureInstance(context.Background(), c, p, b); err != nil {
		t.Fatalf("first RepositorySync reconcile: %v", err)
	}
	stored := &unstructured.Unstructured{}
	stored.SetGroupVersionKind(instance.GroupVersionKind())
	if err := c.Get(context.Background(), client.ObjectKey{Name: "demo-gitops"}, stored); err != nil {
		t.Fatalf("get first RepositorySync result: %v", err)
	}
	assertNativeRepositorySyncSpec(t, stored, "main", float64(30))

	if _, err := (&Reconciler{}).ensureInstance(context.Background(), c, p, b); err != nil {
		t.Fatalf("second RepositorySync reconcile: %v", err)
	}
	if updates != 1 {
		t.Fatalf("RepositorySync Update calls = %d, want one drift convergence and no second-reconcile shape churn; object=%#v labels=%#v owners=%#v", updates, stored.Object, stored.GetLabels(), stored.GetOwnerReferences())
	}
	if err := c.Get(context.Background(), client.ObjectKey{Name: "demo-gitops"}, stored); err != nil {
		t.Fatalf("get second RepositorySync result: %v", err)
	}
	assertNativeRepositorySyncSpec(t, stored, "main", float64(30))
}

func TestFinalizeDoesNotRequireOptionalRepositorySyncAPI(t *testing.T) {
	syncBinding := binding("gitops")
	syncBinding.Provider = "deployments"
	syncBinding.ResourceRef = &aiv1alpha1.ProjectProviderResourceReference{
		Name:       "demo-gitops",
		APIVersion: "deployments.faros.sh/v1alpha1",
		Kind:       "RepositorySync",
		Resource:   "repositorysyncs",
	}
	syncBinding.Values = runtime.RawExtension{Raw: []byte(`{"repositoryRef":"demo","ref":"main","path":".faros"}`)}
	p := &aiv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Finalizers: []string{finalizer}},
		Spec: aiv1alpha1.ProjectSpec{
			Template:   &aiv1alpha1.ProjectTemplateSpec{Name: "application"},
			Repository: &aiv1alpha1.ProjectRepositoryBinding{RepositoryRef: "demo"},
			Delivery:   controllerTestDelivery(aiv1alpha1.ProjectDeliveryModeDirect, aiv1alpha1.ProjectDeliveryModeGitOps),
			Environments: []aiv1alpha1.ProjectEnvironmentSpec{{
				Name: "configuration", Bindings: []aiv1alpha1.ProjectProviderBindingSpec{syncBinding},
			}},
		},
	}
	scheme := runtime.NewScheme()
	if err := aiv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(p).Build()
	stored := &aiv1alpha1.Project{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: p.Name}, stored); err != nil {
		t.Fatal(err)
	}
	if _, err := (&Reconciler{}).finalize(context.Background(), c, stored); err != nil {
		t.Fatalf("finalize with unavailable optional Deployments API: %v", err)
	}
	updated := &aiv1alpha1.Project{}
	if err := c.Get(context.Background(), client.ObjectKey{Name: p.Name}, updated); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(updated.Finalizers, finalizer) {
		t.Fatalf("optional RepositorySync stranded finalizer: %v", updated.Finalizers)
	}
}

func assertNativeRepositorySyncSpec(t *testing.T, obj *unstructured.Unstructured, wantRef string, wantInterval float64) {
	t.Helper()
	spec, ok, err := unstructured.NestedMap(obj.Object, "spec")
	if err != nil || !ok {
		t.Fatalf("RepositorySync spec = %#v, err = %v", spec, err)
	}
	if spec["repositoryRef"] != "demo-repo" || spec["ref"] != wantRef || spec["path"] != ".faros" || spec["prune"] != true {
		t.Fatalf("RepositorySync spec = %#v, want flat desired fields", spec)
	}
	if got, ok := repositorySyncNumber(spec["intervalSeconds"]); !ok || got != wantInterval {
		t.Fatalf("RepositorySync spec.intervalSeconds = %#v, want %v", spec["intervalSeconds"], wantInterval)
	}
	if spec["providerComputed"] != "preserve" {
		t.Fatalf("provider-computed field = %#v, want preserve", spec["providerComputed"])
	}
	if _, found := spec["template"]; found {
		t.Fatalf("RepositorySync spec unexpectedly contains template wrapper: %#v", spec)
	}
	if _, found := spec["values"]; found {
		t.Fatalf("RepositorySync spec unexpectedly contains values wrapper: %#v", spec)
	}
}

func repositorySyncNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	default:
		return 0, false
	}
}

// Keep the conflict test independent of provider-specific API discovery.
func schemaGroupResourceForTest() schema.GroupResource {
	return schema.GroupResource{Group: "infrastructure.faros.sh", Resource: "instances"}
}

func TestResolveLogicalClusterPathFromAppStudioBinding(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cluster  string
		path     string
		multiple bool
		want     string
		wantErr  string
	}{
		{name: "success", cluster: "cluster-a", path: "root:faros:tenants:org:workspace", want: "root:faros:tenants:org:workspace"},
		{name: "cluster mismatch", cluster: "cluster-b", path: "root:faros:tenants:org:workspace", wantErr: "does not match request cluster"},
		{name: "missing path", cluster: "cluster-a", wantErr: "no kcp.io/path annotation"},
		{name: "multiple bindings", cluster: "cluster-a", path: "root:faros:tenants:org:workspace", multiple: true, wantErr: "multiple APIBindings"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newBinding := func(name string) *apisv1alpha2.APIBinding {
				annotations := map[string]string{"kcp.io/cluster": tc.cluster}
				if tc.path != "" {
					annotations["kcp.io/path"] = tc.path
				}
				return &apisv1alpha2.APIBinding{
					ObjectMeta: metav1.ObjectMeta{Name: name, Annotations: annotations},
					Spec: apisv1alpha2.APIBindingSpec{Reference: apisv1alpha2.BindingReference{
						Export: &apisv1alpha2.ExportBindingReference{Name: appStudioAPIExportName, Path: appStudioAPIExportPath},
					}},
				}
			}
			scheme := runtime.NewScheme()
			if err := apisv1alpha2.AddToScheme(scheme); err != nil {
				t.Fatalf("add APIBinding scheme: %v", err)
			}
			objects := []client.Object{newBinding("app-studio")}
			if tc.multiple {
				objects = append(objects, newBinding("app-studio-duplicate"))
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()

			path, err := resolveLogicalClusterPath(t.Context(), c, "cluster-a")
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveLogicalClusterPath: %v", err)
			}
			if path != tc.want {
				t.Fatalf("path = %q, want %q", path, tc.want)
			}
		})
	}
}

func TestOverlayDevelopmentBindingUsesAuthoritativeConfigAndClearsRevokedTransport(t *testing.T) {
	p := &aiv1alpha1.Project{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", UID: "uid-1"},
		Spec: aiv1alpha1.ProjectSpec{Environments: []aiv1alpha1.ProjectEnvironmentSpec{{
			Bindings: []aiv1alpha1.ProjectProviderBindingSpec{{
				Kind:           aiv1alpha1.ProjectBindingKindProviderReference,
				AllowedActions: []aiv1alpha1.ProjectProviderActionSpec{{Name: "query", Revoked: false}},
			}},
		}}},
	}
	binding := actionsDevelopmentBinding(`{
		"name":"demo-dev",
		"farosActionsExchangeURL":"https://stale.example/api/provider-actions/workload/exchange",
		"farosActionsBaseURL":"https://stale.example/services/providers/app-studio",
		"farosActionsCABundle":"stale-ca",
		"farosActionsTenantPath":"stale-tenant",
		"farosActionsProject":"stale-project"
	}`)
	r := &Reconciler{Actions: bindings.ActionsRuntimeConfig{
		ExternalURL: "https://actions.example",
		CABundle:    "authoritative-ca",
	}}

	updated, err := r.overlayDevelopmentBinding(p, binding, "root:faros:tenants:authoritative-org:authoritative-workspace")
	if err != nil {
		t.Fatalf("active overlay: %v", err)
	}
	var values map[string]any
	if err := json.Unmarshal(updated.Values.Raw, &values); err != nil {
		t.Fatalf("decode active overlay: %v", err)
	}
	for key, want := range map[string]string{
		bindings.ActionsExchangeURLField: "https://actions.example/api/provider-actions/workload/exchange",
		bindings.ActionsBaseURLField:     "https://actions.example/services/providers/app-studio",
		bindings.ActionsCABundleField:    "authoritative-ca",
		bindings.ActionsTenantPathField:  "root:faros:tenants:authoritative-org:authoritative-workspace",
		bindings.ActionsOrgField:         "authoritative-org",
		bindings.ActionsWorkspaceField:   "authoritative-workspace",
		bindings.ActionsProjectField:     "demo",
		bindings.ActionsProjectUIDField:  "uid-1",
		bindings.ActionsEnvironmentField: projectDevelopmentEnvironmentName,
		bindings.ActionsInstanceField:    "demo-dev",
	} {
		if values[key] != want {
			t.Errorf("active %s = %v, want %q", key, values[key], want)
		}
	}

	p.Spec.Environments[0].Bindings[0].AllowedActions[0].Revoked = true
	if bindings.HasActiveProviderActionGrant(p) {
		t.Fatal("revoked test grant is still active")
	}
	updated, err = r.overlayDevelopmentBinding(p, binding, "root:faros:tenants:authoritative-org:authoritative-workspace")
	if err != nil {
		t.Fatalf("revoked overlay: %v", err)
	}
	values = nil
	if err := json.Unmarshal(updated.Values.Raw, &values); err != nil {
		t.Fatalf("decode revoked overlay: %v", err)
	}
	for _, key := range []string{bindings.ActionsExchangeURLField, bindings.ActionsBaseURLField, bindings.ActionsCABundleField} {
		if _, found := values[key]; found {
			t.Errorf("revoked transport field %s survived: %v", key, values[key])
		}
	}
	if values[bindings.ActionsTenantPathField] != "root:faros:tenants:authoritative-org:authoritative-workspace" {
		t.Fatalf("revoked tenant path = %v, want authoritative identity", values[bindings.ActionsTenantPathField])
	}
}

func TestActionsTenantPathRejectsConflictingProjectAnnotations(t *testing.T) {
	for _, tc := range []struct {
		name        string
		annotations map[string]string
		want        string
	}{
		{
			name: "organization",
			annotations: map[string]string{
				bindings.OrgUUIDAnnotation:       "stale-org",
				bindings.WorkspaceUUIDAnnotation: "workspace",
			},
			want: "organization annotation",
		},
		{
			name: "workspace",
			annotations: map[string]string{
				bindings.OrgUUIDAnnotation:       "org",
				bindings.WorkspaceUUIDAnnotation: "stale-workspace",
			},
			want: "workspace annotation",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &aiv1alpha1.Project{
				ObjectMeta: metav1.ObjectMeta{Name: "demo", Annotations: tc.annotations},
				Spec: aiv1alpha1.ProjectSpec{Environments: []aiv1alpha1.ProjectEnvironmentSpec{{
					Name:     projectDevelopmentEnvironmentName,
					Bindings: []aiv1alpha1.ProjectProviderBindingSpec{actionsDevelopmentBinding(`{}`)},
				}}},
			}
			r := &Reconciler{ResolveTenantPath: func(context.Context, client.Client, string) (string, error) {
				return "root:faros:tenants:org:workspace", nil
			}}
			_, err := r.actionsTenantPath(context.Background(), nil, p, providerBindings(p), "cluster-a")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("actionsTenantPath error = %v, want substring %q", err, tc.want)
			}
		})
	}
}
