/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package template

// Reconciler unit tests using the in-memory client.Fake. The controller's
// whole surface is Template + status + backend dispatch — per-template CRD
// and APIExport wiring were retired with the flattened Instance kind, so
// there is nothing dynamic-client-shaped left to fake.
//
// Coverage:
//   * happy path — Template reaches Ready=True; backend called
//   * invalid schema — SchemaValid=False / Ready=False, backend NOT called
//   * backend missing — Ready reports BackendNotFound
//   * retired sweep + delete — teardown runs, finalizer drops

import (
	"context"
	"encoding/json"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	infrav1alpha1 "github.com/faroshq/provider-infrastructure/apis/v1alpha1"
	"github.com/faroshq/provider-infrastructure/backend"
	"github.com/faroshq/provider-infrastructure/backend/stub"
)

// newTestReconciler wires up the fake client + a backend registry
// containing only the stub.
func newTestReconciler(t *testing.T, initial ...client.Object) (*Reconciler, *stub.Backend) {
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

	reg := backend.NewRegistry()
	stb := stub.New()
	if err := reg.Register(stb); err != nil {
		t.Fatalf("register stub: %v", err)
	}

	return &Reconciler{Client: c, Backends: reg}, stb
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

// reconcileUntilSettled drives Reconcile through the "add finalizer →
// requeue → real work" progression the controller designs in. Capped at 5
// iterations.
func reconcileUntilSettled(t *testing.T, r *Reconciler, name string) {
	t.Helper()
	for range 5 {
		_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: name}})
		if err != nil {
			continue
		}
	}
}

func TestReconcileHappyPath(t *testing.T) {
	tmpl := newTestTemplate(t, "redis")
	r, stb := newTestReconciler(t, tmpl)

	reconcileUntilSettled(t, r, "redis")

	var got infrav1alpha1.Template
	if err := r.Client.Get(context.Background(), types.NamespacedName{Name: "redis"}, &got); err != nil {
		t.Fatalf("get template: %v", err)
	}
	if cond := findCondition(got.Status.Conditions, infrav1alpha1.ConditionReady); cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected Ready=True; conditions=%v", got.Status.Conditions)
	}
	if cond := findCondition(got.Status.Conditions, infrav1alpha1.ConditionSchemaValid); cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected SchemaValid=True; conditions=%v", got.Status.Conditions)
	}
	if got.Status.Backend.Name != stub.Name {
		t.Fatalf("expected backend=%q in status; got %q", stub.Name, got.Status.Backend.Name)
	}

	// SetupTemplate is documented as idempotent and called per reconcile
	// pass, so only assert it was called at least once with the right name.
	if len(stb.SeenSetups) < 1 || stb.SeenSetups[0] != "redis" {
		t.Fatalf("expected at least one stub SetupTemplate call for redis; got %v", stb.SeenSetups)
	}
}

