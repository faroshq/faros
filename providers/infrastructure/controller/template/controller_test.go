/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package template

// Reconciler unit tests using the in-memory client.Fake (for
// Template + status patches) + dynamic.Fake (for CRDs +
// APIResourceSchema + APIExport). The split is deliberate:
//
//   - The Reconciler reads + patches Template CRs through a typed
//     client (mgr.GetClient()), so the test wires that path through
//     controller-runtime's fake builder with the infrastructure
//     scheme registered.
//
//   - Everything else the controller touches (per-template CRD,
//     APIResourceSchema, APIExport) goes through r.Dynamic. The
//     dynamic fake supports Create + Get + Update on those resources
//     once we register a stub APIExport (so ensureAPIExportEntry's
//     Get succeeds).
//
// Coverage:
//   * happy path — Template reaches Ready=True; per-template CRD
//     written; APIResourceSchema minted; APIExport.spec.resources
//     contains a matching entry
//   * delete  — APIExport entry removed; per-template CRD deleted;
//     finalizer dropped
//   * backend missing — Template's Ready condition reports
//     BackendNotFound, no CRD created

import (
	"context"
	"encoding/json"
	"testing"

	apisv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha2"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clientgotesting "k8s.io/client-go/testing"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1alpha1 "github.com/faroshq/provider-infrastructure/apis/v1alpha1"
	"github.com/faroshq/provider-infrastructure/backend"
	"github.com/faroshq/provider-infrastructure/backend/stub"
)

// newTestReconciler wires up the two fakes + a backend registry
// containing only the stub. The dynamic fake is seeded with an empty
// APIExport so ensureAPIExportEntry's Get succeeds — the controller
// can't materialize the APIExport itself (the hub's catalog
// controller does that in prod).
func newTestReconciler(t *testing.T, initial ...client.Object) (*Reconciler, *dynamicfake.FakeDynamicClient, *stub.Backend) {
	return newTestReconcilerWithCRDEstablishment(t, true, initial...)
}

func newTestReconcilerWithCRDEstablishment(t *testing.T, establishCRDs bool, initial ...client.Object) (*Reconciler, *dynamicfake.FakeDynamicClient, *stub.Backend) {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := infrav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}

	c := clientfake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&infrav1alpha1.Template{}).
		WithObjects(initial...).
		Build()

	// The dynamic fake needs every GVR pre-mapped via a scheme that
	// understands what plural→list-kind to use. Building one in line
	// keeps the test self-contained; the production code path uses
	// the apiserver's discovery which doesn't need this.
	dynScheme := runtime.NewScheme()
	dynScheme.AddKnownTypeWithName(crdGVR.GroupVersion().WithKind("CustomResourceDefinitionList"), &unstructured.UnstructuredList{})
	dynScheme.AddKnownTypeWithName(apiResourceSchemaGVR.GroupVersion().WithKind("APIResourceSchemaList"), &unstructured.UnstructuredList{})
	dynScheme.AddKnownTypeWithName(apiExportGVR.GroupVersion().WithKind("APIExportList"), &unstructured.UnstructuredList{})

	exportObj := &unstructured.Unstructured{}
	exportObj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   apiExportGVR.Group,
		Version: apiExportGVR.Version,
		Kind:    "APIExport",
	})
	exportObj.SetName(APIExportName)
	dyn := dynamicfake.NewSimpleDynamicClient(dynScheme, exportObj)
	if establishCRDs {
		markEstablished := func(obj runtime.Object) {
			u, ok := obj.(*unstructured.Unstructured)
			if !ok {
				t.Fatalf("CRD reactor object = %T, want unstructured", obj)
			}
			if err := unstructured.SetNestedSlice(u.Object, []any{map[string]any{
				"type":   string(apiextensionsv1.Established),
				"status": string(apiextensionsv1.ConditionTrue),
			}}, "status", "conditions"); err != nil {
				t.Fatalf("mark CRD established: %v", err)
			}
		}
		dyn.PrependReactor("create", "customresourcedefinitions", func(action clientgotesting.Action) (bool, runtime.Object, error) {
			markEstablished(action.(clientgotesting.CreateAction).GetObject())
			return false, nil, nil
		})
		dyn.PrependReactor("update", "customresourcedefinitions", func(action clientgotesting.Action) (bool, runtime.Object, error) {
			markEstablished(action.(clientgotesting.UpdateAction).GetObject())
			return false, nil, nil
		})
	}

	reg := backend.NewRegistry()
	stb := stub.New()
	if err := reg.Register(stb); err != nil {
		t.Fatalf("register stub: %v", err)
	}

	r := &Reconciler{
		Client:   c,
		Dynamic:  dyn,
		Backends: reg,
	}
	return r, dyn, stb
}

