//go:build e2e

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

package kro

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const (
	infrakubeFinalizer = "finalizer.infrakube.galleybytes.com"
	infrakubeE2EWait   = 10 * time.Minute
)

var (
	terraformGVR = schema.GroupVersionResource{
		Group:    "infrakube.galleybytes.com",
		Version:  "v1",
		Resource: "terraforms",
	}
	secretGVR = schema.GroupVersionResource{
		Group:    "",
		Version:  "v1",
		Resource: "secrets",
	}
	leaseGVR = schema.GroupVersionResource{
		Group:    "coordination.k8s.io",
		Version:  "v1",
		Resource: "leases",
	}
)

// TestE2EInfrakubeTerraformStateLifecycle isolates the state question from
// cloud credentials and provider APIs. It proves that the Kubernetes backend
// persists state in the KRO-selected runtime namespace, updates that state
// during destroy, and leaves the backend artifacts behind after successful
// Infrakube finalizer cleanup. The test then removes only its exact Secret and
// Lease so repeated assessments do not leak state into the shared test cluster.
func TestE2EInfrakubeTerraformStateLifecycle(t *testing.T) {
	if os.Getenv("FAROS_E2E_INFRAKUBE") != "1" {
		t.Skip("run through make e2e-infrastructure-terraform-state")
	}

	dyn, _ := e2eClients(t)
	raw, err := os.ReadFile(filepath.Join("..", "..", "install", "templates", "terraform-stack.yaml"))
	if err != nil {
		t.Fatalf("read terraform-stack seed template: %v", err)
	}
	tmpl := decodeTemplate(t, raw)
	rgd, err := buildRGD(tmpl, testTokens())
	if err != nil {
		t.Fatalf("build terraform-stack RGD: %v", err)
	}
	applyRGD(t, dyn, rgd)
	t.Cleanup(func() {
		_ = dyn.Resource(rgdGVR).Delete(context.Background(), rgd.GetName(), metav1.DeleteOptions{})
	})
	if status, msg := waitGraphAccepted(t, dyn, rgd.GetName()); status != "True" {
		t.Fatalf("kro rejected terraform-stack RGD: GraphAccepted=%s: %s", status, msg)
	}

	instGVR := schema.GroupVersionResource{
		Group:    tmpl.Spec.InstanceCRD.Group,
		Version:  tmpl.Spec.InstanceCRD.Version,
		Resource: tmpl.Spec.InstanceCRD.Resource,
	}
	inst := e2eInstance(t, tmpl, fmt.Sprintf("%016x", time.Now().UnixNano()))
	createInstance(t, dyn, instGVR, inst)

	stackName, _, _ := unstructured.NestedString(inst.Object, "spec", "name")
	terraformName := stackName + "-terraform"
	stateSecretName := "tfstate-default-" + stackName + "-faros"
	stateLockName := "lock-" + stateSecretName
	terraformNamespace := ""
	cleanupComplete := false
	t.Cleanup(func() {
		if cleanupComplete {
			return
		}
		_ = dyn.Resource(instGVR).Delete(context.Background(), inst.GetName(), metav1.DeleteOptions{})
		if terraformNamespace == "" {
			terraformNamespace = findTerraformNamespace(dyn, terraformName)
		}
		if terraformNamespace == "" || !waitForTerraformCleanup(dyn, terraformNamespace, terraformName) {
			t.Logf("retaining Terraform recovery artifacts because child cleanup was not verified: namespace=%q secret=%q lease=%q", terraformNamespace, stateSecretName, stateLockName)
			return
		}
		deleteStateArtifact(t, dyn, secretGVR, terraformNamespace, stateSecretName)
		deleteStateArtifact(t, dyn, leaseGVR, terraformNamespace, stateLockName)
	})

	tf := waitForTerraformCompleted(t, dyn, terraformName)
	terraformNamespace = tf.GetNamespace()
	if terraformNamespace == "" {
		t.Fatal("materialized Terraform child has no runtime namespace")
	}
	if !slices.Contains(tf.GetFinalizers(), infrakubeFinalizer) {
		t.Fatalf("Terraform child finalizers = %v, want %q before deletion", tf.GetFinalizers(), infrakubeFinalizer)
	}
	backend, _, _ := unstructured.NestedString(tf.Object, "spec", "backend")
	if !strings.Contains(backend, `namespace = "`+terraformNamespace+`"`) {
		t.Fatalf("Terraform backend did not resolve runtime namespace %q: %q", terraformNamespace, backend)
	}

	beforeSecret := getStateArtifact(t, dyn, secretGVR, terraformNamespace, stateSecretName)
	beforeSerial, beforeResources := terraformStateSummary(t, beforeSecret)
	if beforeResources == 0 {
		t.Fatal("Terraform state has no resources after apply")
	}
	assertStateLock(t, dyn, terraformNamespace, stateLockName, stackName+"-faros")
	waitForTerraformStackStatus(t, dyn, instGVR, inst.GetName(), terraformNamespace)

	if err := dyn.Resource(instGVR).Delete(context.Background(), inst.GetName(), metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete TerraformStack instance: %v", err)
	}
	waitForTerraformDeleted(t, dyn, terraformNamespace, terraformName)
	waitForNotFound(t, dyn, instGVR, "", inst.GetName(), "KRO TerraformStack instance cleanup")

	afterSecret := getStateArtifact(t, dyn, secretGVR, terraformNamespace, stateSecretName)
	afterSerial, afterResources := terraformStateSummary(t, afterSecret)
	if afterResources != 0 {
		t.Fatalf("Terraform state still contains %d resources after destroy", afterResources)
	}
	if afterSerial <= beforeSerial {
		t.Fatalf("Terraform state serial did not advance during destroy: before=%d after=%d", beforeSerial, afterSerial)
	}
	if afterSecret.GetResourceVersion() == beforeSecret.GetResourceVersion() {
		t.Fatalf("Terraform state Secret resourceVersion did not change during destroy: %s", afterSecret.GetResourceVersion())
	}
	assertStateLock(t, dyn, terraformNamespace, stateLockName, stackName+"-faros")
	t.Logf("state assessment: Secret %s/%s and Lease %s persisted after destroy; state serial advanced %d -> %d and resources reached zero", terraformNamespace, stateSecretName, stateLockName, beforeSerial, afterSerial)

	deleteStateArtifact(t, dyn, secretGVR, terraformNamespace, stateSecretName)
	deleteStateArtifact(t, dyn, leaseGVR, terraformNamespace, stateLockName)
	cleanupComplete = true
}

