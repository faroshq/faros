// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package instance

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	infrav1alpha1 "github.com/faroshq/provider-infrastructure/apis/v1alpha1"
)

func TestValidateConnectionMappingsRejectsInterfaceAndKeyConfusion(t *testing.T) {
	provided := infrav1alpha1.TemplateProvidedConnection{Name: "default", Type: "postgresql", Keys: []string{"uri"}}
	consumed := infrav1alpha1.TemplateConsumedConnection{Name: "postgresql", Type: "postgresql", Mappings: []infrav1alpha1.TemplateConnectionMapping{{SourceKey: "uri", TargetKey: "DATABASE_URL"}}}
	if got, err := validateConnectionMappings(nil, provided, consumed); err != nil || len(got) != 1 {
		t.Fatalf("default mappings = %#v, %v", got, err)
	}
	for _, mappings := range [][]infrav1alpha1.ConnectionMapping{
		{{SourceKey: "password", TargetKey: "DATABASE_URL"}},
		{{SourceKey: "uri", TargetKey: "REDIS_URL"}},
		{{SourceKey: "uri", TargetKey: "DATABASE_URL"}, {SourceKey: "uri", TargetKey: "DATABASE_URL"}},
	} {
		if _, err := validateConnectionMappings(mappings, provided, consumed); err == nil {
			t.Fatalf("validateConnectionMappings(%#v) succeeded", mappings)
		}
	}
}