func TestReconcileWaitsForCRDEstablishedBeforePublishing(t *testing.T) {
	tmpl := newTestTemplate(t, "await")
	r, dyn, stb := newTestReconcilerWithCRDEstablishment(t, false, tmpl)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: tmpl.Name}}

	if _, err := r.Reconcile(context.Background(), req); err != nil { // finalizer
		t.Fatal(err)
	}
	result, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.RequeueAfter == 0 {
		t.Fatal("unestablished CRD did not request a requeue")
	}
	var waiting infrav1alpha1.Template
	if err := r.Client.Get(context.Background(), req.NamespacedName, &waiting); err != nil {
		t.Fatal(err)
	}
	if condition := findCondition(waiting.Status.Conditions, infrav1alpha1.ConditionCRDEstablished); condition == nil || condition.Status != metav1.ConditionFalse || condition.Reason != infrav1alpha1.ReasonAwaitingEstablish {
		t.Fatalf("CRDEstablished condition before establishment = %+v", condition)
	}
	if waiting.Status.Registered.SchemaInAPIExport || len(stb.SeenSetups) != 0 {
		t.Fatalf("publication/backend ran before CRD establishment: status=%+v setups=%v", waiting.Status.Registered, stb.SeenSetups)
	}
	export, err := dyn.Resource(apiExportGVR).Get(context.Background(), APIExportName, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	resources, err := getAPIExportResources(export)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 0 {
		t.Fatalf("APIExport published before CRD establishment: %v", resources)
	}

	crd, err := dyn.Resource(crdGVR).Get(context.Background(), perTemplateCRDName(tmpl), metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := unstructured.SetNestedSlice(crd.Object, []any{map[string]any{
		"type":   string(apiextensionsv1.Established),
		"status": string(apiextensionsv1.ConditionTrue),
	}}, "status", "conditions"); err != nil {
		t.Fatal(err)
	}
	if _, err := dyn.Resource(crdGVR).Update(context.Background(), crd, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	var ready infrav1alpha1.Template
	if err := r.Client.Get(context.Background(), req.NamespacedName, &ready); err != nil {
		t.Fatal(err)
	}
	if condition := findCondition(ready.Status.Conditions, infrav1alpha1.ConditionReady); condition == nil || condition.Status != metav1.ConditionTrue {
		t.Fatalf("Ready condition after establishment = %+v", condition)
	}
}

// newTestTemplate returns a minimal Template with spec.backend=stub
// and a trivial schema. Test cases override fields as needed.
func newTestTemplate(t *testing.T, name string) *infrav1alpha1.Template {
	t.Helper()
	schemaRaw, err := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
	})
	if err != nil {
		t.Fatalf("marshal test schema: %v", err)
	}
	return &infrav1alpha1.Template{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: infrav1alpha1.TemplateSpec{
			Version: "0.1.0",
			Backend: stub.Name,
			InstanceCRD: infrav1alpha1.TemplateInstanceCRD{
				Group:    infrav1alpha1.GroupName,
				Version:  "v1alpha1",
				Resource: name + "s", // crude pluralize; fine for test names
				Kind:     "Test" + capitalize(name),
			},
			Schema: &runtime.RawExtension{Raw: schemaRaw},
		},
	}
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return string(s[0]-32) + s[1:]
}