func TestReconcileUniversalCodingSandboxDisabledByDefault(t *testing.T) {
	tmpl := newTestTemplate(t, infrav1alpha1.UniversalCodingSandboxTemplateName)
	r, stb := newTestReconciler(t, tmpl)
	reconcileUntilSettled(t, r, tmpl.Name)

	var got infrav1alpha1.Template
	if err := r.Client.Get(context.Background(), types.NamespacedName{Name: tmpl.Name}, &got); err != nil {
		t.Fatalf("get template: %v", err)
	}
	ready := findCondition(got.Status.Conditions, infrav1alpha1.ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != infrav1alpha1.ReasonCodingSandboxDisabled {
		t.Fatalf("disabled coding sandbox condition = %+v", ready)
	}
	if len(stb.SeenSetups) != 0 {
		t.Fatalf("disabled coding sandbox reached backend: %v", stb.SeenSetups)
	}
}

func TestReconcileUniversalCodingSandboxRejectsUnsafeContractWhenEnabled(t *testing.T) {
	tmpl := newTestTemplate(t, infrav1alpha1.UniversalCodingSandboxTemplateName)
	r, stb := newTestReconciler(t, tmpl)
	r.CodingSandboxEnabled = true
	reconcileUntilSettled(t, r, tmpl.Name)

	var got infrav1alpha1.Template
	if err := r.Client.Get(context.Background(), types.NamespacedName{Name: tmpl.Name}, &got); err != nil {
		t.Fatalf("get template: %v", err)
	}
	ready := findCondition(got.Status.Conditions, infrav1alpha1.ConditionReady)
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != infrav1alpha1.ReasonInvalidSpec {
		t.Fatalf("unsafe enabled coding sandbox condition = %+v", ready)
	}
	if len(stb.SeenSetups) != 0 {
		t.Fatalf("unsafe enabled coding sandbox reached backend: %v", stb.SeenSetups)
	}
}

// TestReconcileInvalidSchema pins the values-contract gate: a Template whose
// schema claims a platform-reserved property must park on
// SchemaValid=False/InvalidSpec without ever reaching the backend — the
// instance controller could never validate values against it.
func TestReconcileInvalidSchema(t *testing.T) {
	tmpl := newTestTemplate(t, "claimsreserved")
	schemaRaw, err := json.Marshal(map[string]any{
		"type": "object",
		"properties": map[string]any{
			infrav1alpha1.FarosModeField: map[string]any{"type": "string"},
		},
	})
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	tmpl.Spec.Schema = &runtime.RawExtension{Raw: schemaRaw}
	r, stb := newTestReconciler(t, tmpl)

	reconcileUntilSettled(t, r, "claimsreserved")

	var got infrav1alpha1.Template
	if err := r.Client.Get(context.Background(), types.NamespacedName{Name: "claimsreserved"}, &got); err != nil {
		t.Fatalf("get template: %v", err)
	}
	cond := findCondition(got.Status.Conditions, infrav1alpha1.ConditionSchemaValid)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != infrav1alpha1.ReasonInvalidSpec {
		t.Fatalf("expected SchemaValid=False/InvalidSpec; got %+v", cond)
	}
	if ready := findCondition(got.Status.Conditions, infrav1alpha1.ConditionReady); ready == nil || ready.Status != metav1.ConditionFalse {
		t.Fatalf("expected Ready=False; got %+v", ready)
	}
	if len(stb.SeenSetups) != 0 {
		t.Fatalf("backend must not be called for an invalid schema; got %v", stb.SeenSetups)
	}
}

func TestReconcileBackendNotFound(t *testing.T) {
	tmpl := newTestTemplate(t, "missing")
	tmpl.Spec.Backend = "does-not-exist"
	r, stb := newTestReconciler(t, tmpl)

	reconcileUntilSettled(t, r, "missing")

	var got infrav1alpha1.Template
	if err := r.Client.Get(context.Background(), types.NamespacedName{Name: "missing"}, &got); err != nil {
		t.Fatalf("get template: %v", err)
	}
	cond := findCondition(got.Status.Conditions, infrav1alpha1.ConditionReady)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != infrav1alpha1.ReasonBackendNotFound {
		t.Fatalf("expected Ready=False/BackendNotFound; got %+v", cond)
	}
	if len(stb.SeenSetups) != 0 {
		t.Fatalf("stub backend must not see setups for an unknown backend name; got %v", stb.SeenSetups)
	}
}

// TestReconcileRetiredTemplateIsSwept pins the retirement mechanism
// (retired.go): a Template whose name is on the retired list is deleted by
// the reconciler itself and dismantled through the normal finalize chain
// (backend teardown), without any operator action.
func TestReconcileRetiredTemplateIsSwept(t *testing.T) {
	if _, ok := retiredTemplates["sandbox-runner"]; !ok {
		t.Fatal("sandbox-runner is no longer on the retired list; pick another retired name for this test")
	}
	tmpl := newTestTemplate(t, "sandbox-runner")
	tmpl.Spec.InstanceCRD.Kind = "SandboxRunner"
	tmpl.Spec.InstanceCRD.Resource = "sandboxrunners"
	r, stb := newTestReconciler(t, tmpl)

	reconcileUntilSettled(t, r, "sandbox-runner")

	var post infrav1alpha1.Template
	if err := r.Client.Get(context.Background(), types.NamespacedName{Name: "sandbox-runner"}, &post); err == nil {
		t.Fatalf("retired template still present after reconcile (deletionTimestamp=%v, finalizers=%v)",
			post.DeletionTimestamp, post.Finalizers)
	}
	if len(stb.SeenTeardowns) < 1 {
		t.Fatalf("expected the finalize chain to call TeardownTemplate; got %v", stb.SeenTeardowns)
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
	r, stb := newTestReconciler(t, tmpl)

	reconcileUntilSettled(t, r, "delgone")

	if err := r.Client.Delete(context.Background(), tmpl); err != nil {
		t.Fatalf("delete: %v", err)
	}
	reconcileUntilSettled(t, r, "delgone")

	// Stub backend should have seen at least one TeardownTemplate.
	if len(stb.SeenTeardowns) < 1 {
		t.Fatalf("expected at least one teardown; got %v", stb.SeenTeardowns)
	}

	// Template itself should be gone (the finalizer was removed).
	var post infrav1alpha1.Template
	if err := r.Client.Get(context.Background(), types.NamespacedName{Name: "delgone"}, &post); err == nil {
		t.Fatalf("template still present after finalizer removal")
	}
}

func TestReconcileRejectsIdentityMutationAndDeletesRecordedBackend(t *testing.T) {
	ctx := context.Background()
	tmpl := newTestTemplate(t, "identity")
	r, stb := newTestReconciler(t, tmpl)
	reconcileUntilSettled(t, r, tmpl.Name)

	var provisioned infrav1alpha1.Template
	if err := r.Client.Get(ctx, types.NamespacedName{Name: tmpl.Name}, &provisioned); err != nil {
		t.Fatalf("get provisioned template: %v", err)
	}
	if provisioned.Status.Backend.Name != stub.Name || provisioned.Status.InstanceCRD == nil {
		t.Fatalf("provisioned identity missing: backend=%q instanceCRD=%+v", provisioned.Status.Backend.Name, provisioned.Status.InstanceCRD)
	}

	// A fake client does not run the CRD's CEL admission rule, so this drives
	// the controller-side defense directly. The edited backend must never see
	// a second SetupTemplate call, and the edited instance CRD must not redirect
	// cleanup either.
	provisioned.Spec.Backend = "missing"
	provisioned.Spec.InstanceCRD.Resource = "otherresources"
	if err := r.Client.Update(ctx, &provisioned); err != nil {
		t.Fatalf("update backend: %v", err)
	}
	setupCalls := len(stb.SeenSetups)
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: tmpl.Name}})
	if err != nil {
		t.Fatalf("identity mutation reconcile returned error: %v", err)
	}
	if len(stb.SeenSetups) != setupCalls {
		t.Fatalf("identity mutation called setup: before=%d after=%d", setupCalls, len(stb.SeenSetups))
	}
	var conflicted infrav1alpha1.Template
	if err := r.Client.Get(ctx, types.NamespacedName{Name: tmpl.Name}, &conflicted); err != nil {
		t.Fatalf("get conflicted template: %v", err)
	}
	if cond := findCondition(conflicted.Status.Conditions, infrav1alpha1.ConditionReady); cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != infrav1alpha1.ReasonIdentityConflict {
		t.Fatalf("identity conflict condition = %+v", cond)
	}

	// Deletion must still dispatch through the recorded stub backend and pass
	// its original backend/CRD identity, despite the mutable fake update.
	if err := r.Client.Delete(ctx, &conflicted); err != nil {
		t.Fatalf("delete conflicted template: %v", err)
	}
	reconcileUntilSettled(t, r, tmpl.Name)
	if len(stb.SeenTeardowns) == 0 {
		t.Fatal("recorded backend did not receive teardown")
	}
	cleanup := stb.SeenTeardownTemplates[len(stb.SeenTeardownTemplates)-1]
	if cleanup.Spec.Backend != stub.Name {
		t.Fatalf("teardown backend = %q, want recorded %q", cleanup.Spec.Backend, stub.Name)
	}
	if cleanup.Spec.InstanceCRD != *provisioned.Status.InstanceCRD {
		t.Fatalf("teardown instanceCRD = %+v, want recorded %+v", cleanup.Spec.InstanceCRD, *provisioned.Status.InstanceCRD)
	}
}

