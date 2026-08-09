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
	"strings"
	"testing"

	apisv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	"github.com/faroshq/provider-app-studio/bindings"
)

func binding(name string) aiv1alpha1.ProjectProviderBindingSpec {
	return aiv1alpha1.ProjectProviderBindingSpec{
		Name:     name,
		Provider: "infrastructure",
		Kind:     aiv1alpha1.ProjectBindingKindProviderResource,
		ResourceRef: &aiv1alpha1.ProjectProviderResourceReference{
			APIVersion: "infrastructure.kedge.faros.sh/v1alpha1",
			Kind:       "Application",
			Resource:   "applications",
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

func TestEqualSpecAndMetaDetectsDrift(t *testing.T) {
	base := func() *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "infrastructure.kedge.faros.sh/v1alpha1",
			"kind":       "Application",
			"metadata": map[string]any{
				"name":   "demo-dev",
				"labels": map[string]any{"app-studio.kedge.faros.sh/project": "demo"},
			},
			"spec": map[string]any{"webImage": "x"},
			// Instance-owned fields must not count as drift.
			"status": map[string]any{"phase": "Ready"},
		}}
	}

	same := base()
	if !equalSpecAndMeta(base(), same) {
		t.Fatal("identical objects reported as drifted")
	}

	specDrift := base()
	specDrift.Object["spec"] = map[string]any{"webImage": "y"}
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

func TestResolveLogicalClusterPathFromAppStudioBinding(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cluster  string
		path     string
		multiple bool
		want     string
		wantErr  string
	}{
		{name: "success", cluster: "cluster-a", path: "root:kedge:tenants:org:workspace", want: "root:kedge:tenants:org:workspace"},
		{name: "cluster mismatch", cluster: "cluster-b", path: "root:kedge:tenants:org:workspace", wantErr: "does not match request cluster"},
		{name: "missing path", cluster: "cluster-a", wantErr: "no kcp.io/path annotation"},
		{name: "multiple bindings", cluster: "cluster-a", path: "root:kedge:tenants:org:workspace", multiple: true, wantErr: "multiple APIBindings"},
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
		"kedgeActionsExchangeURL":"https://stale.example/api/provider-actions/workload/exchange",
		"kedgeActionsBaseURL":"https://stale.example/services/providers/app-studio",
		"kedgeActionsCABundle":"stale-ca",
		"kedgeActionsTenantPath":"stale-tenant",
		"kedgeActionsProject":"stale-project"
	}`)
	r := &Reconciler{Actions: bindings.ActionsRuntimeConfig{
		ExternalURL: "https://actions.example",
		CABundle:    "authoritative-ca",
	}}

	updated, err := r.overlayDevelopmentBinding(p, binding, "root:kedge:tenants:authoritative-org:authoritative-workspace")
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
		bindings.ActionsTenantPathField:  "root:kedge:tenants:authoritative-org:authoritative-workspace",
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
	updated, err = r.overlayDevelopmentBinding(p, binding, "root:kedge:tenants:authoritative-org:authoritative-workspace")
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
	if values[bindings.ActionsTenantPathField] != "root:kedge:tenants:authoritative-org:authoritative-workspace" {
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
				return "root:kedge:tenants:org:workspace", nil
			}}
			_, err := r.actionsTenantPath(context.Background(), nil, p, providerBindings(p), "cluster-a")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("actionsTenantPath error = %v, want substring %q", err, tc.want)
			}
		})
	}
}