// reconcileUntilSettled drives Reconcile until two consecutive calls
// don't change anything observable, or the cap is hit. Useful for
// tests that go through the "add finalizer → requeue → real work"
// progression the controller designs in. Capped at 5 iterations.
func reconcileUntilSettled(t *testing.T, r *Reconciler, name string) {
	t.Helper()
	for range 5 {
		_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: name}})
		if err != nil {
			// The happy path may emit errors mid-progression (e.g.
			// requeue after AddFinalizer); the test exits on a
			// failed assertion if final state isn't right.
			continue
		}
	}
}

func TestReconcileHappyPath(t *testing.T) {
	tmpl := newTestTemplate(t, "redis")
	r, dyn, stb := newTestReconciler(t, tmpl)

	reconcileUntilSettled(t, r, "redis")

	// Template should be Ready=True and the backend should have been
	// called exactly once with the right name.
	var got infrav1alpha1.Template
	if err := r.Client.Get(context.Background(), types.NamespacedName{Name: "redis"}, &got); err != nil {
		t.Fatalf("get template: %v", err)
	}
	if cond := findCondition(got.Status.Conditions, infrav1alpha1.ConditionReady); cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected Ready=True; conditions=%v", got.Status.Conditions)
	}
	if !got.Status.Registered.CRDEstablished {
		t.Fatalf("expected CRDEstablished=true")
	}
	if !got.Status.Registered.SchemaInAPIExport {
		t.Fatalf("expected SchemaInAPIExport=true")
	}
	if got.Status.Backend.Name != stub.Name {
		t.Fatalf("expected backend=%q in status; got %q", stub.Name, got.Status.Backend.Name)
	}

	// SetupTemplate is documented as idempotent and called per
	// reconcile pass, so we only assert that it was called at least
	// once with the right name — exact count is an implementation
	// detail of how many requeues the controller does.
	if len(stb.SeenSetups) < 1 || stb.SeenSetups[0] != "redis" {
		t.Fatalf("expected at least one stub SetupTemplate call for redis; got %v", stb.SeenSetups)
	}

	// Per-template CRD must be in the dynamic fake.
	crdName := perTemplateCRDName(&got)
	_, err := dyn.Resource(crdGVR).Get(context.Background(), crdName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("per-template CRD %q not created: %v", crdName, err)
	}

	// APIExport.spec.resources must have one entry pointing at the
	// minted APIResourceSchema.
	export, err := dyn.Resource(apiExportGVR).Get(context.Background(), APIExportName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get APIExport: %v", err)
	}
	resources, err := getAPIExportResources(export)
	if err != nil {
		t.Fatalf("decode resources: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("expected one resource entry; got %d (%v)", len(resources), resources)
	}
	if resources[0].Name != "rediss" || resources[0].Group != infrav1alpha1.GroupName {
		t.Fatalf("resource entry name/group wrong: %+v", resources[0])
	}
	if resources[0].Schema == "" {
		t.Fatalf("resource entry missing schema name")
	}
}