func waitForTerraformCompleted(t *testing.T, dyn dynamic.Interface, name string) *unstructured.Unstructured {
	t.Helper()
	deadline := time.Now().Add(infrakubeE2EWait)
	var lastPhase string
	for time.Now().Before(deadline) {
		list, err := dyn.Resource(terraformGVR).List(context.Background(), metav1.ListOptions{})
		if err == nil {
			for i := range list.Items {
				item := &list.Items[i]
				if item.GetName() != name {
					continue
				}
				lastPhase, _, _ = unstructured.NestedString(item.Object, "status", "phase")
				if lastPhase == "completed" {
					return item.DeepCopy()
				}
			}
		}
		time.Sleep(e2ePollEvery)
	}
	t.Fatalf("Terraform child %q did not complete within %s (last phase %q)", name, infrakubeE2EWait, lastPhase)
	return nil
}

func waitForTerraformStackStatus(t *testing.T, dyn dynamic.Interface, gvr schema.GroupVersionResource, name, wantNamespace string) {
	t.Helper()
	deadline := time.Now().Add(infrakubeE2EWait)
	var last *unstructured.Unstructured
	for time.Now().Before(deadline) {
		obj, err := dyn.Resource(gvr).Get(context.Background(), name, metav1.GetOptions{})
		if err == nil {
			last = obj
			phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
			message, _, _ := unstructured.NestedString(obj.Object, "status", "message")
			resourceID, _, _ := unstructured.NestedString(obj.Object, "status", "resourceID")
			runtimeNamespace, _, _ := unstructured.NestedString(obj.Object, "status", "runtimeNamespace")
			if phase == "completed" && message == "hello from Faros" && resourceID != "" && runtimeNamespace == wantNamespace {
				if _, found, _ := unstructured.NestedFieldNoCopy(obj.Object, "status", "internal_marker"); found {
					t.Fatalf("TerraformStack status unexpectedly exposes internal_marker: %v", obj.Object["status"])
				}
				return
			}
		}
		time.Sleep(e2ePollEvery)
	}
	t.Fatalf("TerraformStack %q never projected completed status in namespace %q; last object: %v", name, wantNamespace, last)
}

