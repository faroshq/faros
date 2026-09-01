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

package instance

import (
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	infrav1alpha1 "github.com/faroshq/provider-infrastructure/apis/v1alpha1"
)

func testDevelopmentService(name string) *infrav1alpha1.DevelopmentService {
	enabled := true
	return &infrav1alpha1.DevelopmentService{
		ObjectMeta: metav1.ObjectMeta{Name: name, Generation: 1},
		Spec: infrav1alpha1.DevelopmentServiceSpec{
			ProjectRef:  infrav1alpha1.DevelopmentServiceProjectReference{Name: "project", UID: "project-uid"},
			Environment: "development",
			Enabled:     &enabled,
			Command:     infrav1alpha1.DevelopmentServiceCommand{Argv: []string{"node", "server.js"}},
			Endpoint:    infrav1alpha1.DevelopmentServiceEndpoint{Port: 8080},
			SandboxRef:  infrav1alpha1.DevelopmentServiceSandboxReference{Name: "sandbox", UID: "sandbox-uid"},
		},
	}
}

func TestNormalizeDevelopmentServiceDefaultsAndValidation(t *testing.T) {
	service := testDevelopmentService("web")
	got, err := normalizeDevelopmentService(service)
	if err != nil {
		t.Fatalf("normalizeDevelopmentService() error = %v", err)
	}
	if !got.Enabled || got.Visibility != infrav1alpha1.DevelopmentServiceVisibilityPrivate || got.RestartPolicy != infrav1alpha1.DevelopmentServiceRestartAlways || got.WorkingDir != "." || got.Protocol != "HTTP" {
		t.Fatalf("normalized defaults = %+v", got)
	}

	tests := []struct {
		name   string
		mutate func(*infrav1alpha1.DevelopmentService)
		want   string
	}{
		{name: "environment", mutate: func(s *infrav1alpha1.DevelopmentService) { s.Spec.Environment = "production" }, want: "environment"},
		{name: "project uid", mutate: func(s *infrav1alpha1.DevelopmentService) { s.Spec.ProjectRef.UID = "" }, want: "projectRef.uid"},
		{name: "sandbox uid", mutate: func(s *infrav1alpha1.DevelopmentService) { s.Spec.SandboxRef.UID = "" }, want: "sandboxRef.uid"},
		{name: "argv quota", mutate: func(s *infrav1alpha1.DevelopmentService) {
			s.Spec.Command.Argv = make([]string, developmentServiceMaxArgv+1)
		}, want: "argv"},
		{name: "reserved control port", mutate: func(s *infrav1alpha1.DevelopmentService) { s.Spec.Endpoint.Port = controlPort }, want: "reserved"},
		{name: "health path", mutate: func(s *infrav1alpha1.DevelopmentService) { s.Spec.Endpoint.HealthPath = "ready" }, want: "healthPath"},
		{name: "working directory", mutate: func(s *infrav1alpha1.DevelopmentService) { s.Spec.Command.WorkingDirectory = "../outside" }, want: "workspace"},
		{name: "visibility", mutate: func(s *infrav1alpha1.DevelopmentService) { s.Spec.Exposure.Visibility = "internal" }, want: "visibility"},
		{name: "restart policy", mutate: func(s *infrav1alpha1.DevelopmentService) { s.Spec.RestartPolicy = "Sometimes" }, want: "restartPolicy"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := testDevelopmentService("web")
			test.mutate(candidate)
			if _, err := normalizeDevelopmentService(candidate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("normalize error = %v, want message containing %q", err, test.want)
			}
		})
	}
}

