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

package application

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/faroshq/provider-infrastructure/kro"
)

const (
	testTenant    = "tenantcluster1"
	testNamespace = "default"
)

// tenantSecret is the Secret an agents Connection leaves in the tenant
// workspace — the source of truth this bridge copies from.
func tenantSecret(name, token string) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Data:       map[string][]byte{"token": []byte(token)},
	}
}

func instanceWithTokenRef(name, ref string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "infrastructure.kedge.faros.sh/v1alpha1",
		"kind":       "SearXNG",
		"metadata":   map[string]any{"name": name, "namespace": testNamespace},
		"spec":       map[string]any{},
	}}
	if ref != "" {
		_ = unstructured.SetNestedField(u.Object, ref, "spec", tokenSpecField)
	}
	return u
}

// newBridgeController wires a Controller over fake tenant + runtime clients.
func newBridgeController(t *testing.T, tenantObjs ...client.Object) (*Controller, *dynamicfake.FakeDynamicClient) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	tenantClient := ctrlfake.NewClientBuilder().WithScheme(scheme).WithObjects(tenantObjs...).Build()
	runtimeDyn := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	c := &Controller{cfg: Config{Runtime: runtimeDyn, CredentialsNamespace: "default"}}
	t.Cleanup(func() { _ = tenantClient })
	return c, runtimeDyn
}

func readBridged(t *testing.T, dyn *dynamicfake.FakeDynamicClient, ns, name string) *unstructured.Unstructured {
	t.Helper()
	got, err := dyn.Resource(secretGVR).Namespace(ns).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("bridged secret %s/%s: %v", ns, name, err)
	}
	return got
}

