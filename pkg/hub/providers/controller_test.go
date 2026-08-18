/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package providers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	providersv1alpha1 "github.com/faroshq/faros/apis/providers/v1alpha1"
	"github.com/faroshq/faros/utils/testfakes"
)

type httpDoerFunc func(*http.Request) (*http.Response, error)

func (f httpDoerFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

type apiExportCheckerFunc func(context.Context, string, string, []APIExportResource, []PermissionClaim) error

func (f apiExportCheckerFunc) CheckAPIExport(ctx context.Context, path, name string, resources []APIExportResource, claims []PermissionClaim) error {
	return f(ctx, path, name, resources, claims)
}

type catalogWorkspaceOwnerFunc func(context.Context, string, bool) (string, error)

func (f catalogWorkspaceOwnerFunc) ResolveCatalogEntryOwnerCluster(ctx context.Context, name string, builtin bool) (string, error) {
	return f(ctx, name, builtin)
}

func healthyHTTPDoer() httpDoer {
	return httpDoerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"status":"ready"}`)),
			Header:     make(http.Header),
		}, nil
	})
}

func newProviderTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatalf("adding core scheme: %v", err)
	}
	if err := providersv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("adding providers scheme: %v", err)
	}
	return s
}

func registerAppStudioBuiltin(t *testing.T) {
	t.Helper()
	if _, ok := BuiltinByName("app-studio"); ok {
		return
	}
	RegisterBuiltin(BuiltinSpec{
		Name:          "app-studio",
		DisplayName:   "App Studio",
		LocalUIAssets: fstest.MapFS{"main.js": &fstest.MapFile{Data: []byte("bundle")}},
	})
}

func TestCatalogReconcilerRejectsSpoofedConsumerClusterLifecycle(t *testing.T) {
	reg := NewRegistry()
	scheme := newProviderTestScheme(t)
	entry := &providersv1alpha1.CatalogEntry{
		ObjectMeta: metav1.ObjectMeta{Name: "code"},
		Spec: providersv1alpha1.CatalogEntrySpec{
			DisplayName: "Spoof",
			UI:          &providersv1alpha1.ProviderUI{URL: "http://attacker.invalid"},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&providersv1alpha1.CatalogEntry{}).
		WithObjects(entry).
		Build()
	r := &CatalogReconciler{
		mgr: testfakes.NewManager(c), reg: reg, noKCP: false,
		healthClient: healthyHTTPDoer(),
		workspaceOwner: catalogWorkspaceOwnerFunc(func(_ context.Context, name string, builtin bool) (string, error) {
			if name != "code" || builtin {
				t.Fatalf("owner lookup = (%q, %t), want (code, false)", name, builtin)
			}
			return "provider-cluster", nil
		}),
	}

	// A CatalogEntry with a real provider's name in another APIExport consumer
	// must not create a route.
	if _, err := r.Reconcile(context.Background(), testfakes.NewRequest("attacker-cluster", "", "code")); err != nil {
		t.Fatalf("reconcile spoofed create: %v", err)
	}
	if _, ok := reg.Get("code"); ok {
		t.Fatal("spoofed create entered the registry")
	}

	var current providersv1alpha1.CatalogEntry
	if err := c.Get(context.Background(), types.NamespacedName{Name: "code"}, &current); err != nil {
		t.Fatalf("get entry: %v", err)
	}
	current.Spec.DisplayName = "Code"
	current.Spec.UI.URL = "http://code.invalid"
	if err := c.Update(context.Background(), &current); err != nil {
		t.Fatalf("update authoritative entry: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), testfakes.NewRequest("provider-cluster", "", "code")); err != nil {
		t.Fatalf("reconcile authoritative create: %v", err)
	}
	got, ok := reg.Get("code")
	if !ok || got.UIURL == nil || got.UIURL.String() != "http://code.invalid" {
		t.Fatalf("authoritative route = found %t, %#v", ok, got.UIURL)
	}

	// A spoofed update cannot replace the authoritative route.
	if err := c.Get(context.Background(), types.NamespacedName{Name: "code"}, &current); err != nil {
		t.Fatalf("get entry for spoofed update: %v", err)
	}
	current.Spec.UI.URL = "http://attacker.invalid"
	if err := c.Update(context.Background(), &current); err != nil {
		t.Fatalf("write spoofed update fixture: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), testfakes.NewRequest("attacker-cluster", "", "code")); err != nil {
		t.Fatalf("reconcile spoofed update: %v", err)
	}
	got, _ = reg.Get("code")
	if got.UIURL == nil || got.UIURL.String() != "http://code.invalid" {
		t.Fatalf("spoofed update replaced route with %v", got.UIURL)
	}

	if err := c.Delete(context.Background(), &current); err != nil {
		t.Fatalf("delete entry fixture: %v", err)
	}
	if _, err := r.Reconcile(context.Background(), testfakes.NewRequest("attacker-cluster", "", "code")); err != nil {
		t.Fatalf("reconcile spoofed delete: %v", err)
	}
	if _, ok := reg.Get("code"); !ok {
		t.Fatal("spoofed delete removed the authoritative route")
	}
	if _, err := r.Reconcile(context.Background(), testfakes.NewRequest("provider-cluster", "", "code")); err != nil {
		t.Fatalf("reconcile authoritative delete: %v", err)
	}
	if _, ok := reg.Get("code"); ok {
		t.Fatal("authoritative delete left the route registered")
	}
}

func TestCatalogReconcilerClusterLookupFailureRemovesOnlyOwnedRoute(t *testing.T) {
	reg := NewRegistry()
	reg.Upsert(Provider{Name: "code", CatalogEntryCluster: "provider-cluster"})

	mgr := testfakes.NewManager(nil)
	mgr.GetClusterErr = fmt.Errorf("logical cluster no longer exists")
	r := &CatalogReconciler{mgr: mgr, reg: reg}

	if _, err := r.Reconcile(context.Background(), testfakes.NewRequest("attacker-cluster", "", "code")); err == nil {
		t.Fatal("spoofed cluster lookup failure returned nil error")
	}
	if _, ok := reg.Get("code"); !ok {
		t.Fatal("spoofed cluster lookup failure removed authoritative route")
	}

	if _, err := r.Reconcile(context.Background(), testfakes.NewRequest("provider-cluster", "", "code")); err == nil {
		t.Fatal("authoritative cluster lookup failure returned nil error")
	}
	if _, ok := reg.Get("code"); ok {
		t.Fatal("unavailable authoritative cluster left its route registered")
	}
}

func TestCatalogReconcilerRejectsUnregisteredBuiltinAnnotation(t *testing.T) {
	reg := NewRegistry()
	scheme := newProviderTestScheme(t)
	entry := &providersv1alpha1.CatalogEntry{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "spoofed-builtin",
			Annotations: map[string]string{catalogBuiltinAnnotation: "true"},
		},
		Spec: providersv1alpha1.CatalogEntrySpec{
			UI: &providersv1alpha1.ProviderUI{URL: "http://attacker.invalid"},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&providersv1alpha1.CatalogEntry{}).
		WithObjects(entry).
		Build()
	r := &CatalogReconciler{
		mgr: testfakes.NewManager(c), reg: reg,
		workspaceOwner: catalogWorkspaceOwnerFunc(func(context.Context, string, bool) (string, error) {
			t.Fatal("unknown builtin reached workspace authority lookup")
			return "", nil
		}),
	}
	if _, err := r.Reconcile(context.Background(), testfakes.NewRequest("system-providers", "", entry.Name)); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, ok := reg.Get(entry.Name); ok {
		t.Fatal("unregistered builtin annotation entered registry")
	}
}

func TestCatalogReconciler_PreservesChartOwnedUIRoutingForBuiltinName(t *testing.T) {
	registerAppStudioBuiltin(t)
	reg := NewRegistry()
	scheme := newProviderTestScheme(t)
	entry := &providersv1alpha1.CatalogEntry{
		ObjectMeta: metav1.ObjectMeta{Name: "app-studio"},
		Spec: providersv1alpha1.CatalogEntrySpec{
			DisplayName: "App Studio from Chart",
			Description: "Persistent AI project workspace.",
			Dependencies: []providersv1alpha1.ProviderDependency{
				{Name: "code"},
			},
			UI: &providersv1alpha1.ProviderUI{
				URL: "http://app-studio.invalid",
			},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&providersv1alpha1.CatalogEntry{}).
		WithObjects(entry).
		Build()

	r := &CatalogReconciler{mgr: testfakes.NewManager(c), reg: reg, noKCP: true, healthClient: healthyHTTPDoer()}
	if _, err := r.Reconcile(context.Background(), testfakes.NewRequest("cluster", "", "app-studio")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got, ok := reg.Get("app-studio")
	if !ok {
		t.Fatal("expected app-studio in registry")
	}
	if got.UIURL == nil || got.UIURL.String() != "http://app-studio.invalid" {
		t.Fatalf("UIURL = %v, want http://app-studio.invalid", got.UIURL)
	}
	if got.LocalUIAssets != nil {
		t.Fatal("expected chart-owned provider to keep proxy routing, not embedded assets")
	}
	if len(got.Dependencies) != 1 || got.Dependencies[0].Name != "code" {
		t.Fatalf("Dependencies = %#v, want [code]", got.Dependencies)
	}
	// The portal's catalog cards and first-run welcome flow render this; if the
	// reconciler drops it, both fall back to showing a bare provider name.
	if got.Description != "Persistent AI project workspace." {
		t.Fatalf("Description = %q, want the spec value", got.Description)
	}
	if !got.EndpointsValid {
		t.Fatal("expected endpoints to be valid when ui.url is present")
	}

	var updated providersv1alpha1.CatalogEntry
	if err := c.Get(context.Background(), types.NamespacedName{Name: "app-studio"}, &updated); err != nil {
		t.Fatalf("get updated entry: %v", err)
	}
	if updated.Status.Endpoints == nil || updated.Status.Endpoints.UI != "http://app-studio.invalid" {
		t.Fatalf("status endpoints = %#v, want UI=http://app-studio.invalid", updated.Status.Endpoints)
	}
}

func TestCatalogReconcilerRejectsInvalidActionDeclarations(t *testing.T) {
	reg := NewRegistry()
	scheme := newProviderTestScheme(t)
	entry := &providersv1alpha1.CatalogEntry{
		ObjectMeta: metav1.ObjectMeta{Name: "invalid-actions"},
		Spec: providersv1alpha1.CatalogEntrySpec{
			DisplayName: "Invalid actions",
			UI:          &providersv1alpha1.ProviderUI{URL: "http://provider.invalid"},
			Actions: []providersv1alpha1.ProviderActionSpec{{
				ID:          "query_table/latest",
				DisplayName: "Invalid action",
			}},
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&providersv1alpha1.CatalogEntry{}).
		WithObjects(entry).
		Build()

	r := &CatalogReconciler{mgr: testfakes.NewManager(c), reg: reg, noKCP: true, healthClient: healthyHTTPDoer()}
	if _, err := r.Reconcile(context.Background(), testfakes.NewRequest("cluster", "", "invalid-actions")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, ok := reg.Get("invalid-actions"); ok {
		t.Fatal("invalid action declaration must not enter the provider registry")
	}

	var updated providersv1alpha1.CatalogEntry
	if err := c.Get(context.Background(), types.NamespacedName{Name: "invalid-actions"}, &updated); err != nil {
		t.Fatalf("get updated entry: %v", err)
	}
	if len(updated.Status.Conditions) != 1 {
		t.Fatalf("conditions = %#v, want one Ready condition", updated.Status.Conditions)
	}
	condition := updated.Status.Conditions[0]
	if condition.Type != "Ready" || condition.Status != metav1.ConditionFalse || condition.Reason != "InvalidActions" {
		t.Fatalf("condition = %#v, want Ready=False/InvalidActions", condition)
	}
}

func TestCatalogReconcilerOmitsInvalidAssistantSkillAndKeepsValidSibling(t *testing.T) {
	reg := NewRegistry()
	scheme := newProviderTestScheme(t)
	valid := providersv1alpha1.ProviderAssistantSkillSpec{
		PackageName: "valid",
		Version:     "1.0.0",
		Skill:       "---\nname: valid\ndescription: valid guidance\n---\nbody\n",
	}
	digest, err := providersv1alpha1.ProviderAssistantSkillDigest(valid)
	if err != nil {
		t.Fatalf("skill digest: %v", err)
	}
	valid.Digest = digest
	invalid := valid
	invalid.PackageName = "invalid"
	invalid.Skill += "tampered"
	entry := &providersv1alpha1.CatalogEntry{
		ObjectMeta: metav1.ObjectMeta{Name: "skills"},
		Spec: providersv1alpha1.CatalogEntrySpec{
			DisplayName: "Skills",
			UI:          &providersv1alpha1.ProviderUI{URL: "http://skills.invalid"},
			AssistantSkills: []providersv1alpha1.ProviderAssistantSkillSpec{
				invalid,
				valid,
			},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&providersv1alpha1.CatalogEntry{}).
		WithObjects(entry).
		Build()

	r := &CatalogReconciler{mgr: testfakes.NewManager(c), reg: reg, noKCP: true, healthClient: healthyHTTPDoer()}
	if _, err := r.Reconcile(context.Background(), testfakes.NewRequest("cluster", "", "skills")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, ok := reg.Get("skills")
	if !ok {
		t.Fatal("expected provider in registry")
	}
	if len(got.AssistantSkills) != 1 || got.AssistantSkills[0].PackageName != "valid" || got.AssistantSkills[0].Digest != digest {
		t.Fatalf("assistant skills = %#v, want only valid sibling", got.AssistantSkills)
	}
}

// Every replica runs the catalog reconciler, so an unconditional status write
// is a cross-replica write storm: each Update bumps the resource version, every
// peer's watch fires, and they all write again. A steady-state reconcile must
// therefore leave the object untouched.
func TestCatalogReconcilerDoesNotRewriteUnchangedStatus(t *testing.T) {
	reg := NewRegistry()
	scheme := newProviderTestScheme(t)
	entry := &providersv1alpha1.CatalogEntry{
		ObjectMeta: metav1.ObjectMeta{Name: "cost"},
		Spec: providersv1alpha1.CatalogEntrySpec{
			DisplayName: "Cost",
			Backend:     &providersv1alpha1.ProviderBackend{URL: "http://cost.invalid"},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&providersv1alpha1.CatalogEntry{}).
		WithObjects(entry).
		Build()

	r := &CatalogReconciler{mgr: testfakes.NewManager(c), reg: reg, noKCP: true, healthClient: healthyHTTPDoer()}
	req := testfakes.NewRequest("cluster", "", "cost")
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}

	var afterFirst providersv1alpha1.CatalogEntry
	if err := c.Get(context.Background(), types.NamespacedName{Name: "cost"}, &afterFirst); err != nil {
		t.Fatalf("get after first reconcile: %v", err)
	}

	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	var afterSecond providersv1alpha1.CatalogEntry
	if err := c.Get(context.Background(), types.NamespacedName{Name: "cost"}, &afterSecond); err != nil {
		t.Fatalf("get after second reconcile: %v", err)
	}

	if afterFirst.ResourceVersion != afterSecond.ResourceVersion {
		t.Fatalf("status rewritten with no change: resourceVersion %s -> %s",
			afterFirst.ResourceVersion, afterSecond.ResourceVersion)
	}
}

// The status heartbeat is how a beat served by one replica reaches the others;
// the reconciler has to carry it into the registry.
func TestCatalogReconcilerAdoptsStatusHeartbeat(t *testing.T) {
	beat := metav1.NewTime(time.Now().Add(-time.Second).Truncate(time.Second))
	reg := NewRegistry()
	scheme := newProviderTestScheme(t)
	entry := &providersv1alpha1.CatalogEntry{
		ObjectMeta: metav1.ObjectMeta{Name: "cost"},
		Spec: providersv1alpha1.CatalogEntrySpec{
			Backend: &providersv1alpha1.ProviderBackend{URL: "http://cost.invalid"},
		},
		Status: providersv1alpha1.CatalogEntryStatus{
			LastHeartbeat:   &beat,
			ReportedVersion: "v4.5.6",
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&providersv1alpha1.CatalogEntry{}).
		WithObjects(entry).
		Build()

	r := &CatalogReconciler{mgr: testfakes.NewManager(c), reg: reg, noKCP: true, healthClient: healthyHTTPDoer()}
	if _, err := r.Reconcile(context.Background(), testfakes.NewRequest("cluster", "", "cost")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got, ok := reg.Get("cost")
	if !ok {
		t.Fatal("cost missing from registry")
	}
	if !got.LastHeartbeat.Equal(beat.Time) || got.ReportedVersion != "v4.5.6" || !got.HeartbeatRequired {
		t.Fatalf("registry did not adopt status heartbeat: %+v", got)
	}
	if !got.Ready() {
		t.Fatal("provider with a fresh heartbeat should be Ready on every replica")
	}
}

func TestCatalogReconcilerBackendHealthGatesReady(t *testing.T) {
	reg := NewRegistry()
	scheme := newProviderTestScheme(t)
	entry := &providersv1alpha1.CatalogEntry{
		ObjectMeta: metav1.ObjectMeta{Name: "cost"},
		Spec: providersv1alpha1.CatalogEntrySpec{
			Backend: &providersv1alpha1.ProviderBackend{URL: "http://cost.invalid", HealthPath: "/readyz"},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&providersv1alpha1.CatalogEntry{}).
		WithObjects(entry).
		Build()

	r := &CatalogReconciler{
		mgr: testfakes.NewManager(c), reg: reg, noKCP: true,
		healthClient: httpDoerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: io.NopCloser(strings.NewReader("starting")), Header: make(http.Header)}, nil
		}),
	}
	if _, err := r.Reconcile(context.Background(), testfakes.NewRequest("cluster", "", "cost")); err != nil {
		t.Fatalf("reconcile unhealthy: %v", err)
	}
	if got, _ := reg.Get("cost"); got.Ready() || !got.BackendHealthRequired || got.BackendHealthy {
		t.Fatalf("unhealthy registry provider = %+v", got)
	}

	var updated providersv1alpha1.CatalogEntry
	if err := c.Get(context.Background(), types.NamespacedName{Name: "cost"}, &updated); err != nil {
		t.Fatalf("get unhealthy entry: %v", err)
	}
	if conditionStatus(updated.Status.Conditions, "BackendHealthy") != metav1.ConditionFalse ||
		conditionStatus(updated.Status.Conditions, "Ready") != metav1.ConditionFalse {
		t.Fatalf("unhealthy conditions = %#v", updated.Status.Conditions)
	}

	r.healthClient = healthyHTTPDoer()
	if _, err := r.Reconcile(context.Background(), testfakes.NewRequest("cluster", "", "cost")); err != nil {
		t.Fatalf("reconcile healthy: %v", err)
	}
	if got, _ := reg.Get("cost"); !got.Ready() || !got.BackendHealthy {
		t.Fatalf("healthy registry provider = %+v", got)
	}
}

func TestCatalogReconcilerAPIExportOnlyProviderIsReady(t *testing.T) {
	reg := NewRegistry()
	scheme := newProviderTestScheme(t)
	entry := &providersv1alpha1.CatalogEntry{
		ObjectMeta: metav1.ObjectMeta{
			Name: "database", Generation: 3,
			Annotations: map[string]string{AcceptUntrustedClaimsAnnotation: "true"},
		},
		Spec: providersv1alpha1.CatalogEntrySpec{
			APIExport: &providersv1alpha1.ProviderAPIExport{
				Name: "database.providers.faros.sh",
				RequiredResources: []providersv1alpha1.ProviderAPIExportResource{
					{Group: "database.providers.faros.sh", Name: "databases"},
				},
				PermissionClaims: []providersv1alpha1.ProviderPermissionClaim{{
					Group: "infrastructure.faros.sh", Resource: "applications",
					IdentitySource: &providersv1alpha1.ProviderPermissionClaimIdentitySource{Kind: "Provider", Provider: "infrastructure"},
				}},
			},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&providersv1alpha1.CatalogEntry{}).
		WithObjects(entry).
		Build()

	var checkedPath, checkedName string
	var checkedResources []APIExportResource
	var checkedClaims []PermissionClaim
	r := &CatalogReconciler{
		mgr: testfakes.NewManager(c), reg: reg, noKCP: true,
		exportChecker: apiExportCheckerFunc(func(_ context.Context, path, name string, resources []APIExportResource, claims []PermissionClaim) error {
			checkedPath, checkedName = path, name
			checkedResources = append([]APIExportResource(nil), resources...)
			checkedClaims = append([]PermissionClaim(nil), claims...)
			return nil
		}),
	}
	result, err := r.Reconcile(context.Background(), testfakes.NewRequest("cluster", "", "database"))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if checkedPath != "root:faros:providers:database" || checkedName != "database.providers.faros.sh" {
		t.Fatalf("checked APIExport = %s/%s", checkedPath, checkedName)
	}
	if len(checkedResources) != 1 || checkedResources[0] != (APIExportResource{Group: "database.providers.faros.sh", Name: "databases"}) {
		t.Fatalf("checked required resources = %#v", checkedResources)
	}
	if len(checkedClaims) != 1 || checkedClaims[0].IdentitySourceKind != "Provider" || checkedClaims[0].IdentitySourceProvider != "infrastructure" {
		t.Fatalf("checked permission claim identity source = %#v", checkedClaims)
	}
	if result.RequeueAfter != SweepInterval {
		t.Fatalf("RequeueAfter = %v, want %v", result.RequeueAfter, SweepInterval)
	}

	got, ok := reg.Get("database")
	if !ok || !got.Ready() {
		t.Fatalf("APIExport-only provider is not Ready: found=%v provider=%+v", ok, got)
	}
	if !got.AllowUntrustedClaims {
		t.Fatal("catalog owner untrusted-claim approval was not projected into the registry")
	}
	if got.RuntimeReady() {
		t.Fatal("APIExport-only provider was marked routable")
	}
	var updated providersv1alpha1.CatalogEntry
	if err := c.Get(context.Background(), types.NamespacedName{Name: "database"}, &updated); err != nil {
		t.Fatalf("get updated entry: %v", err)
	}
	ready := conditionByType(updated.Status.Conditions, "Ready")
	if ready == nil || ready.Status != metav1.ConditionTrue || ready.ObservedGeneration != 3 {
		t.Fatalf("Ready condition = %#v", ready)
	}
	if conditionStatus(updated.Status.Conditions, "APIExportReady") != metav1.ConditionTrue {
		t.Fatalf("APIExportReady condition = %#v", conditionByType(updated.Status.Conditions, "APIExportReady"))
	}
}

func TestCatalogReconcilerAPIExportOnlyProviderWaitsForExport(t *testing.T) {
	reg := NewRegistry()
	scheme := newProviderTestScheme(t)
	entry := &providersv1alpha1.CatalogEntry{
		ObjectMeta: metav1.ObjectMeta{Name: "database"},
		Spec: providersv1alpha1.CatalogEntrySpec{
			APIExport: &providersv1alpha1.ProviderAPIExport{Name: "database.providers.faros.sh"},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&providersv1alpha1.CatalogEntry{}).
		WithObjects(entry).
		Build()

	r := &CatalogReconciler{
		mgr: testfakes.NewManager(c), reg: reg, noKCP: true,
		exportChecker: apiExportCheckerFunc(func(context.Context, string, string, []APIExportResource, []PermissionClaim) error {
			return fmt.Errorf("APIExport does not exist")
		}),
	}
	result, err := r.Reconcile(context.Background(), testfakes.NewRequest("cluster", "", "database"))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.RequeueAfter != SweepInterval {
		t.Fatalf("RequeueAfter = %v, want %v", result.RequeueAfter, SweepInterval)
	}
	if got, ok := reg.Get("database"); !ok || got.Ready() || got.APIExportReady {
		t.Fatalf("unavailable APIExport provider = found=%v provider=%+v", ok, got)
	}

	var updated providersv1alpha1.CatalogEntry
	if err := c.Get(context.Background(), types.NamespacedName{Name: "database"}, &updated); err != nil {
		t.Fatalf("get updated entry: %v", err)
	}
	ready := conditionByType(updated.Status.Conditions, "Ready")
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "APIExportUnavailable" {
		t.Fatalf("Ready condition = %#v", ready)
	}
	exportReady := conditionByType(updated.Status.Conditions, "APIExportReady")
	if exportReady == nil || exportReady.Status != metav1.ConditionFalse || !strings.Contains(exportReady.Message, "does not exist") {
		t.Fatalf("APIExportReady condition = %#v", exportReady)
	}
}

func TestValidateAPIExportReady(t *testing.T) {
	valid := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "database.providers.faros.sh"},
		"spec": map[string]any{
			"resources": []any{
				map[string]any{"group": "database.example.io", "name": "databases", "schema": "v1alpha1.databases.database.example.io"},
			},
			"permissionClaims": []any{
				map[string]any{"group": "tenancy.faros.sh", "resource": "organizations", "identityHash": "claim-id"},
				map[string]any{"group": "authentication.k8s.io", "resource": "tokenreviews"},
			},
		},
		"status": map[string]any{
			"identityHash": "export-id",
			"conditions":   []any{map[string]any{"type": "IdentityValid", "status": "True"}},
		},
	}}
	claims := []PermissionClaim{
		{Group: "tenancy.faros.sh", Resource: "organizations", ExpectedIdentityHash: "claim-id"},
		{Group: "authentication.k8s.io", Resource: "tokenreviews"},
	}
	required := []APIExportResource{{Group: "database.example.io", Name: "databases"}}
	if _, err := validateAPIExportReady(valid, required, claims); err != nil {
		t.Fatalf("valid APIExport rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*unstructured.Unstructured)
		want   string
	}{
		{
			name: "missing export identity",
			mutate: func(export *unstructured.Unstructured) {
				_ = unstructured.SetNestedField(export.Object, "", "status", "identityHash")
			},
			want: "status.identityHash",
		},
		{
			name: "identity condition false",
			mutate: func(export *unstructured.Unstructured) {
				_ = unstructured.SetNestedSlice(export.Object, []any{map[string]any{"type": "IdentityValid", "status": "False", "reason": "VerificationFailed"}}, "status", "conditions")
			},
			want: "VerificationFailed",
		},
		{
			name: "first party claim has no identity",
			mutate: func(export *unstructured.Unstructured) {
				_ = unstructured.SetNestedSlice(export.Object, []any{map[string]any{"group": "tenancy.faros.sh", "resource": "organizations"}}, "spec", "permissionClaims")
			},
			want: "permission claim tenancy.faros.sh/organizations",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			export := valid.DeepCopy()
			tt.mutate(export)
			_, err := validateAPIExportReady(export, required, claims)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestCatalogReconcilerPublishesBackendlessHeartbeatExpiry(t *testing.T) {
	beat := metav1.NewTime(time.Now().Add(-HeartbeatTTL - time.Second))
	reg := NewRegistry()
	scheme := newProviderTestScheme(t)
	entry := &providersv1alpha1.CatalogEntry{
		ObjectMeta: metav1.ObjectMeta{Name: "ui-provider", Generation: 4},
		Spec: providersv1alpha1.CatalogEntrySpec{
			UI: &providersv1alpha1.ProviderUI{URL: "http://ui-provider.invalid"},
		},
		Status: providersv1alpha1.CatalogEntryStatus{LastHeartbeat: &beat},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&providersv1alpha1.CatalogEntry{}).
		WithObjects(entry).
		Build()

	r := &CatalogReconciler{mgr: testfakes.NewManager(c), reg: reg, noKCP: true}
	result, err := r.Reconcile(context.Background(), testfakes.NewRequest("cluster", "", "ui-provider"))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.RequeueAfter != SweepInterval {
		t.Fatalf("RequeueAfter = %v, want %v", result.RequeueAfter, SweepInterval)
	}
	var updated providersv1alpha1.CatalogEntry
	if err := c.Get(context.Background(), types.NamespacedName{Name: "ui-provider"}, &updated); err != nil {
		t.Fatalf("get updated entry: %v", err)
	}
	ready := conditionByType(updated.Status.Conditions, "Ready")
	if ready == nil || ready.Status != metav1.ConditionFalse || ready.Reason != "HeartbeatStale" {
		t.Fatalf("Ready condition = %#v", ready)
	}
}

func TestSetConditionUpdatesObservedGenerationWithoutTransition(t *testing.T) {
	transition := metav1.NewTime(time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC))
	reconciledAt := metav1.NewTime(transition.Add(time.Minute))
	conditions := []metav1.Condition{{
		Type: "Ready", Status: metav1.ConditionTrue, Reason: "Ready", Message: "ready",
		ObservedGeneration: 1, LastTransitionTime: transition,
	}}

	setCondition(&conditions, metav1.Condition{
		Type: "Ready", Status: metav1.ConditionTrue, Reason: "Reconciled", Message: "still ready",
		ObservedGeneration: 2, LastTransitionTime: reconciledAt,
	})
	got := conditions[0]
	if got.ObservedGeneration != 2 || got.Reason != "Reconciled" {
		t.Fatalf("condition was not refreshed: %#v", got)
	}
	if !got.LastTransitionTime.Equal(&transition) {
		t.Fatalf("LastTransitionTime = %v, want preserved %v", got.LastTransitionTime, transition)
	}

	transitionedAt := metav1.NewTime(reconciledAt.Add(time.Minute))
	setCondition(&conditions, metav1.Condition{
		Type: "Ready", Status: metav1.ConditionFalse, Reason: "Failed", Message: "not ready",
		ObservedGeneration: 3, LastTransitionTime: transitionedAt,
	})
	if !conditions[0].LastTransitionTime.Equal(&transitionedAt) {
		t.Fatalf("status transition time = %v, want %v", conditions[0].LastTransitionTime, transitionedAt)
	}
}

func TestProbeBackendHealthUsesSameAuthorityAndBoundedPath(t *testing.T) {
	var requested string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requested = r.URL.RequestURI()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	backend, err := url.Parse(server.URL + "/services/provider")
	if err != nil {
		t.Fatalf("parse backend URL: %v", err)
	}
	if err := probeBackendHealth(context.Background(), server.Client(), backend, "/readyz?full=1"); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if requested != "/services/provider/readyz?full=1" {
		t.Fatalf("request URI = %q, want /services/provider/readyz?full=1", requested)
	}
	backend.Path = "/services/provider/"
	if err := probeBackendHealth(context.Background(), server.Client(), backend, "readyz"); err != nil {
		t.Fatalf("probe relative health path: %v", err)
	}
	if requested != "/services/provider/readyz" {
		t.Fatalf("relative health request URI = %q, want /services/provider/readyz", requested)
	}
	for _, healthPath := range []string{"https://attacker.invalid/healthz", "//attacker.invalid/healthz", "/../admin", "/%2e%2e/admin"} {
		if err := probeBackendHealth(context.Background(), server.Client(), backend, healthPath); err == nil {
			t.Fatalf("healthPath %q was accepted", healthPath)
		}
	}
	ftpBackend, err := url.Parse("ftp://provider.invalid")
	if err != nil {
		t.Fatalf("parse ftp URL: %v", err)
	}
	if err := probeBackendHealth(context.Background(), server.Client(), ftpBackend, "/healthz"); err == nil {
		t.Fatal("non-HTTP backend was accepted")
	}
}

func conditionStatus(conditions []metav1.Condition, conditionType string) metav1.ConditionStatus {
	condition := conditionByType(conditions, conditionType)
	if condition != nil {
		return condition.Status
	}
	return metav1.ConditionUnknown
}

func conditionByType(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}