func TestValidateDevelopmentServiceQuotaCountsOnlyMatchingEnabledServices(t *testing.T) {
	ctx := context.Background()
	current := testDevelopmentService("current")
	objects := []ctrlclient.Object{current}
	for i := 0; i < developmentServiceMaxEnabled; i++ {
		service := testDevelopmentService("enabled-" + string(rune('a'+i)))
		objects = append(objects, service)
	}
	disabled := testDevelopmentService("disabled")
	disabledValue := false
	disabled.Spec.Enabled = &disabledValue
	objects = append(objects, disabled)
	otherSandbox := testDevelopmentService("other-sandbox")
	otherSandbox.Spec.SandboxRef.Name = "another-sandbox"
	objects = append(objects, otherSandbox)

	scheme := runtime.NewScheme()
	if err := infrav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add infrastructure scheme: %v", err)
	}
	client := clientfake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	normalized, err := normalizeDevelopmentService(current)
	if err != nil {
		t.Fatalf("normalize current: %v", err)
	}
	if err := validateDevelopmentServiceQuota(ctx, client, current, normalized); err == nil || !strings.Contains(err.Error(), "at most 8") {
		t.Fatalf("quota error = %v, want eight-service limit", err)
	}

	// Disabling one matching service frees exactly one slot; unrelated and
	// disabled services must not affect the count.
	disabledMatching := objects[1].(*infrav1alpha1.DevelopmentService)
	disabledMatching.Spec.Enabled = &disabledValue
	if err := client.Update(ctx, disabledMatching); err != nil {
		t.Fatalf("disable matching service: %v", err)
	}
	if err := validateDevelopmentServiceQuota(ctx, client, current, normalized); err != nil {
		t.Fatalf("quota after disabling one matching service = %v", err)
	}
}

func TestDevelopmentServiceRuntimeNamesAndHostAreStableAndBounded(t *testing.T) {
	name := strings.Repeat("long-name-", 12)
	first := developmentServiceRuntimeNames(name)
	second := developmentServiceRuntimeNames(name)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("runtime names are not stable: %#v != %#v", first, second)
	}
	for _, value := range []string{first.backendService, first.gateDeployment, first.gateService, first.route} {
		if len(value) > 63 || value == "" {
			t.Fatalf("runtime name %q has invalid length", value)
		}
	}
	host, err := developmentServiceHost(name, "tenant", "apps.example.com")
	if err != nil {
		t.Fatalf("developmentServiceHost() error = %v", err)
	}
	if !strings.HasSuffix(host, ".apps.example.com") {
		t.Fatalf("developmentServiceHost() = %q, want apps.example.com suffix", host)
	}
	label := strings.TrimSuffix(host, ".apps.example.com")
	if len(label) > 63 || len(label) == 0 {
		t.Fatalf("developmentServiceHost() label = %q, want bounded DNS label", label)
	}
	if got, err := developmentServiceHost(name, "tenant", "apps.example.com"); err != nil || got != host {
		t.Fatalf("repeated developmentServiceHost() = %q/%v, want %q/nil", got, err, host)
	}
}

func TestEnsureDevelopmentServiceResourcesUsesVisibilityAndRouteContract(t *testing.T) {
	for _, test := range []struct {
		name       string
		visibility infrav1alpha1.DevelopmentServiceVisibility
		hubURL     string
	}{
		{name: "private", visibility: infrav1alpha1.DevelopmentServiceVisibilityPrivate, hubURL: "https://hub.example.com"},
		{name: "public", visibility: infrav1alpha1.DevelopmentServiceVisibilityPublic},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("FAROS_ACCESS_HUB_URL", test.hubURL)
			t.Setenv("FAROS_HUB_URL", "")
			runtimeClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
			controller := &Controller{cfg: Config{Runtime: runtimeClient}}
			service := testDevelopmentService("web")
			spec := normalizedDevelopmentService{
				Argv: []string{"node", "server.js"}, WorkingDir: ".", Protocol: "HTTP", Port: 8080,
				Visibility: test.visibility, RestartPolicy: infrav1alpha1.DevelopmentServiceRestartAlways,
				SandboxName: "sandbox",
			}
			sandbox := sandboxControlRef{RuntimeNamespace: "runtime", WorkloadName: "sandbox-pod"}
			names := developmentServiceRuntimeNames(service.Name)
			labels := developmentServiceLabels(service.Name, sandbox.WorkloadName)
			if err := controller.ensureDevelopmentServiceResources(context.Background(), "tenant", service, spec, sandbox, "web.apps.example.com", "web.apps.example.com", names, labels); err != nil {
				t.Fatalf("ensureDevelopmentServiceResources() error = %v", err)
			}

			backend, err := runtimeClient.Resource(coreServiceGVR).Namespace("runtime").Get(context.Background(), names.backendService, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("get backend Service: %v", err)
			}
			selector, _, err := unstructured.NestedMap(backend.Object, "spec", "selector")
			if err != nil || selector["app"] != "sandbox-pod" {
				t.Fatalf("backend selector = %#v, error %v", selector, err)
			}

			gate, err := runtimeClient.Resource(deploymentGVR).Namespace("runtime").Get(context.Background(), names.gateDeployment, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("get access gate Deployment: %v", err)
			}
			env := deploymentEnv(t, gate)
			if env["FAROS_ACCESS_PROXY_MODE"] != accessProxyVisibility(test.visibility) {
				t.Fatalf("access proxy mode = %q, want %q", env["FAROS_ACCESS_PROXY_MODE"], test.visibility)
			}
			if env["FAROS_HUB_URL"] != test.hubURL {
				t.Fatalf("access proxy hub URL = %q, want %q", env["FAROS_HUB_URL"], test.hubURL)
			}

			route, err := runtimeClient.Resource(httpRouteGVR).Namespace("runtime").Get(context.Background(), names.route, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("get preview HTTPRoute: %v", err)
			}
			hostnames, _, err := unstructured.NestedStringSlice(route.Object, "spec", "hostnames")
			if err != nil || !reflect.DeepEqual(hostnames, []string{"web.apps.example.com"}) {
				t.Fatalf("route hostnames = %#v, error %v", hostnames, err)
			}
			rules, _, err := unstructured.NestedSlice(route.Object, "spec", "rules")
			if err != nil || len(rules) != 1 {
				t.Fatalf("route rules = %#v, error %v", rules, err)
			}
			rule := rules[0].(map[string]any)
			backendRefs := rule["backendRefs"].([]any)
			backendRef := backendRefs[0].(map[string]any)
			if backendRef["name"] != names.gateService || backendRef["port"] != int64(8080) {
				t.Fatalf("route backendRef = %#v", backendRef)
			}
		})
	}
}