func TestBridgeAccessToken(t *testing.T) {
	ctx := context.Background()
	ns := kro.RuntimeNamespace(testTenant, testNamespace)

	t.Run("copies the tenant token into the runtime namespace", func(t *testing.T) {
		scheme := runtime.NewScheme()
		if err := corev1.AddToScheme(scheme); err != nil {
			t.Fatal(err)
		}
		tenantClient := ctrlfake.NewClientBuilder().WithScheme(scheme).
			WithObjects(tenantSecret("kedge-agents-conn-search", "s3cret")).Build()
		c, dyn := newBridgeController(t)

		app := instanceWithTokenRef("search", "kedge-agents-conn-search")
		if err := c.bridgeAccessToken(ctx, tenantClient, testTenant, testNamespace, app); err != nil {
			t.Fatalf("bridge: %v", err)
		}

		got := readBridged(t, dyn, ns, bridgedTokenSecretName("search"))
		data, _, _ := unstructured.NestedStringMap(got.Object, "data")
		// The payload rides as base64 the whole way — the controller never
		// decodes the credential into memory.
		decoded, err := base64.StdEncoding.DecodeString(data["token"])
		if err != nil {
			t.Fatalf("bridged value is not base64: %v", err)
		}
		if string(decoded) != "s3cret" {
			t.Fatalf("token = %q, want the tenant's value", decoded)
		}
		if got.GetLabels()[kro.LabelManagedBy] != kro.ManagedByValue {
			t.Errorf("bridged Secret should be labelled managed-by so cleanup can find it: %v", got.GetLabels())
		}
	})

	t.Run("no ref is a no-op, not an error", func(t *testing.T) {
		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)
		tenantClient := ctrlfake.NewClientBuilder().WithScheme(scheme).Build()
		c, dyn := newBridgeController(t)

		if err := c.bridgeAccessToken(ctx, tenantClient, testTenant, testNamespace, instanceWithTokenRef("plain", "")); err != nil {
			t.Fatalf("an ungated template must reconcile cleanly: %v", err)
		}
		if _, err := dyn.Resource(secretGVR).Namespace(ns).Get(ctx, bridgedTokenSecretName("plain"), metav1.GetOptions{}); err == nil {
			t.Fatal("nothing should have been written")
		}
	})

	// Starting an ungated instance because a Secret name was mistyped is the
	// failure worth being loud about.
	t.Run("a named-but-missing Secret is a clear error", func(t *testing.T) {
		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)
		tenantClient := ctrlfake.NewClientBuilder().WithScheme(scheme).Build()
		c, _ := newBridgeController(t)

		err := c.bridgeAccessToken(ctx, tenantClient, testTenant, testNamespace, instanceWithTokenRef("search", "typo"))
		if err == nil {
			t.Fatal("want an error naming the missing Secret")
		}
		if !strings.Contains(err.Error(), "typo") || !strings.Contains(err.Error(), tokenSpecField) {
			t.Fatalf("error should name the Secret and the field that referenced it: %v", err)
		}
	})

	t.Run("a Secret without a token key is an error", func(t *testing.T) {
		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)
		wrong := &corev1.Secret{
			TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
			ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "default"},
			Data:       map[string][]byte{"password": []byte("x")},
		}
		tenantClient := ctrlfake.NewClientBuilder().WithScheme(scheme).WithObjects(wrong).Build()
		c, _ := newBridgeController(t)

		err := c.bridgeAccessToken(ctx, tenantClient, testTenant, testNamespace, instanceWithTokenRef("search", "other"))
		if err == nil || !strings.Contains(err.Error(), "token") {
			t.Fatalf("want an error about the missing key, got %v", err)
		}
	})

	// Rotating the credential in the tenant workspace must replace the runtime
	// copy, or the gate would keep accepting the old token.
	t.Run("a rotated token overwrites the bridged copy", func(t *testing.T) {
		scheme := runtime.NewScheme()
		_ = corev1.AddToScheme(scheme)
		src := tenantSecret("kedge-agents-conn-search", "old")
		tenantClient := ctrlfake.NewClientBuilder().WithScheme(scheme).WithObjects(src).Build()
		c, dyn := newBridgeController(t)
		app := instanceWithTokenRef("search", "kedge-agents-conn-search")

		if err := c.bridgeAccessToken(ctx, tenantClient, testTenant, testNamespace, app); err != nil {
			t.Fatal(err)
		}
		src.Data["token"] = []byte("new")
		if err := tenantClient.Update(ctx, src); err != nil {
			t.Fatal(err)
		}
		if err := c.bridgeAccessToken(ctx, tenantClient, testTenant, testNamespace, app); err != nil {
			t.Fatalf("second reconcile: %v", err)
		}

		data, _, _ := unstructured.NestedStringMap(readBridged(t, dyn, ns, bridgedTokenSecretName("search")).Object, "data")
		decoded, _ := base64.StdEncoding.DecodeString(data["token"])
		if string(decoded) != "new" {
			t.Fatalf("token = %q, want the rotated value", decoded)
		}
	})
}

func TestCleanupAccessToken(t *testing.T) {
	ctx := context.Background()
	ns := kro.RuntimeNamespace(testTenant, testNamespace)
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	tenantClient := ctrlfake.NewClientBuilder().WithScheme(scheme).
		WithObjects(tenantSecret("kedge-agents-conn-search", "s3cret")).Build()
	c, dyn := newBridgeController(t)
	app := instanceWithTokenRef("search", "kedge-agents-conn-search")

	if err := c.bridgeAccessToken(ctx, tenantClient, testTenant, testNamespace, app); err != nil {
		t.Fatal(err)
	}
	if err := c.cleanupAccessToken(ctx, testTenant, testNamespace, "search"); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := dyn.Resource(secretGVR).Namespace(ns).Get(ctx, bridgedTokenSecretName("search"), metav1.GetOptions{}); err == nil {
		t.Fatal("bridged Secret should be gone")
	}

	// The bridged Secret lives outside the instance's cluster, so cleanup runs
	// on a namespace that may already have been reaped.
	if err := c.cleanupAccessToken(ctx, testTenant, testNamespace, "search"); err != nil {
		t.Fatalf("cleanup must be idempotent: %v", err)
	}
}