func TestReconcileRechecksBackendReadinessAndDegradesAfterLoss(t *testing.T) {
	tmpl := newTestTemplate(t, "backend-health")
	r, dyn, stb := newTestReconciler(t, tmpl)
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: tmpl.Name}}
	getPublishedResources := func() []apisv1alpha2.ResourceSchema {
		t.Helper()
		export, err := dyn.Resource(apiExportGVR).Get(context.Background(), APIExportName, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get APIExport: %v", err)
		}
		resources, err := getAPIExportResources(export)
		if err != nil {
			t.Fatalf("decode APIExport resources: %v", err)
		}
		return resources
	}

	// The first pass only installs the finalizer. Start the actual setup with a
	// backend that has not accepted the template yet.
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("add finalizer: %v", err)
	}
	stb.FailSetup = true
	pending, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("reconcile pending backend: %v", err)
	}
	if pending.RequeueAfter != backendPendingRequeueInterval {
		t.Fatalf("pending backend requeue = %s, want %s", pending.RequeueAfter, backendPendingRequeueInterval)
	}
	assertTemplateReadyCondition(t, r.Client, req.NamespacedName, metav1.ConditionFalse)
	if resources := getPublishedResources(); len(resources) != 0 {
		t.Fatalf("APIExport published before backend readiness: %v", resources)
	}

	// Once accepted, the template becomes ready but retains a periodic requeue:
	// kro's RGD status lives on a different cluster, so there is no local watch
	// event that could otherwise report a later GraphAccepted loss.
	stb.FailSetup = false
	ready, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("reconcile ready backend: %v", err)
	}
	if ready.RequeueAfter != backendReadyRequeueInterval {
		t.Fatalf("ready backend requeue = %s, want %s", ready.RequeueAfter, backendReadyRequeueInterval)
	}
	assertTemplateReadyCondition(t, r.Client, req.NamespacedName, metav1.ConditionTrue)
	if resources := getPublishedResources(); len(resources) != 1 || resources[0].Name != tmpl.Spec.InstanceCRD.Resource {
		t.Fatalf("APIExport resources after backend readiness = %v", resources)
	}

	// Simulate that periodic reconcile after the external backend loses
	// acceptance. Platform readiness must degrade instead of remaining stale.
	stb.FailSetup = true
	degraded, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("reconcile degraded backend: %v", err)
	}
	if degraded.RequeueAfter != backendPendingRequeueInterval {
		t.Fatalf("degraded backend requeue = %s, want %s", degraded.RequeueAfter, backendPendingRequeueInterval)
	}
	assertTemplateReadyCondition(t, r.Client, req.NamespacedName, metav1.ConditionFalse)
	if resources := getPublishedResources(); len(resources) != 1 || resources[0].Name != tmpl.Spec.InstanceCRD.Resource {
		t.Fatalf("backend degradation hid the existing APIExport resource: %v", resources)
	}
}

func assertTemplateReadyCondition(t *testing.T, c client.Client, key types.NamespacedName, want metav1.ConditionStatus) {
	t.Helper()
	var got infrav1alpha1.Template
	if err := c.Get(context.Background(), key, &got); err != nil {
		t.Fatalf("get template: %v", err)
	}
	condition := findCondition(got.Status.Conditions, infrav1alpha1.ConditionReady)
	if condition == nil || condition.Status != want {
		t.Fatalf("Ready condition = %+v, want status %s", condition, want)
	}
}

func TestReconcileBackendNotFound(t *testing.T) {
	tmpl := newTestTemplate(t, "missing")
	tmpl.Spec.Backend = "does-not-exist"
	r, dyn, _ := newTestReconciler(t, tmpl)

	reconcileUntilSettled(t, r, "missing")

	var got infrav1alpha1.Template
	if err := r.Client.Get(context.Background(), types.NamespacedName{Name: "missing"}, &got); err != nil {
		t.Fatalf("get template: %v", err)
	}
	cond := findCondition(got.Status.Conditions, infrav1alpha1.ConditionReady)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != infrav1alpha1.ReasonBackendNotFound {
		t.Fatalf("expected Ready=False/BackendNotFound; got %+v", cond)
	}

	// No per-template CRD must have been created.
	crdName := perTemplateCRDName(&got)
	_, err := dyn.Resource(crdGVR).Get(context.Background(), crdName, metav1.GetOptions{})
	if err == nil {
		t.Fatalf("per-template CRD %q was created despite BackendNotFound", crdName)
	}
}