func deploymentEnv(t *testing.T, deployment *unstructured.Unstructured) map[string]string {
	t.Helper()
	containers, found, err := unstructured.NestedSlice(deployment.Object, "spec", "template", "spec", "containers")
	if err != nil || !found || len(containers) != 1 {
		t.Fatalf("deployment containers = %#v, found=%v, error=%v", containers, found, err)
	}
	container, ok := containers[0].(map[string]any)
	if !ok {
		t.Fatalf("deployment container has type %T", containers[0])
	}
	envItems, found, err := unstructured.NestedSlice(container, "env")
	if err != nil || !found {
		t.Fatalf("deployment env = %#v, found=%v, error=%v", envItems, found, err)
	}
	result := make(map[string]string, len(envItems))
	for _, raw := range envItems {
		item := raw.(map[string]any)
		name, _ := item["name"].(string)
		value, _ := item["value"].(string)
		result[name] = value
	}
	return result
}

func TestRemoveDevelopmentServiceResourcesIsExact(t *testing.T) {
	names := developmentServiceRuntimeNames("web")
	objects := []runtime.Object{
		runtimeTestObject("v1", "Service", names.backendService, "runtime"),
		runtimeTestObject("v1", "Service", names.gateService, "runtime"),
		runtimeTestObject("apps/v1", "Deployment", names.gateDeployment, "runtime"),
		runtimeTestObject("gateway.networking.k8s.io/v1", "HTTPRoute", names.route, "runtime"),
		runtimeTestObject("v1", "Service", "unrelated", "runtime"),
	}
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), objects...)
	controller := &Controller{cfg: Config{Runtime: client}}
	if err := controller.removeDevelopmentServiceResources(context.Background(), "runtime", names); err != nil {
		t.Fatalf("removeDevelopmentServiceResources() error = %v", err)
	}
	for _, target := range []struct {
		gvr  schema.GroupVersionResource
		name string
	}{{httpRouteGVR, names.route}, {coreServiceGVR, names.gateService}, {deploymentGVR, names.gateDeployment}, {coreServiceGVR, names.backendService}} {
		if _, err := client.Resource(target.gvr).Namespace("runtime").Get(context.Background(), target.name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
			t.Fatalf("runtime object %s/%s still exists or returned wrong error: %v", target.gvr.Resource, target.name, err)
		}
	}
	if _, err := client.Resource(coreServiceGVR).Namespace("runtime").Get(context.Background(), "unrelated", metav1.GetOptions{}); err != nil {
		t.Fatalf("unrelated runtime object was deleted: %v", err)
	}
}

