/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package project

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
)

func projectConnectionTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(projectConnectionGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(projectConnectionListGVK, &unstructured.UnstructuredList{})
	scheme.AddKnownTypeWithName(projectDevelopmentServiceGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(projectDevelopmentServiceListGVK, &unstructured.UnstructuredList{})
	return scheme
}

func projectConnectionTestFixture(t *testing.T) (*aiv1alpha1.Project, *unstructured.Unstructured, *unstructured.Unstructured) {
	t.Helper()
	p := &aiv1alpha1.Project{ObjectMeta: metav1.ObjectMeta{Name: "demo", UID: types.UID("project-uid")}}
	binding := aiv1alpha1.ProjectProviderBindingSpec{
		Name: "dep-db", Provider: "infrastructure", Kind: aiv1alpha1.ProjectBindingKindProviderResource,
		ResourceRef: &aiv1alpha1.ProjectProviderResourceReference{APIVersion: "infrastructure.faros.sh/v1alpha1", Kind: "Instance", Resource: "instances", Name: "database-instance"},
	}
	p.Spec.Environments = []aiv1alpha1.ProjectEnvironmentSpec{{
		Name: "development", Mode: aiv1alpha1.ProjectEnvironmentModeLive, Bindings: []aiv1alpha1.ProjectProviderBindingSpec{binding},
		Connections: []aiv1alpha1.ProjectEnvironmentConnectionSpec{{
			Name: "database", SourceRef: aiv1alpha1.ProjectConnectionEndpointReference{Kind: aiv1alpha1.ProjectConnectionReferenceBinding, Name: "dep-db"},
			TargetRef:       aiv1alpha1.ProjectConnectionEndpointReference{Kind: aiv1alpha1.ProjectConnectionReferenceDevelopmentService, Name: "web"},
			SourceInterface: "default", TargetInterface: "postgresql",
		}},
	}}
	source := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "infrastructure.faros.sh/v1alpha1", "kind": "Instance", "metadata": map[string]any{"name": "database-instance", "uid": "source-uid"}}}
	service := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": projectDevelopmentServiceGVK.GroupVersion().String(), "kind": projectDevelopmentServiceGVK.Kind,
		"metadata": map[string]any{"name": "devsvc-demo-web", "uid": "service-uid", "labels": map[string]any{
			projectConnectionProjectLabel: p.Name, projectConnectionProjectUIDLabel: string(p.UID), projectDevelopmentServiceLogicalNameLabel: "web",
		}},
		"spec": map[string]any{},
	}}
	return p, source, service
}

func TestReconcileProjectConnectionsPinsExactUIDsAndDerivesServiceRefs(t *testing.T) {
	p, source, service := projectConnectionTestFixture(t)
	c := fake.NewClientBuilder().WithScheme(projectConnectionTestScheme(t)).WithObjects(service).Build()
	statuses, retry, err := reconcileProjectConnections(context.Background(), c, p, map[string]map[string]*unstructured.Unstructured{"development": {"dep-db": source}})
	if err != nil {
		t.Fatal(err)
	}
	if !retry || len(statuses["development"]) != 1 || statuses["development"][0].Reason != "Creating" {
		t.Fatalf("status=%+v retry=%v, want Creating retry", statuses, retry)
	}
	name := projectConnectionPhysicalName(p, "development", "database")
	connection := &unstructured.Unstructured{}
	connection.SetGroupVersionKind(projectConnectionGVK)
	if err := c.Get(context.Background(), client.ObjectKey{Name: name}, connection); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := unstructured.NestedString(connection.Object, "spec", "source", "instanceRef", "uid"); got != "source-uid" {
		t.Fatalf("source UID=%q", got)
	}
	if got, _, _ := unstructured.NestedString(connection.Object, "spec", "target", "uid"); got != "service-uid" {
		t.Fatalf("target UID=%q", got)
	}
	updatedService := &unstructured.Unstructured{}
	updatedService.SetGroupVersionKind(projectDevelopmentServiceGVK)
	if err := c.Get(context.Background(), client.ObjectKey{Name: service.GetName()}, updatedService); err != nil {
		t.Fatal(err)
	}
	refs, _, _ := unstructured.NestedStringSlice(updatedService.Object, "spec", "connectionRefs")
	if len(refs) != 1 || refs[0] != name {
		t.Fatalf("connectionRefs=%v, want [%s]", refs, name)
	}
}

