// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package skills

import (
	"context"
	"strings"
	"testing"
)

func TestUniversalWebPreviewBundledSkillGuidesHealthPathMismatch(t *testing.T) {
	snapshot, err := LoadBuiltinSnapshot(context.Background(), DefaultLimits())
	if err != nil {
		t.Fatalf("load builtin skills: %v", err)
	}

	entry, err := snapshot.Get("system:universal-web-preview")
	if err != nil {
		t.Fatalf("get universal web preview skill: %v", err)
	}
	if entry.Scope != ScopeSystem || !entry.Enabled || entry.Editable {
		t.Fatalf("unexpected bundled skill policy: %#v", entry)
	}
	if !strings.Contains(entry.Description, "template-less App Studio project") ||
		!strings.Contains(entry.Description, "preview is not becoming ready") {
		t.Fatalf("description does not support situational discovery: %q", entry.Description)
	}

	content := strings.Join(strings.Fields(entry.Content), " ")
	for _, guidance := range []string{
		"Use a dedicated path such as `/healthz` only when the application actually implements it.",
		"An accepted route can coexist with a failing health check.",
		"Health returns `404`: the configured health path is not implemented",
	} {
		if !strings.Contains(content, guidance) {
			t.Errorf("skill is missing preview diagnosis guidance %q", guidance)
		}
	}
}