func TestFailClosedDevelopmentServiceRemovesExposureWhenProcessStopFails(t *testing.T) {
	ctx := context.Background()
	names := developmentServiceRuntimeNames("web")
	stopRequests := 0
	controlHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stopRequests++
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/namespaces/runtime/services/sandbox-control:7070/proxy/service" || r.URL.Query().Get("name") != "web" {
			http.Error(w, "unexpected control request", http.StatusBadRequest)
			return
		}
		if stopRequests == 1 {
			http.Error(w, "control plane unavailable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for control server: %v", err)
	}
	controlServer := &http.Server{Handler: controlHandler}
	go func() { _ = controlServer.Serve(listener) }()
	defer func() { _ = controlServer.Shutdown(ctx) }()

	runtimeClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(),
		secretObject("runtime", "sandbox-control-token", map[string]string{
			"token": base64.StdEncoding.EncodeToString([]byte("control-token")),
		}, nil, nil),
		runtimeTestObject("v1", "Service", names.backendService, "runtime"),
		runtimeTestObject("v1", "Service", names.gateService, "runtime"),
		runtimeTestObject("apps/v1", "Deployment", names.gateDeployment, "runtime"),
		runtimeTestObject("gateway.networking.k8s.io/v1", "HTTPRoute", names.route, "runtime"),
		runtimeTestObject("v1", "Service", "unrelated", "runtime"),
	)
	controller := &Controller{cfg: Config{
		Runtime:       runtimeClient,
		RuntimeConfig: &rest.Config{Host: "http://" + listener.Addr().String()},
	}}
	sandbox := sandboxControlRef{
		RuntimeNamespace: "runtime",
		ServiceName:      "sandbox-control",
		ServiceNamespace: "runtime",
		SecretName:       "sandbox-control-token",
		SecretNamespace:  "runtime",
	}

	if err := controller.failClosedDevelopmentService(ctx, sandbox, "runtime", "web"); err == nil || !strings.Contains(err.Error(), "stop service process") {
		t.Fatalf("first failClosedDevelopmentService() error = %v, want stop failure", err)
	}
	if stopRequests != 1 {
		t.Fatalf("stop requests after first cleanup = %d, want 1", stopRequests)
	}
	for _, target := range []struct {
		gvr  schema.GroupVersionResource
		name string
	}{{httpRouteGVR, names.route}, {coreServiceGVR, names.gateService}, {deploymentGVR, names.gateDeployment}, {coreServiceGVR, names.backendService}} {
		if _, err := runtimeClient.Resource(target.gvr).Namespace("runtime").Get(ctx, target.name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
			t.Fatalf("stale runtime object %s/%s after failed process stop: %v", target.gvr.Resource, target.name, err)
		}
	}
	if _, err := runtimeClient.Resource(coreServiceGVR).Namespace("runtime").Get(ctx, "unrelated", metav1.GetOptions{}); err != nil {
		t.Fatalf("unrelated runtime object was deleted: %v", err)
	}

	// A retry must be idempotent: the process stop can succeed after the
	// exposure has already been withdrawn, and no deleted object should block
	// convergence.
	if err := controller.failClosedDevelopmentService(ctx, sandbox, "runtime", "web"); err != nil {
		t.Fatalf("retry failClosedDevelopmentService() error = %v", err)
	}
	if stopRequests != 2 {
		t.Fatalf("stop requests after retry = %d, want 2", stopRequests)
	}
}

func TestResolveSandboxControlChecksUIDAndTemplate(t *testing.T) {
	valid := runtimeSandboxObject("sandbox", "uid-1", infrav1alpha1.UniversalCodingSandboxTemplateName, infrav1alpha1.FarosModeDevelopment)
	client := clientfake.NewClientBuilder().WithObjects(valid).Build()
	controller := &Controller{}
	base := normalizedDevelopmentService{SandboxName: "sandbox", SandboxUID: "uid-1"}

	_, ref, err := controller.resolveSandboxControl(context.Background(), client, "tenant", base)
	if err != nil {
		t.Fatalf("resolve valid sandbox: %v", err)
	}
	if !ref.Ready || ref.RuntimeNamespace != "runtime" || ref.ServiceName != "sandbox-control" || ref.SecretName != "sandbox-control-token" || ref.WorkloadName != "sandbox-pod" {
		t.Fatalf("resolved sandbox ref = %+v", ref)
	}

	for _, test := range []struct {
		name string
		uid  string
		tmpl string
		want string
	}{
		{name: "UID mismatch", uid: "uid-2", tmpl: infrav1alpha1.UniversalCodingSandboxTemplateName, want: "UID"},
		{name: "wrong template", uid: "uid-1", tmpl: "simple-webapp", want: "template"},
		{name: "missing UID", uid: "", tmpl: infrav1alpha1.UniversalCodingSandboxTemplateName, want: "reference UID"},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := runtimeSandboxObject("sandbox", "uid-1", test.tmpl, infrav1alpha1.FarosModeDevelopment)
			candidate.SetUID(types.UID("uid-1"))
			candidateClient := clientfake.NewClientBuilder().WithObjects(candidate).Build()
			candidateSpec := base
			candidateSpec.SandboxUID = test.uid
			if _, _, err := controller.resolveSandboxControl(context.Background(), candidateClient, "tenant", candidateSpec); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("resolve error = %v, want message containing %q", err, test.want)
			}
		})
	}
}