func TestReconcileProjectConnectionsDeletesImmutableDriftWaitsThenRecreates(t *testing.T) {
	p, source, service := projectConnectionTestFixture(t)
	c := fake.NewClientBuilder().WithScheme(projectConnectionTestScheme(t)).WithObjects(service).Build()
	instances := map[string]map[string]*unstructured.Unstructured{"development": {"dep-db": source}}
	if _, _, err := reconcileProjectConnections(context.Background(), c, p, instances); err != nil {
		t.Fatal(err)
	}
	name := projectConnectionPhysicalName(p, "development", "database")
	connection := &unstructured.Unstructured{}
	connection.SetGroupVersionKind(projectConnectionGVK)
	if err := c.Get(context.Background(), client.ObjectKey{Name: name}, connection); err != nil {
		t.Fatal(err)
	}
	connection.SetFinalizers([]string{"infrastructure.faros.sh/connection"})
	if err := c.Update(context.Background(), connection); err != nil {
		t.Fatal(err)
	}
	p.Spec.Environments[0].Connections[0].Mappings = []aiv1alpha1.ProjectConnectionMappingSpec{{SourceKey: "uri", TargetKey: "DATABASE_URL"}}
	statuses, retry, err := reconcileProjectConnections(context.Background(), c, p, instances)
	if err != nil {
		t.Fatal(err)
	}
	if !retry || statuses["development"][0].Reason != "Replacing" {
		t.Fatalf("status=%+v retry=%v", statuses, retry)
	}
	deleting := &unstructured.Unstructured{}
	deleting.SetGroupVersionKind(projectConnectionGVK)
	if err := c.Get(context.Background(), client.ObjectKey{Name: name}, deleting); err != nil {
		t.Fatal(err)
	}
	if deleting.GetDeletionTimestamp().IsZero() {
		t.Fatal("immutable Connection was updated instead of deleted")
	}
	statuses, _, err = reconcileProjectConnections(context.Background(), c, p, instances)
	if err != nil || statuses["development"][0].Reason != "Replacing" {
		t.Fatalf("waiting status=%+v err=%v", statuses, err)
	}
	deleting.SetFinalizers(nil)
	if err := c.Update(context.Background(), deleting); err != nil && !apierrors.IsNotFound(err) {
		t.Fatal(err)
	}
	_ = c.Delete(context.Background(), deleting)
	if _, _, err := reconcileProjectConnections(context.Background(), c, p, instances); err != nil {
		t.Fatal(err)
	}
	recreated := &unstructured.Unstructured{}
	recreated.SetGroupVersionKind(projectConnectionGVK)
	if err := c.Get(context.Background(), client.ObjectKey{Name: name}, recreated); err != nil {
		t.Fatal(err)
	}
	mappings, _, _ := unstructured.NestedSlice(recreated.Object, "spec", "mappings")
	if len(mappings) != 1 {
		t.Fatalf("recreated mappings=%v", mappings)
	}
}

func TestReconcileProjectConnectionsCleansRemovedIntentAndServiceRefs(t *testing.T) {
	p, source, service := projectConnectionTestFixture(t)
	c := fake.NewClientBuilder().WithScheme(projectConnectionTestScheme(t)).WithObjects(service).Build()
	instances := map[string]map[string]*unstructured.Unstructured{"development": {"dep-db": source}}
	if _, _, err := reconcileProjectConnections(context.Background(), c, p, instances); err != nil {
		t.Fatal(err)
	}
	name := projectConnectionPhysicalName(p, "development", "database")
	p.Spec.Environments[0].Connections = nil
	if _, retry, err := reconcileProjectConnections(context.Background(), c, p, instances); err != nil || !retry {
		t.Fatalf("cleanup retry=%v err=%v", retry, err)
	}
	connection := &unstructured.Unstructured{}
	connection.SetGroupVersionKind(projectConnectionGVK)
	if err := c.Get(context.Background(), client.ObjectKey{Name: name}, connection); !apierrors.IsNotFound(err) {
		t.Fatalf("stale Connection get err=%v, want NotFound", err)
	}
	updatedService := &unstructured.Unstructured{}
	updatedService.SetGroupVersionKind(projectDevelopmentServiceGVK)
	if err := c.Get(context.Background(), client.ObjectKey{Name: service.GetName()}, updatedService); err != nil {
		t.Fatal(err)
	}
	refs, found, _ := unstructured.NestedStringSlice(updatedService.Object, "spec", "connectionRefs")
	if found || len(refs) != 0 {
		t.Fatalf("removed connectionRefs still present: %v", refs)
	}
}

func TestReconcileProjectConnectionsKeepsMissingTargetPending(t *testing.T) {
	p, source, _ := projectConnectionTestFixture(t)
	c := fake.NewClientBuilder().WithScheme(projectConnectionTestScheme(t)).Build()
	statuses, retry, err := reconcileProjectConnections(context.Background(), c, p, map[string]map[string]*unstructured.Unstructured{"development": {"dep-db": source}})
	if err != nil {
		t.Fatal(err)
	}
	if !retry || statuses["development"][0].Phase != "Pending" || statuses["development"][0].Reason != "TargetPending" {
		t.Fatalf("status=%+v retry=%v", statuses, retry)
	}
}

func TestProjectConnectionStatusProjectionIsSecretFree(t *testing.T) {
	obj := &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{
		"revision":         "rev-1",
		"managedSecretRef": map[string]any{"name": "never-return-this", "namespace": "runtime", "targetRuntimeIdentity": "sandbox/private"},
		"conditions":       []any{map[string]any{"type": "Ready", "status": "True", "reason": "Ready", "message": "credentials are synchronized"}},
	}}}
	status := projectConnectionStatus("database", obj)
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if status.Phase != "Ready" || status.Revision != "rev-1" || strings.Contains(text, "never-return-this") || strings.Contains(text, "runtime") || strings.Contains(text, "sandbox/private") {
		t.Fatalf("unsafe status projection: %s", text)
	}
}