// TestReconcileRetiredTemplateIsSwept pins the retirement mechanism
// (retired.go): a Template whose name is on the retired list — e.g. left
// behind in a workspace seeded before the template was removed from the
// catalog — is deleted by the reconciler itself and dismantled through the
// normal finalize chain (backend teardown + CRD/APIExport cleanup), without
// any operator action.
func TestReconcileRetiredTemplateIsSwept(t *testing.T) {
	if _, ok := retiredTemplates["sandbox-runner"]; !ok {
		t.Fatal("sandbox-runner is no longer on the retired list; pick another retired name for this test")
	}
	tmpl := newTestTemplate(t, "sandbox-runner")
	tmpl.Spec.InstanceCRD.Kind = "SandboxRunner"
	tmpl.Spec.InstanceCRD.Resource = "sandboxrunners"
	r, dyn, stb := newTestReconciler(t, tmpl)

	reconcileUntilSettled(t, r, "sandbox-runner")

	// The Template must be gone — deleted by the controller, finalizer
	// removed by the finalize chain.
	var post infrav1alpha1.Template
	if err := r.Client.Get(context.Background(), types.NamespacedName{Name: "sandbox-runner"}, &post); err == nil {
		t.Fatalf("retired template still present after reconcile (deletionTimestamp=%v, finalizers=%v)",
			post.DeletionTimestamp, post.Finalizers)
	}

	// The finalize chain ran: the backend saw a teardown, and no
	// per-template CRD or APIExport entry survives. Retirement fires before
	// the CRD is ever authored on a fresh workspace, so absence — not
	// deletion — is the invariant.
	if len(stb.SeenTeardowns) < 1 {
		t.Fatalf("expected the finalize chain to call TeardownTemplate; got %v", stb.SeenTeardowns)
	}
	if _, err := dyn.Resource(crdGVR).Get(context.Background(), perTemplateCRDName(tmpl), metav1.GetOptions{}); err == nil {
		t.Fatal("per-template CRD exists for a retired template")
	}
	export, err := dyn.Resource(apiExportGVR).Get(context.Background(), APIExportName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get APIExport: %v", err)
	}
	resources, err := getAPIExportResources(export)
	if err != nil {
		t.Fatalf("decode resources: %v", err)
	}
	if len(resources) != 0 {
		t.Fatalf("APIExport still carries entries for a retired template: %v", resources)
	}

	// Re-applying the retired template must sweep it again — retirement is
	// enforced by the watch loop, not a one-shot migration.
	again := newTestTemplate(t, "sandbox-runner")
	if err := r.Client.Create(context.Background(), again); err != nil {
		t.Fatalf("re-create retired template: %v", err)
	}
	reconcileUntilSettled(t, r, "sandbox-runner")
	if err := r.Client.Get(context.Background(), types.NamespacedName{Name: "sandbox-runner"}, &post); err == nil {
		t.Fatal("re-applied retired template survived reconciliation")
	}
}

func TestReconcileDelete(t *testing.T) {
	tmpl := newTestTemplate(t, "delgone")
	r, dyn, stb := newTestReconciler(t, tmpl)

	// Reach Ready first so the per-template CRD + APIExport entry
	// exist.
	reconcileUntilSettled(t, r, "delgone")

	// Confirm CRD is present pre-delete.
	crdName := perTemplateCRDName(tmpl)
	if _, err := dyn.Resource(crdGVR).Get(context.Background(), crdName, metav1.GetOptions{}); err != nil {
		t.Fatalf("setup: per-template CRD missing: %v", err)
	}

	if err := r.Client.Delete(context.Background(), tmpl); err != nil {
		t.Fatalf("delete: %v", err)
	}
	reconcileUntilSettled(t, r, "delgone")

	// CRD should be gone.
	if _, err := dyn.Resource(crdGVR).Get(context.Background(), crdName, metav1.GetOptions{}); err == nil {
		t.Fatalf("per-template CRD %q still present after delete", crdName)
	}

	// APIExport entry should be empty.
	export, err := dyn.Resource(apiExportGVR).Get(context.Background(), APIExportName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get APIExport: %v", err)
	}
	resources, err := getAPIExportResources(export)
	if err != nil {
		t.Fatalf("decode resources: %v", err)
	}
	if len(resources) != 0 {
		t.Fatalf("expected empty resources after delete; got %v", resources)
	}

	// Stub backend should have seen at least one TeardownTemplate
	// (same idempotency note as SetupTemplate).
	if len(stb.SeenTeardowns) < 1 {
		t.Fatalf("expected at least one teardown; got %v", stb.SeenTeardowns)
	}

	// Template itself should be gone (the finalizer was removed).
	var post infrav1alpha1.Template
	err = r.Client.Get(context.Background(), types.NamespacedName{Name: "delgone"}, &post)
	if err == nil {
		t.Fatalf("template still present after finalizer removal")
	}
}