func waitForTerraformDeleted(t *testing.T, dyn dynamic.Interface, namespace, name string) {
	t.Helper()
	deadline := time.Now().Add(infrakubeE2EWait)
	deletePhases := map[string]bool{
		"initializing-delete": true,
		"deleting":            true,
		"deleted":             true,
	}
	var lastPhase string
	var sawDeletePhase bool
	for time.Now().Before(deadline) {
		obj, err := dyn.Resource(terraformGVR).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			if !sawDeletePhase {
				t.Fatalf("Terraform child %s/%s disappeared without an observed delete phase (last phase %q)", namespace, name, lastPhase)
			}
			return
		}
		if err == nil {
			lastPhase, _, _ = unstructured.NestedString(obj.Object, "status", "phase")
			sawDeletePhase = sawDeletePhase || deletePhases[lastPhase]
		}
		time.Sleep(e2ePollEvery)
	}
	t.Fatalf("Infrakube finalizer cleanup did not finish within %s (last phase %q, observed delete phase=%t)", infrakubeE2EWait, lastPhase, sawDeletePhase)
}

func waitForNotFound(t *testing.T, dyn dynamic.Interface, gvr schema.GroupVersionResource, namespace, name, description string) {
	t.Helper()
	deadline := time.Now().Add(infrakubeE2EWait)
	for time.Now().Before(deadline) {
		_, err := dyn.Resource(gvr).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return
		}
		time.Sleep(e2ePollEvery)
	}
	t.Fatalf("%s did not finish within %s", description, infrakubeE2EWait)
}

func getStateArtifact(t *testing.T, dyn dynamic.Interface, gvr schema.GroupVersionResource, namespace, name string) *unstructured.Unstructured {
	t.Helper()
	obj, err := dyn.Resource(gvr).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get state artifact %s/%s: %v", namespace, name, err)
	}
	return obj
}

func assertStateLock(t *testing.T, dyn dynamic.Interface, namespace, name, suffix string) {
	t.Helper()
	lease := getStateArtifact(t, dyn, leaseGVR, namespace, name)
	labels := lease.GetLabels()
	if labels["app.kubernetes.io/managed-by"] != "terraform" ||
		labels["tfstate"] != "true" ||
		labels["tfstateSecretSuffix"] != suffix ||
		labels["tfstateWorkspace"] != "default" {
		t.Fatalf("Terraform state Lease %s/%s has unexpected labels: %v", namespace, name, labels)
	}
}

func terraformStateSummary(t *testing.T, secret *unstructured.Unstructured) (int64, int) {
	t.Helper()
	encoded, found, err := unstructured.NestedString(secret.Object, "data", "tfstate")
	if err != nil || !found || encoded == "" {
		t.Fatalf("Terraform state Secret %s/%s has no data.tfstate: found=%t err=%v", secret.GetNamespace(), secret.GetName(), found, err)
	}
	compressed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode Terraform state from Secret %s/%s: %v", secret.GetNamespace(), secret.GetName(), err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("open Terraform state gzip from Secret %s/%s: %v", secret.GetNamespace(), secret.GetName(), err)
	}
	stateJSON, err := io.ReadAll(reader)
	if closeErr := reader.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatalf("read Terraform state from Secret %s/%s: %v", secret.GetNamespace(), secret.GetName(), err)
	}
	var state struct {
		Serial    int64             `json:"serial"`
		Resources []json.RawMessage `json:"resources"`
	}
	if err := json.Unmarshal(stateJSON, &state); err != nil {
		t.Fatalf("decode Terraform state JSON from Secret %s/%s: %v", secret.GetNamespace(), secret.GetName(), err)
	}
	return state.Serial, len(state.Resources)
}

func findTerraformNamespace(dyn dynamic.Interface, name string) string {
	list, err := dyn.Resource(terraformGVR).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return ""
	}
	for i := range list.Items {
		if list.Items[i].GetName() == name {
			return list.Items[i].GetNamespace()
		}
	}
	return ""
}

func waitForTerraformCleanup(dyn dynamic.Interface, namespace, name string) bool {
	deadline := time.Now().Add(infrakubeE2EWait)
	for time.Now().Before(deadline) {
		_, err := dyn.Resource(terraformGVR).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true
		}
		time.Sleep(e2ePollEvery)
	}
	return false
}

func deleteStateArtifact(t *testing.T, dyn dynamic.Interface, gvr schema.GroupVersionResource, namespace, name string) {
	t.Helper()
	err := dyn.Resource(gvr).Namespace(namespace).Delete(context.Background(), name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		t.Fatalf("delete state artifact %s/%s: %v", namespace, name, err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		_, err := dyn.Resource(gvr).Namespace(namespace).Get(context.Background(), name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return
		}
		time.Sleep(e2ePollEvery)
	}
	t.Fatalf("state artifact %s/%s was not deleted", namespace, name)
}
