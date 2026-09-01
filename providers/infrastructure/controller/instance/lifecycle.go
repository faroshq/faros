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
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1alpha1 "github.com/faroshq/provider-infrastructure/apis/v1alpha1"
)

// instanceSuspended reads the provider-owned lifecycle bit from the
// tenant-facing Instance. Missing means running, preserving the additive
// contract for Instances created before lifecycle suspension was introduced.
func instanceSuspended(inst *unstructured.Unstructured) bool {
	if inst == nil {
		return false
	}
	suspended, _, _ := unstructured.NestedBool(inst.Object, "spec", "lifecycle", "suspended")
	return suspended
}

// markInstanceSuspended persists a one-way suspension transition. The
// controller intentionally does not auto-resume after an idle/hard lifetime
// deadline: a user or App Studio must explicitly clear the lifecycle bit,
// which makes compute spend and resume behavior visible and auditable.
func markInstanceSuspended(ctx context.Context, tenantClient client.Client, inst *unstructured.Unstructured) (bool, error) {
	if instanceSuspended(inst) {
		return false, nil
	}
	if err := unstructured.SetNestedField(inst.Object, true, "spec", "lifecycle", "suspended"); err != nil {
		return false, err
	}
	if err := tenantClient.Update(ctx, inst); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// lifecycleDue is deliberately pure so short-lived test templates can prove
// expiry without sleeping or a controller-runtime harness. A missing activity
// marker is fail-closed to the Instance creation time.
func lifecycleDue(now time.Time, created metav1.Time, development *infrav1alpha1.TemplateDevelopment, runtimeObj *unstructured.Unstructured) (string, bool) {
	if development == nil || created.IsZero() {
		return "", false
	}
	if development.MaxLifetimeSeconds > 0 && !now.Before(created.Time.Add(time.Duration(development.MaxLifetimeSeconds)*time.Second)) {
		return "SandboxExpired", true
	}
	last := created.Time
	if runtimeObj != nil {
		if raw := runtimeObj.GetAnnotations()[infrav1alpha1.FarosLastActivityAnnotation]; raw != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil && parsed.After(last) && !parsed.After(now) {
				last = parsed
			}
		}
	}
	if development.IdleTimeoutSeconds > 0 && !now.Before(last.Add(time.Duration(development.IdleTimeoutSeconds)*time.Second)) {
		return "SandboxIdle", true
	}
	return "", false
}

func lifecycleRequeueAfter(now time.Time, created metav1.Time, development *infrav1alpha1.TemplateDevelopment, runtimeObj *unstructured.Unstructured, fallback time.Duration) time.Duration {
	if development == nil || created.IsZero() {
		return fallback
	}
	deadline := time.Time{}
	if development.MaxLifetimeSeconds > 0 {
		deadline = created.Time.Add(time.Duration(development.MaxLifetimeSeconds) * time.Second)
	}
	last := created.Time
	if runtimeObj != nil {
		if raw := runtimeObj.GetAnnotations()[infrav1alpha1.FarosLastActivityAnnotation]; raw != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil && parsed.After(last) && !parsed.After(now) {
				last = parsed
			}
		}
	}
	if development.IdleTimeoutSeconds > 0 {
		idle := last.Add(time.Duration(development.IdleTimeoutSeconds) * time.Second)
		if deadline.IsZero() || idle.Before(deadline) {
			deadline = idle
		}
	}
	if deadline.IsZero() || !deadline.After(now) {
		return time.Second
	}
	if wait := time.Until(deadline); wait > 0 && wait < fallback {
		return wait
	}
	return fallback
}

func runtimeReady(obj *unstructured.Unstructured) bool {
	if obj == nil {
		return false
	}
	status, _, _ := unstructured.NestedMap(obj.Object, "status")
	if phase, _ := status["phase"].(string); phase == "Ready" {
		return true
	}
	conditions, _ := status["conditions"].([]any)
	return conditionTrue(conditions, "Ready")
}

// runtimeReadyForNetwork is the stricter readiness predicate used before the
// controller publishes the runtime network phase to a tenant Instance. A
// status/condition from the previous runtime generation must not survive a
// setup -> runtime spec update and open the data plane while the new graph is
// still converging. KRO commonly puts observedGeneration on the Ready
// condition, while other runtimes expose it at status.observedGeneration; we
// accept either server-owned shape, but never accept a Ready signal without a
// matching generation.
func runtimeReadyForNetwork(obj *unstructured.Unstructured) bool {
	if obj == nil || obj.GetGeneration() <= 0 {
		return false
	}
	status, found, err := unstructured.NestedMap(obj.Object, "status")
	if err != nil || !found {
		return false
	}
	phase, _ := status["phase"].(string)
	phaseReady := phase == "Ready"
	conditions, _ := status["conditions"].([]any)
	readyConditionFound := false
	readyConditionCurrent := false
	for _, raw := range conditions {
		condition, ok := raw.(map[string]any)
		if !ok || condition["type"] != "Ready" {
			continue
		}
		readyConditionFound = true
		if condition["status"] != "True" {
			return false
		}
		if observed, ok := generationValue(condition["observedGeneration"]); ok && observed == obj.GetGeneration() {
			readyConditionCurrent = true
		}
	}
	if !phaseReady && !readyConditionFound {
		return false
	}

	if observed, ok := generationValue(status["observedGeneration"]); ok {
		return observed == obj.GetGeneration()
	}
	return readyConditionCurrent
}

func generationValue(value any) (int64, bool) {
	switch value := value.(type) {
	case int64:
		return value, true
	case int32:
		return int64(value), true
	case int:
		return int64(value), true
	case uint64:
		if value > uint64(^uint64(0)>>1) {
			return 0, false
		}
		return int64(value), true
	case uint32:
		return int64(value), true
	case uint:
		return int64(value), true
	case float64:
		if value != float64(int64(value)) {
			return 0, false
		}
		return int64(value), true
	default:
		return 0, false
	}
}