func TestReconcileDeleteRetainsFinalizerWhenRecordedBackendMissing(t *testing.T) {
	ctx := context.Background()
	tmpl := newTestTemplate(t, "missing-cleanup")
	r, _ := newTestReconciler(t, tmpl)
	reconcileUntilSettled(t, r, tmpl.Name)

	var provisioned infrav1alpha1.Template
	if err := r.Client.Get(ctx, types.NamespacedName{Name: tmpl.Name}, &provisioned); err != nil {
		t.Fatalf("get provisioned template: %v", err)
	}
	if err := r.Client.Delete(ctx, &provisioned); err != nil {
		t.Fatalf("delete template: %v", err)
	}
	// Simulate a provider restart/configuration where the recorded backend is
	// unavailable. Cleanup must remain pending instead of releasing the
	// finalizer and orphaning backend-owned resources.
	r.Backends = backend.NewRegistry()
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: tmpl.Name}})
	if err == nil {
		t.Fatal("missing backend cleanup unexpectedly succeeded")
	}
	var pending infrav1alpha1.Template
	if err := r.Client.Get(ctx, types.NamespacedName{Name: tmpl.Name}, &pending); err != nil {
		t.Fatalf("get pending template: %v", err)
	}
	if !controllerutil.ContainsFinalizer(&pending, infrav1alpha1.FinalizerTemplateReconcile) {
		t.Fatalf("missing backend cleanup released finalizer: %v", pending.Finalizers)
	}
	if cond := findCondition(pending.Status.Conditions, infrav1alpha1.ConditionReady); cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != infrav1alpha1.ReasonBackendNotFound {
		t.Fatalf("missing backend condition = %+v", cond)
	}
}