func TestPerTemplateCRDShape(t *testing.T) {
	tmpl := newTestTemplate(t, "shape")
	crd, err := buildPerTemplateCRD(tmpl)
	if err != nil {
		t.Fatalf("build crd: %v", err)
	}
	if crd.Spec.Group != infrav1alpha1.GroupName {
		t.Fatalf("crd group = %q; want %q", crd.Spec.Group, infrav1alpha1.GroupName)
	}
	if crd.Spec.Scope != apiextensionsv1.ClusterScoped {
		t.Fatalf("crd scope = %v; want Cluster", crd.Spec.Scope)
	}
	if len(crd.Spec.Versions) != 1 {
		t.Fatalf("expected one version; got %d", len(crd.Spec.Versions))
	}
	v := crd.Spec.Versions[0]
	if v.Schema == nil || v.Schema.OpenAPIV3Schema == nil {
		t.Fatalf("missing openAPI schema")
	}
	if _, ok := v.Schema.OpenAPIV3Schema.Properties["spec"]; !ok {
		t.Fatalf("openAPI missing spec property")
	}
	if _, ok := v.Schema.OpenAPIV3Schema.Properties["status"]; !ok {
		t.Fatalf("openAPI missing platform-provided status property")
	}
}

func TestSchemaPrefixIsStable(t *testing.T) {
	tmpl := newTestTemplate(t, "stable")
	crd1, _ := buildPerTemplateCRD(tmpl)
	crd2, _ := buildPerTemplateCRD(tmpl) // identical input
	if schemaPrefix(crd1) != schemaPrefix(crd2) {
		t.Fatalf("schemaPrefix must be deterministic for identical CRDs")
	}

	// Changing the schema content must change the prefix.
	tmpl.Spec.InstanceCRD.Version = "v1beta1"
	crd3, _ := buildPerTemplateCRD(tmpl)
	if schemaPrefix(crd1) == schemaPrefix(crd3) {
		t.Fatalf("schemaPrefix must change when CRD content changes")
	}
}

func TestSetConditionRefreshesGenerationWithoutFalseTransition(t *testing.T) {
	transition := metav1.Date(2026, 1, 2, 3, 4, 5, 0, metav1.Now().Location())
	tmpl := newTestTemplate(t, "condition")
	tmpl.Generation = 2
	tmpl.Status.Conditions = []metav1.Condition{{
		Type:               infrav1alpha1.ConditionReady,
		Status:             metav1.ConditionFalse,
		Reason:             "OldReason",
		Message:            "old message",
		ObservedGeneration: 1,
		LastTransitionTime: transition,
	}}

	setCondition(tmpl, infrav1alpha1.ConditionReady, metav1.ConditionFalse, "NewReason", "new message")
	condition := findCondition(tmpl.Status.Conditions, infrav1alpha1.ConditionReady)
	if condition == nil {
		t.Fatal("Ready condition missing")
	}
	if condition.ObservedGeneration != tmpl.Generation {
		t.Fatalf("ObservedGeneration = %d, want %d", condition.ObservedGeneration, tmpl.Generation)
	}
	if !condition.LastTransitionTime.Equal(&transition) {
		t.Fatalf("LastTransitionTime changed without a status transition: got %s, want %s", condition.LastTransitionTime, transition)
	}
	if condition.Reason != "NewReason" || condition.Message != "new message" {
		t.Fatalf("reason/message were not refreshed: %+v", condition)
	}

	setCondition(tmpl, infrav1alpha1.ConditionReady, metav1.ConditionTrue, infrav1alpha1.ReasonReady, "")
	condition = findCondition(tmpl.Status.Conditions, infrav1alpha1.ConditionReady)
	if condition == nil || condition.Status != metav1.ConditionTrue {
		t.Fatalf("Ready condition did not transition: %+v", condition)
	}
	if condition.LastTransitionTime.Equal(&transition) {
		t.Fatalf("LastTransitionTime did not change on a status transition: %+v", condition)
	}
}

// findCondition is a tiny test helper. Not exported because tests of
// other packages have their own preferences for condition lookup.
func findCondition(conds []metav1.Condition, condType string) *metav1.Condition {
	for i := range conds {
		if conds[i].Type == condType {
			return &conds[i]
		}
	}
	return nil
}
