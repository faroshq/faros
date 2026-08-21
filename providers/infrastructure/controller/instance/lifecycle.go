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
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	infrav1alpha1 "github.com/faroshq/provider-infrastructure/apis/v1alpha1"
)

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
