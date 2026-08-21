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
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

const (
	runSandboxTemplateName   = "universal-coding-sandbox"
	runSandboxDevComponent   = "faros-dev"
	runSandboxManagedBy      = "faros-infrastructure"
	runSandboxJobNameLabel   = "job-name"
	runSandboxNameLabel      = "app.kubernetes.io/name"
	runSandboxComponentLabel = "app.kubernetes.io/component"
	runSandboxManagedByLabel = "app.kubernetes.io/managed-by"
)

var podsGVR = schema.GroupVersionResource{Version: "v1", Resource: "pods"}

// cleanupRunSandboxTokenPods removes only the short-lived control-token Job's
// pods for the platform-owned run-sandbox template. Jobs normally own their
// pods, but succeeded token Jobs have been observed leaving pods without an
// ownerReference after the runtime CR is deleted. The exact job-name plus
// template/component/manager labels keep this cleanup from touching any
// ordinary application or non-run template pod.
//
// The boolean is false when matching pods were found and delete requests were
// issued. Callers should requeue and call again before removing their finalizer
// so the finalizer does not claim the runtime namespace is clean too early.
func cleanupRunSandboxTokenPods(ctx context.Context, runtime dynamic.Interface, templateName, namespace, instanceName string) (bool, error) {
	if templateName != runSandboxTemplateName {
		return true, nil
	}
	if runtime == nil {
		return false, fmt.Errorf("runtime client is unavailable")
	}
	if namespace == "" || instanceName == "" {
		return false, fmt.Errorf("run-sandbox pod cleanup requires namespace and instance name")
	}
	selector := labels.Set{
		runSandboxNameLabel:      runSandboxTemplateName,
		runSandboxComponentLabel: runSandboxDevComponent,
		runSandboxManagedByLabel: runSandboxManagedBy,
		runSandboxJobNameLabel:   instanceName + "-dev-token",
	}.AsSelector().String()
	pods, err := runtime.Resource(podsGVR).Namespace(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return false, fmt.Errorf("list run-sandbox token pods in %s: %w", namespace, err)
	}
	if len(pods.Items) == 0 {
		return true, nil
	}
	for i := range pods.Items {
		name := pods.Items[i].GetName()
		if name == "" {
			continue
		}
		if err := runtime.Resource(podsGVR).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return false, fmt.Errorf("delete run-sandbox token pod %s/%s: %w", namespace, name, err)
		}
	}
	return false, nil
}

func runSandboxInstanceTemplateName(inst *unstructured.Unstructured) string {
	if inst == nil {
		return ""
	}
	if name, _, _ := unstructured.NestedString(inst.Object, "spec", "template"); name != "" {
		return name
	}
	name, _, _ := unstructured.NestedString(inst.Object, "status", "template")
	return name
}