func TestReconcileDeleteWithoutRecordedIdentityUsesCurrentBackend(t *testing.T) {
	ctx := context.Background()
	tmpl := newTestTemplate(t, "unrecorded-cleanup")
	tmpl.Finalizers = []string{infrav1alpha1.FinalizerTemplateReconcile}
	r, stb := newTestReconciler(t, tmpl)

	// Simulate a process that added its finalizer and then crashed before the
	// first ownership status patch. Teardown is the conservative, idempotent
	// recovery path for that ambiguous state.
	if err := r.Client.Delete(ctx, tmpl); err != nil {
		t.Fatalf("delete template: %v", err)
	}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: tmpl.Name}}); err != nil {
		t.Fatalf("unrecorded cleanup reconcile: %v", err)
	}
	if len(stb.SeenTeardownTemplates) != 1 {
		t.Fatalf("teardown calls = %d, want 1", len(stb.SeenTeardownTemplates))
	}
	if got := stb.SeenTeardownTemplates[0].Spec.Backend; got != stub.Name {
		t.Fatalf("teardown backend = %q, want %q", got, stub.Name)
	}
	var gone infrav1alpha1.Template
	if err := r.Client.Get(ctx, types.NamespacedName{Name: tmpl.Name}, &gone); err == nil {
		t.Fatal("template still exists after conservative cleanup")
	}
}

func TestReconcileDeleteWithoutRecordedIdentityRetainsFinalizerWhenCurrentBackendMissing(t *testing.T) {
	ctx := context.Background()
	tmpl := newTestTemplate(t, "unrecorded-missing-cleanup")
	tmpl.Finalizers = []string{infrav1alpha1.FinalizerTemplateReconcile}
	r, _ := newTestReconciler(t, tmpl)
	if err := r.Client.Delete(ctx, tmpl); err != nil {
		t.Fatalf("delete template: %v", err)
	}
	r.Backends = backend.NewRegistry()
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: tmpl.Name}}); err == nil {
		t.Fatal("missing current backend cleanup unexpectedly succeeded")
	}
	var pending infrav1alpha1.Template
	if err := r.Client.Get(ctx, types.NamespacedName{Name: tmpl.Name}, &pending); err != nil {
		t.Fatalf("get pending template: %v", err)
	}
	if !controllerutil.ContainsFinalizer(&pending, infrav1alpha1.FinalizerTemplateReconcile) {
		t.Fatalf("missing current backend cleanup released finalizer: %v", pending.Finalizers)
	}
}

func TestReconcileRecordsIdentityBeforeBackendReady(t *testing.T) {
	ctx := context.Background()
	tmpl := newTestTemplate(t, "partial")
	r, stb := newTestReconciler(t, tmpl)
	stb.FailSetup = true

	// The backend has accepted the handoff but is not ready. The controller
	// must still record enough ownership for a later delete to dispatch the
	// matching teardown instead of treating this as an unowned finalizer.
	reconcileUntilSettled(t, r, tmpl.Name)
	var pending infrav1alpha1.Template
	if err := r.Client.Get(ctx, types.NamespacedName{Name: tmpl.Name}, &pending); err != nil {
		t.Fatalf("get partially setup template: %v", err)
	}
	if pending.Status.Backend.Name != stub.Name || pending.Status.InstanceCRD == nil {
		t.Fatalf("partial setup lost identity: backend=%q instanceCRD=%+v", pending.Status.Backend.Name, pending.Status.InstanceCRD)
	}
	if cond := findCondition(pending.Status.Conditions, infrav1alpha1.ConditionReady); cond == nil || cond.Status != metav1.ConditionFalse {
		t.Fatalf("partial setup Ready condition = %+v", cond)
	}

	stb.FailSetup = false
	if err := r.Client.Delete(ctx, &pending); err != nil {
		t.Fatalf("delete partially setup template: %v", err)
	}
	reconcileUntilSettled(t, r, tmpl.Name)
	if len(stb.SeenTeardownTemplates) == 0 {
		t.Fatal("partial setup cleanup did not call teardown")
	}
	cleanup := stb.SeenTeardownTemplates[len(stb.SeenTeardownTemplates)-1]
	if cleanup.Spec.Backend != stub.Name || cleanup.Spec.InstanceCRD != *pending.Status.InstanceCRD {
		t.Fatalf("partial setup teardown identity = backend %q, instanceCRD %+v; want backend %q, instanceCRD %+v",
			cleanup.Spec.Backend, cleanup.Spec.InstanceCRD, stub.Name, *pending.Status.InstanceCRD)
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