func TestDevelopmentServiceStatusPreservesConditionTransitionAndRequiresCurrentRouteGeneration(t *testing.T) {
	service := testDevelopmentService("web")
	service.Generation = 4
	previous := metav1.NewTime(time.Unix(123, 0))
	service.Status.Conditions = []metav1.Condition{{Type: infrav1alpha1.DevelopmentServiceConditionReady, Status: metav1.ConditionTrue, Reason: "Ready", Message: "ready", ObservedGeneration: 3, LastTransitionTime: previous}}
	status := baseDevelopmentServiceStatus(service)
	setDevelopmentServiceCondition(&status, infrav1alpha1.DevelopmentServiceConditionReady, metav1.ConditionTrue, "Ready", "ready", service.Generation)
	condition := status.Conditions[0]
	if !condition.LastTransitionTime.Time.Equal(previous.Time) || condition.ObservedGeneration != 4 {
		t.Fatalf("unchanged condition = %+v, previous transition=%v", condition, previous)
	}
	setDevelopmentServiceCondition(&status, infrav1alpha1.DevelopmentServiceConditionReady, metav1.ConditionFalse, "NotReady", "stopped", service.Generation)
	if status.Conditions[0].LastTransitionTime.Time.Equal(previous.Time) {
		t.Fatal("condition transition time did not change after status changed")
	}

	route := runtimeTestObject("gateway.networking.k8s.io/v1", "HTTPRoute", "web-preview", "runtime")
	route.SetGeneration(4)
	accepted := map[string]any{
		"parentRef": map[string]any{"group": "gateway.networking.k8s.io", "kind": "Gateway", "name": "faros-gateway", "namespace": "faros-system"},
		"conditions": []any{
			map[string]any{"type": "Accepted", "status": "True", "observedGeneration": int64(4)},
			map[string]any{"type": "ResolvedRefs", "status": "True", "observedGeneration": int64(4)},
		},
	}
	if err := unstructured.SetNestedSlice(route.Object, []any{accepted}, "status", "parents"); err != nil {
		t.Fatalf("set route status: %v", err)
	}
	if !httpRouteAccepted(route, "faros-gateway", "faros-system") {
		t.Fatal("current accepted route was not recognized")
	}
	route.Object["metadata"].(map[string]any)["generation"] = int64(3)
	if httpRouteAccepted(route, "faros-gateway", "faros-system") {
		t.Fatal("stale route status was recognized as accepted")
	}
}

