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

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestChartRunSandboxBinaryPolicy(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is not installed")
	}
	render := func(t *testing.T, values ...string) (string, error) {
		t.Helper()
		args := []string{"template", "app-studio", "deploy/chart"}
		for _, value := range values {
			args = append(args, "--set", value)
		}
		output, err := exec.Command(helm, args...).CombinedOutput()
		return string(output), err
	}

	defaultChart, err := render(t)
	if err != nil {
		t.Fatalf("render default chart: %v\n%s", err, defaultChart)
	}
	if !strings.Contains(defaultChart, "name: APP_STUDIO_RUN_SANDBOX_MODE\n              value: \"off\"") ||
		strings.Contains(defaultChart, "name: APP_STUDIO_RUN_SANDBOX\n") ||
		strings.Contains(defaultChart, "name: APP_STUDIO_DEVELOPMENT_MODE\n") {
		t.Fatal("default chart must emit only the binary mode=off policy")
	}
	selfHostRecipe := "- name: assistant.runSandbox.mode\n            value: \"on\""
	if !strings.Contains(defaultChart, selfHostRecipe) {
		t.Fatal("rendered CatalogEntry self-host recipe must select mode=on")
	}
	manifest, err := os.ReadFile("manifest.yaml")
	if err != nil {
		t.Fatalf("read manifest.yaml: %v", err)
	}
	if !strings.Contains(string(manifest), "- name: assistant.runSandbox.mode\n        value: \"on\"") {
		t.Fatal("manifest CatalogEntry self-host recipe must select mode=on")
	}

	onChart, err := render(t, "assistant.runSandbox.mode=on")
	if err != nil {
		t.Fatalf("render on chart: %v\n%s", err, onChart)
	}
	if !strings.Contains(onChart, "name: APP_STUDIO_RUN_SANDBOX_MODE\n              value: \"on\"") {
		t.Fatal("mode=on was not rendered into the provider Deployment")
	}

	for _, invalid := range []string{"byo-only", "force", "sometimes"} {
		output, err := render(t, "assistant.runSandbox.mode="+invalid)
		if err == nil || !strings.Contains(output, "assistant.runSandbox.mode must be off or on") {
			t.Fatalf("mode %q render error = %v\n%s", invalid, err, output)
		}
	}

	output, err := render(t, "assistant.runSandbox.mode=on", "replicaCount=2", "workspace.emptyDir=true")
	if err == nil || !strings.Contains(output, "assistant.runSandbox.mode=on requires replicaCount=1") {
		t.Fatalf("multi-replica on render error = %v\n%s", err, output)
	}
}
