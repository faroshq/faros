// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUniversalEnvironmentResolverChecksProjectRequirements(t *testing.T) {
	tests := []struct {
		name      string
		files     map[string]string
		wantError string
	}{
		{
			name: "compatible declarations",
			files: map[string]string{
				"go.mod":         "module example.test/app\n\ngo 1.22\n",
				"package.json":   `{"engines":{"node":">=20 <23"}}`,
				"pyproject.toml": "[project]\nrequires-python = \">=3.11,<3.13\"\n",
			},
		},
		{name: "newer Go", files: map[string]string{"go.mod": "module example.test/app\n\ngo 1.27\n"}, wantError: "requires Go 1.27"},
		{name: "newer Node", files: map[string]string{".nvmrc": "24\n"}, wantError: "requires Node 24"},
		{name: "newer Python", files: map[string]string{".python-version": "3.13\n"}, wantError: "requires Python 3.13"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			project := t.TempDir()
			for name, contents := range tc.files {
				if err := os.WriteFile(filepath.Join(project, name), []byte(contents), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			output, err := runUniversalResolver(t, project, "check", project)
			if tc.wantError == "" {
				if err != nil {
					t.Fatalf("faros-env check failed: %v\n%s", err, output)
				}
				if !strings.Contains(output, `"status": "ready"`) {
					t.Fatalf("faros-env output = %q, want ready evidence", output)
				}
				return
			}
			if err == nil || !strings.Contains(output, tc.wantError) {
				t.Fatalf("faros-env error = %v output=%q, want %q", err, output, tc.wantError)
			}
		})
	}
}

func TestUniversalEnvironmentExecFailsBeforeApplicationCommand(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module example.test/app\n\ngo 1.27\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(project, "application-started")
	output, err := runUniversalResolver(t, project, "exec", "--", "/bin/sh", "-c", "touch "+marker)
	if err == nil || !strings.Contains(output, "requires Go 1.27") {
		t.Fatalf("faros-env exec error = %v output=%q", err, output)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("application command ran despite incompatible environment: %v", statErr)
	}
}

func TestManagedServiceCommandUsesUniversalResolverWhenAvailable(t *testing.T) {
	binDir := t.TempDir()
	resolver := filepath.Join(binDir, "faros-env")
	if err := os.WriteFile(resolver, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	cmd := managedServiceCommand(context.Background(), []string{"node", "server.js"})
	if cmd.Path != resolver {
		t.Fatalf("managed service executable = %q, want %q", cmd.Path, resolver)
	}
	want := []string{resolver, "exec", "--", "node", "server.js"}
	if strings.Join(cmd.Args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("managed service argv = %q, want %q", cmd.Args, want)
	}
}

func runUniversalResolver(t *testing.T, directory string, args ...string) (string, error) {
	t.Helper()
	manifestPath := filepath.Join(t.TempDir(), "image-manifest.json")
	manifest := map[string]any{
		"schemaVersion": 1,
		"image":         "faros-universal-dev-test",
		"runtimes": map[string]any{
			"go":     map[string]string{"version": "1.26.3", "executable": "/usr/local/go/bin/go"},
			"node":   map[string]string{"version": "22.0.0", "executable": "/usr/local/bin/node"},
			"python": map[string]string{"version": "3.12.0", "executable": "/usr/local/bin/python3"},
		},
		"commands": []string{},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join("universal", "faros-env"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", append([]string{script}, args...)...)
	cmd.Dir = directory
	cmd.Env = append(os.Environ(), "FAROS_ENV_MANIFEST="+manifestPath)
	output, runErr := cmd.CombinedOutput()
	return string(output), runErr
}