func TestFinalizeDevelopmentServiceDeletesRuntimeObjectsAndPreservesUnrelated(t *testing.T) {
	ctx := context.Background()
	names := developmentServiceRuntimeNames("web")
	objects := []runtime.Object{
		runtimeTestObject("v1", "Service", names.backendService, "runtime"),
		runtimeTestObject("v1", "Service", names.gateService, "runtime"),
		runtimeTestObject("apps/v1", "Deployment", names.gateDeployment, "runtime"),
		runtimeTestObject("gateway.networking.k8s.io/v1", "HTTPRoute", names.route, "runtime"),
		runtimeTestObject("v1", "Service", "unrelated", "runtime"),
	}
	runtimeClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), objects...)
	service := testDevelopmentService("web")
	service.Spec.SandboxRef.Name = "gone"
	service.Status.RuntimeNamespace = "runtime"
	service.Finalizers = []string{infrav1alpha1.DevelopmentServiceFinalizer, "other.example/finalizer"}
	deletionTime := metav1.NewTime(time.Now())
	service.DeletionTimestamp = &deletionTime
	scheme := runtime.NewScheme()
	if err := infrav1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add infrastructure scheme: %v", err)
	}
	tenantClient := clientfake.NewClientBuilder().WithScheme(scheme).WithObjects(service).Build()
	controller := &developmentServiceReconciler{c: &Controller{cfg: Config{Runtime: runtimeClient}}}
	if _, err := controller.finalize(ctx, tenantClient, "tenant", service); err != nil {
		t.Fatalf("finalize() error = %v", err)
	}
	got := &infrav1alpha1.DevelopmentService{}
	if err := tenantClient.Get(ctx, ctrlclient.ObjectKey{Name: service.Name}, got); err != nil {
		t.Fatalf("get finalized DevelopmentService: %v", err)
	}
	if !reflect.DeepEqual(got.Finalizers, []string{"other.example/finalizer"}) {
		t.Fatalf("finalizers after finalize = %#v", got.Finalizers)
	}
	for _, target := range []struct {
		gvr  schema.GroupVersionResource
		name string
	}{{httpRouteGVR, names.route}, {coreServiceGVR, names.gateService}, {deploymentGVR, names.gateDeployment}, {coreServiceGVR, names.backendService}} {
		if _, err := runtimeClient.Resource(target.gvr).Namespace("runtime").Get(ctx, target.name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
			t.Fatalf("runtime object %s/%s remains after finalize: %v", target.gvr.Resource, target.name, err)
		}
	}
	if _, err := runtimeClient.Resource(coreServiceGVR).Namespace("runtime").Get(ctx, "unrelated", metav1.GetOptions{}); err != nil {
		t.Fatalf("unrelated runtime object was deleted: %v", err)
	}
}

func TestMarkInstanceSuspendedIsIdempotent(t *testing.T) {
	ctx := context.Background()
	instance := runtimeTestObject(instanceGVK.GroupVersion().String(), instanceGVK.Kind, "sandbox", "tenant")
	instance.SetGroupVersionKind(instanceGVK)
	client := clientfake.NewClientBuilder().WithObjects(instance).Build()
	changed, err := markInstanceSuspended(ctx, client, instance)
	if err != nil || !changed {
		t.Fatalf("first markInstanceSuspended() = %v/%v, want true/nil", changed, err)
	}
	changed, err = markInstanceSuspended(ctx, client, instance)
	if err != nil || changed {
		t.Fatalf("second markInstanceSuspended() = %v/%v, want false/nil", changed, err)
	}
	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(instanceGVK)
	if err := client.Get(ctx, ctrlclient.ObjectKey{Name: "sandbox", Namespace: "tenant"}, got); err != nil {
		t.Fatalf("get suspended Instance: %v", err)
	}
	suspended, found, err := unstructured.NestedBool(got.Object, "spec", "lifecycle", "suspended")
	if err != nil || !found || !suspended {
		t.Fatalf("suspended field = %v/%v/%v, want true", suspended, found, err)
	}
}

func runtimeSandboxObject(name, uid, templateName, mode string) *unstructured.Unstructured {
	// Instance is cluster-scoped; the tenant logical cluster is carried by the
	// client rather than metadata.namespace.
	object := runtimeTestObject(instanceGVK.GroupVersion().String(), instanceGVK.Kind, name, "")
	object.SetUID(types.UID(uid))
	object.Object["spec"] = map[string]any{
		"template": templateName,
		"values": map[string]any{
			"name":                       "sandbox-pod",
			infrav1alpha1.FarosModeField: mode,
		},
	}
	object.Object["status"] = map[string]any{
		"phase":            "Ready",
		"runtimeNamespace": "runtime",
		"components": map[string]any{
			"workspace": map[string]any{
				"controlServiceRef": map[string]any{"name": "sandbox-control", "namespace": "runtime"},
			},
		},
		"controlSecretRef": map[string]any{"name": "sandbox-control-token", "namespace": "runtime"},
	}
	return object
}

func runtimeTestObject(apiVersion, kind, name, namespace string) *unstructured.Unstructured {
	object := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
		},
	}}
	object.SetGroupVersionKind(schema.FromAPIVersionAndKind(apiVersion, kind))
	return object
}