func TestResolveConnectionRejectsUIDNamespaceAndInterfaceConfusion(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*infrav1alpha1.Connection, *unstructured.Unstructured, *unstructured.Unstructured, *unstructured.Unstructured)
		want   string
	}{
		{name: "source uid", mutate: func(c *infrav1alpha1.Connection, _, _, _ *unstructured.Unstructured) {
			c.Spec.Source.InstanceRef.UID = "wrong"
		}, want: "source Instance"},
		{name: "target uid", mutate: func(c *infrav1alpha1.Connection, _, _, _ *unstructured.Unstructured) { c.Spec.Target.UID = "wrong" }, want: "target Instance"},
		{name: "runtime namespace", mutate: func(_ *infrav1alpha1.Connection, _, target, _ *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(target.Object, "runtime-b", "status", "runtimeNamespace")
		}, want: "does not match target runtime namespace"},
		{name: "secret namespace", mutate: func(_ *infrav1alpha1.Connection, source, _, _ *unstructured.Unstructured) {
			_ = unstructured.SetNestedField(source.Object, "runtime-b", "status", "connectionSecretRef", "namespace")
		}, want: "source Secret namespace"},
		{name: "interface type", mutate: func(_ *infrav1alpha1.Connection, _, _, targetTemplate *unstructured.Unstructured) {
			consumes, _, _ := unstructured.NestedSlice(targetTemplate.Object, "spec", "connections", "consumes")
			consumes[0].(map[string]any)["type"] = "redis"
			_ = unstructured.SetNestedSlice(targetTemplate.Object, consumes, "spec", "connections", "consumes")
		}, want: "incompatible"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			controller, tenantClient, connection, source, target, targetTemplate := connectionResolutionFixture(t)
			tc.mutate(connection, source, target, targetTemplate)
			// Rebuild clients so object mutations are authoritative.
			controller.templates = fake.NewSimpleDynamicClient(runtime.NewScheme(), sourceTemplateObject(t), targetTemplate)
			scheme := runtime.NewScheme()
			scheme.AddKnownTypeWithName(instanceGVK, &unstructured.Unstructured{})
			scheme.AddKnownTypeWithName(instanceGVK.GroupVersion().WithKind("InstanceList"), &unstructured.UnstructuredList{})
			tenantClient = clientfake.NewClientBuilder().WithScheme(scheme).WithObjects(source, target).Build()
			_, err := controller.resolveConnection(context.Background(), tenantClient, "tenant", connection)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("resolve error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestAggregateResolvedConnectionsMergeCollisionAndRotation(t *testing.T) {
	first := resolvedForAggregate("c1", "rv1", infrav1alpha1.ConnectionTargetInstance, "DATABASE_URL", "postgres")
	second := resolvedForAggregate("c2", "rv1", infrav1alpha1.ConnectionTargetInstance, "REDIS_URL", "redis")
	aggregate, err := aggregateResolvedConnections("instance/target", []*resolvedConnection{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if len(aggregate.Data) != 2 || aggregate.Data["DATABASE_URL"] != "postgres" || aggregate.Data["REDIS_URL"] != "redis" {
		t.Fatalf("aggregate data = %#v", aggregate.Data)
	}
	revision := aggregate.Revision
	first.sourceVersion = "rv2"
	rotated, err := aggregateResolvedConnections("instance/target", []*resolvedConnection{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Revision == revision || rotated.Data["DATABASE_URL"] != "postgres" {
		t.Fatalf("rotation revision=%q old=%q data=%#v", rotated.Revision, revision, rotated.Data)
	}
	second.mappings[0].TargetKey = "DATABASE_URL"
	if _, err := aggregateResolvedConnections("instance/target", []*resolvedConnection{first, second}); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("collision error = %v", err)
	}
}

func TestAggregateDevelopmentServicesUseIsolatedFileKeys(t *testing.T) {
	first := resolvedForAggregate("c1", "rv1", infrav1alpha1.ConnectionTargetService, "DATABASE_URL", "one")
	second := resolvedForAggregate("c2", "rv1", infrav1alpha1.ConnectionTargetService, "DATABASE_URL", "two")
	aggregate, err := aggregateResolvedConnections("sandbox/target", []*resolvedConnection{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if len(aggregate.Data) != 2 || aggregate.Data[connectionDataKey("c1", "DATABASE_URL")] != "one" || aggregate.Data[connectionDataKey("c2", "DATABASE_URL")] != "two" {
		t.Fatalf("aggregate data = %#v", aggregate.Data)
	}
}

func TestApplyAggregateSecretFailsClosedOnUnmanagedOrWrongIdentity(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	client := fake.NewSimpleDynamicClient(scheme, secretObject("runtime", aggregateConnectionSecretName("instance/a"), map[string]string{"KEEP": "dmFsdWU="}, nil, nil))
	aggregate := &aggregateConnectionSecret{Name: aggregateConnectionSecretName("instance/a"), Namespace: "runtime", RuntimeIdentity: "instance/a", Data: map[string]string{"DATABASE_URL": "bmV3"}, Inventory: map[string][]string{"c1": {"DATABASE_URL"}}, Revision: "r1"}
	if err := applyAggregateConnectionSecret(ctx, client, aggregate); err == nil || !strings.Contains(err.Error(), "not owned") {
		t.Fatalf("unmanaged same-name error = %v", err)
	}
	got, _ := client.Resource(connectionSecretGVR).Namespace("runtime").Get(ctx, aggregate.Name, metav1.GetOptions{})
	data, _, _ := unstructured.NestedStringMap(got.Object, "data")
	if data["KEEP"] != "dmFsdWU=" || data["DATABASE_URL"] != "" {
		t.Fatalf("unmanaged Secret mutated: %#v", data)
	}

	wrongLabels := map[string]string{connectionManagedByLabel: connectionManagedByValue, connectionTargetRuntimeLabel: shortHash("instance/a")}
	wrongAnnotations := map[string]string{connectionTargetRuntimeAnnotation: "instance/b", connectionInventoryAnnotation: `{}`}
	client = fake.NewSimpleDynamicClient(scheme, secretObject("runtime", aggregate.Name, map[string]string{"KEEP": "dmFsdWU="}, wrongLabels, wrongAnnotations))
	if err := applyAggregateConnectionSecret(ctx, client, aggregate); err == nil || !strings.Contains(err.Error(), "not owned") {
		t.Fatalf("identity collision error = %v", err)
	}
}

func TestConnectionRuntimeMetadataRefusesUnownedSecretMount(t *testing.T) {
	ctx := context.Background()
	instance := instanceObject("target", "target-uid", "simple-webapp", "runtime")
	identity := "instance/target-uid"
	name := aggregateConnectionSecretName(identity)
	unmanaged := secretObject("runtime", name, map[string]string{"DATABASE_URL": "c3RvbGVu"}, nil, map[string]string{connectionRevisionAnnotation: "attacker"})
	controller := &Controller{cfg: Config{Runtime: fake.NewSimpleDynamicClient(runtime.NewScheme(), unmanaged)}}
	if _, _, err := controller.connectionRuntimeMetadata(ctx, "tenant", instance); err == nil || !strings.Contains(err.Error(), "not owned") {
		t.Fatalf("unowned mount error = %v", err)
	}
	labels := map[string]string{connectionManagedByLabel: connectionManagedByValue, connectionTargetRuntimeLabel: shortHash(identity)}
	annotations := map[string]string{connectionTargetRuntimeAnnotation: identity, connectionRevisionAnnotation: "safe"}
	controller.cfg.Runtime = fake.NewSimpleDynamicClient(runtime.NewScheme(), secretObject("runtime", name, map[string]string{"DATABASE_URL": "c2FmZQ=="}, labels, annotations))
	gotName, revision, err := controller.connectionRuntimeMetadata(ctx, "tenant", instance)
	if err != nil || gotName != name || revision != "safe" {
		t.Fatalf("managed metadata = %q %q %v", gotName, revision, err)
	}
}

func TestRemoveConnectionFromAggregateRequiresExpectedOwnership(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	name := aggregateConnectionSecretName("instance/a")
	labels := map[string]string{connectionManagedByLabel: connectionManagedByValue, connectionTargetRuntimeLabel: shortHash("instance/a")}
	annotations := map[string]string{connectionTargetRuntimeAnnotation: "instance/a", connectionInventoryAnnotation: `{"c1":["DATABASE_URL"]}`}
	client := fake.NewSimpleDynamicClient(scheme, secretObject("runtime", name, map[string]string{"DATABASE_URL": "dmFsdWU="}, labels, annotations))
	if err := removeConnectionFromAggregate(ctx, client, "runtime", name, "instance/b", "c1"); err == nil {
		t.Fatal("wrong target identity cleanup succeeded")
	}
	if _, err := client.Resource(connectionSecretGVR).Namespace("runtime").Get(ctx, name, metav1.GetOptions{}); err != nil {
		t.Fatalf("Secret removed after rejected cleanup: %v", err)
	}
}

func TestFallbackRemovalChangesRevisionAndPreservesOwnership(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	identity := "sandbox/runtime-uid"
	name := aggregateConnectionSecretName(identity)
	entries := map[string]string{"c1": "rev-one", "c2": "rev-two"}
	entryRaw, _ := json.Marshal(entries)
	labels := map[string]string{connectionManagedByLabel: connectionManagedByValue, connectionTargetRuntimeLabel: shortHash(identity)}
	annotations := map[string]string{
		connectionTargetRuntimeAnnotation:  identity,
		connectionInventoryAnnotation:      `{"c1":["key-one"],"c2":["key-two"]}`,
		connectionEntryRevisionsAnnotation: string(entryRaw),
		connectionRevisionAnnotation:       aggregateRevision(entries),
	}
	oldRevision := annotations[connectionRevisionAnnotation]
	client := fake.NewSimpleDynamicClient(scheme, secretObject("runtime", name, map[string]string{"key-one": "b25l", "key-two": "dHdv"}, labels, annotations))
	if err := removeConnectionFromAggregate(ctx, client, "runtime", name, identity, "c1"); err != nil {
		t.Fatal(err)
	}
	got, err := client.Resource(connectionSecretGVR).Namespace("runtime").Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	data, _, _ := unstructured.NestedStringMap(got.Object, "data")
	if _, exists := data["key-one"]; exists || data["key-two"] != "dHdv" {
		t.Fatalf("data after removal = %#v", data)
	}
	if got.GetAnnotations()[connectionRevisionAnnotation] == oldRevision {
		t.Fatalf("aggregate revision did not change: %q", oldRevision)
	}
	if err := validateAggregateSecretOwnership(got, identity); err != nil {
		t.Fatalf("ownership lost after fallback cleanup: %v", err)
	}
}

func TestWithdrawInvalidConnectionClearsAggregateCredentialsAndStatus(t *testing.T) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	identity := "sandbox/runtime-uid"
	name := aggregateConnectionSecretName(identity)
	entryRevisions := map[string]string{"c1": "old-revision", "c2": "other-revision"}
	entryRaw, _ := json.Marshal(entryRevisions)
	keyOne := connectionDataKey("c1", "DATABASE_URL")
	keyTwo := connectionDataKey("c2", "DATABASE_URL")
	labels := map[string]string{
		connectionManagedByLabel:     connectionManagedByValue,
		connectionTargetRuntimeLabel: shortHash(identity),
	}
	annotations := map[string]string{
		connectionTargetRuntimeAnnotation:  identity,
		connectionInventoryAnnotation:      `{"c1":["` + keyOne + `"],"c2":["` + keyTwo + `"]}`,
		connectionEntryRevisionsAnnotation: string(entryRaw),
		connectionRevisionAnnotation:       aggregateRevision(entryRevisions),
	}
	runtimeClient := fake.NewSimpleDynamicClient(scheme, secretObject("runtime", name, map[string]string{
		keyOne: "b2xkLXNlY3JldA==",
		keyTwo: "b3RoZXItc2VjcmV0",
	}, labels, annotations))
	connection := &infrav1alpha1.Connection{
		ObjectMeta: metav1.ObjectMeta{Name: "binding", UID: types.UID("c1"), Generation: 2},
		Spec: infrav1alpha1.ConnectionSpec{
			Target: infrav1alpha1.ConnectionTarget{Kind: infrav1alpha1.ConnectionTargetService, Name: "web", UID: "replacement-service-uid"},
		},
		Status: infrav1alpha1.ConnectionStatus{
			Revision:         "old-revision",
			ManagedSecretRef: &infrav1alpha1.ConnectionManagedSecretReference{Name: name, Namespace: "runtime", TargetRuntimeIdentity: identity},
			Conditions:       []metav1.Condition{{Type: infrav1alpha1.ConnectionConditionReady, Status: metav1.ConditionTrue, Reason: "Ready"}},
		},
	}
	controller := &Controller{cfg: Config{Runtime: runtimeClient}}

	if err := controller.withdrawConnectionFromAggregate(ctx, connection); err != nil {
		t.Fatalf("withdrawConnectionFromAggregate() error = %v", err)
	}
	gotSecret, err := runtimeClient.Resource(connectionSecretGVR).Namespace("runtime").Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get aggregate Secret after withdrawal: %v", err)
	}
	data, _, err := unstructured.NestedStringMap(gotSecret.Object, "data")
	if err != nil {
		t.Fatalf("aggregate Secret data: %v", err)
	}
	if _, exists := data[keyOne]; exists {
		t.Fatalf("withdrawn connection credential remains in aggregate: %#v", data)
	}
	if data[keyTwo] != "b3RoZXItc2VjcmV0" {
		t.Fatalf("unrelated connection credential changed: %#v", data)
	}

	if err := infrav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add infrastructure scheme: %v", err)
	}
	tenantClient := clientfake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(connection).WithObjects(connection).Build()
	if err := controller.persistConnectionStatus(ctx, tenantClient, connection, nil, "InvalidConnection", "target UID was replaced"); err != nil {
		t.Fatalf("persist invalid Connection status: %v", err)
	}
	gotConnection := &infrav1alpha1.Connection{}
	if err := tenantClient.Get(ctx, client.ObjectKey{Name: connection.Name}, gotConnection); err != nil {
		t.Fatalf("get invalid Connection: %v", err)
	}
	if gotConnection.Status.Revision != "" || gotConnection.Status.ManagedSecretRef != nil {
		t.Fatalf("invalid Connection retained managed credentials status: %+v", gotConnection.Status)
	}
	ready := meta.FindStatusCondition(gotConnection.Status.Conditions, infrav1alpha1.ConnectionConditionReady)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "InvalidConnection" {
		t.Fatalf("invalid Connection Ready condition = %+v", ready)
	}
}

func TestConnectionStatusContainsMetadataNotSecretValues(t *testing.T) {
	status := infrav1alpha1.ConnectionStatus{Revision: "rv", ManagedSecretRef: &infrav1alpha1.ConnectionManagedSecretReference{Name: "aggregate", Namespace: "runtime", TargetRuntimeIdentity: "instance/uid"}}
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"password", "postgres://", "secretValue", "data"} {
		if strings.Contains(strings.ToLower(string(raw)), strings.ToLower(forbidden)) {
			t.Fatalf("status unexpectedly contains %q: %s", forbidden, raw)
		}
	}
}

func TestPersistConnectionStatusSkipsSemanticNoop(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := infrav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	connection := &infrav1alpha1.Connection{ObjectMeta: metav1.ObjectMeta{Name: "binding", UID: types.UID("c1"), Generation: 1}}
	updates := 0
	client := clientfake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(connection).WithObjects(connection).WithInterceptorFuncs(interceptor.Funcs{
		SubResourceUpdate: func(ctx context.Context, underlying client.Client, subresource string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			updates++
			return underlying.SubResource(subresource).Update(ctx, obj, opts...)
		},
	}).Build()
	aggregate := &aggregateConnectionSecret{Name: "aggregate", Namespace: "runtime", RuntimeIdentity: "instance/target", ConnectionRevisions: map[string]string{"c1": "revision"}}
	controller := &Controller{}
	if err := controller.persistConnectionStatus(context.Background(), client, connection, aggregate, "Ready", "connection credentials are synchronized"); err != nil {
		t.Fatal(err)
	}
	if err := controller.persistConnectionStatus(context.Background(), client, connection, aggregate, "Ready", "connection credentials are synchronized"); err != nil {
		t.Fatal(err)
	}
	if updates != 1 {
		t.Fatalf("status updates = %d, want 1", updates)
	}
}

func resolvedForAggregate(uid, version, targetKind, targetKey, value string) *resolvedConnection {
	connection := &infrav1alpha1.Connection{ObjectMeta: metav1.ObjectMeta{Name: uid, UID: types.UID(uid)}}
	return &resolvedConnection{connection: connection, runtimeNamespace: "runtime", runtimeIdentity: "instance/target", targetKind: targetKind, sourceSecretUID: "source-" + uid, sourceVersion: version, sourceData: map[string]string{"uri": value}, mappings: []infrav1alpha1.ConnectionMapping{{SourceKey: "uri", TargetKey: targetKey}}}
}

func connectionResolutionFixture(t *testing.T) (*Controller, client.Client, *infrav1alpha1.Connection, *unstructured.Unstructured, *unstructured.Unstructured, *unstructured.Unstructured) {
	t.Helper()
	sourceTemplate := sourceTemplateObject(t)
	targetTemplate := targetTemplateObject(t)
	runtimeClient := fake.NewSimpleDynamicClient(runtime.NewScheme(), secretObject("runtime-a", "source-credentials", map[string]string{"uri": "cG9zdGdyZXM6Ly9kYg=="}, nil, nil))
	controller := &Controller{cfg: Config{Runtime: runtimeClient}, templates: fake.NewSimpleDynamicClient(runtime.NewScheme(), sourceTemplate, targetTemplate), contracts: map[string]*cachedContract{}}
	source := instanceObject("source", "source-uid", "database", "runtime-a")
	_ = unstructured.SetNestedMap(source.Object, map[string]any{"name": "source-credentials", "namespace": "runtime-a"}, "status", "connectionSecretRef")
	target := instanceObject("target", "target-uid", "application-target", "runtime-a")
	connection := &infrav1alpha1.Connection{ObjectMeta: metav1.ObjectMeta{Name: "binding", UID: types.UID("connection-uid")}, Spec: infrav1alpha1.ConnectionSpec{
		Source: infrav1alpha1.ConnectionSource{InstanceRef: infrav1alpha1.ConnectionObjectReference{Name: "source", UID: "source-uid"}, Interface: "default"},
		Target: infrav1alpha1.ConnectionTarget{Kind: infrav1alpha1.ConnectionTargetInstance, Name: "target", UID: "target-uid", Interface: "postgresql"},
	}}
	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(instanceGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(instanceGVK.GroupVersion().WithKind("InstanceList"), &unstructured.UnstructuredList{})
	tenantClient := clientfake.NewClientBuilder().WithScheme(scheme).WithObjects(source, target).Build()
	return controller, tenantClient, connection, source, target, targetTemplate
}

func sourceTemplateObject(t *testing.T) *unstructured.Unstructured {
	t.Helper()
	return templateObject(t, "database", &infrav1alpha1.TemplateConnections{Provides: []infrav1alpha1.TemplateProvidedConnection{{Name: "default", Type: "postgresql", SecretRefPath: "status.connectionSecretRef", Keys: []string{"uri"}}}})
}

func targetTemplateObject(t *testing.T) *unstructured.Unstructured {
	t.Helper()
	return templateObject(t, "application-target", &infrav1alpha1.TemplateConnections{Consumes: []infrav1alpha1.TemplateConsumedConnection{{Name: "postgresql", Type: "postgresql", Mappings: []infrav1alpha1.TemplateConnectionMapping{{SourceKey: "uri", TargetKey: "DATABASE_URL"}}}}})
}

func templateObject(t *testing.T, name string, connections *infrav1alpha1.TemplateConnections) *unstructured.Unstructured {
	t.Helper()
	template := &infrav1alpha1.Template{TypeMeta: metav1.TypeMeta{APIVersion: infrav1alpha1.SchemeGroupVersion.String(), Kind: "Template"}, ObjectMeta: metav1.ObjectMeta{Name: name}, Spec: infrav1alpha1.TemplateSpec{
		Version: "0.1.0", Backend: "stub", InstanceCRD: infrav1alpha1.TemplateInstanceCRD{Group: infrav1alpha1.GroupName, Version: infrav1alpha1.Version, Resource: name + "s", Kind: "Fixture"},
		Schema: &runtime.RawExtension{Raw: []byte(`{"type":"object","properties":{"name":{"type":"string"}}}`)}, Connections: connections,
	}}
	object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(template)
	if err != nil {
		t.Fatal(err)
	}
	return &unstructured.Unstructured{Object: object}
}

func instanceObject(name, uid, template, namespace string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": infrav1alpha1.SchemeGroupVersion.String(), "kind": "Instance",
		"metadata": map[string]any{"name": name, "uid": uid},
		"spec":     map[string]any{"template": template, "values": map[string]any{}},
		"status":   map[string]any{"runtimeNamespace": namespace},
	}}
	obj.SetGroupVersionKind(instanceGVK)
	return obj
}

func secretObject(namespace, name string, data, labels, annotations map[string]string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"name": name, "namespace": namespace}, "data": stringMapAny(data)}}
	obj.SetLabels(labels)
	obj.SetAnnotations(annotations)
	return obj
}
