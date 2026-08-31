// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"errors"
	"strings"
	"testing"
)

// A provider that has not finished starting is not broken. Readiness gates
// traffic, so the interesting state is a probe that RAN and failed — not the
// absence of one.
func TestVWReadinessIsReadyBeforeTheFirstProbe(t *testing.T) {
	var r vwReadiness
	if err := r.Check(); err != nil {
		t.Errorf("unprobed provider reported unready: %v", err)
	}
}

func TestVWReadinessReportsAFailedProbe(t *testing.T) {
	var r vwReadiness
	r.set("https://127.0.0.1:6443/services/apiexport/abc/x", errors.New("no such host"))

	err := r.Check()
	if err == nil {
		t.Fatal("a failed probe reported ready; this is the silence the probe exists to break")
	}
	// The message has to carry the address and say what stops working —
	// the failure otherwise looks like an unrelated provider bug.
	for _, want := range []string{"127.0.0.1:6443", "no such host", "Instances will not reconcile", "virtualWorkspaceURL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("readiness message omits %q: %s", want, err)
		}
	}
}

// Recovery must clear it: a transient DNS or restart blip should not pin the
// provider unready until someone notices.
func TestVWReadinessClearsOnRecovery(t *testing.T) {
	var r vwReadiness
	r.set("https://x/y", errors.New("connection refused"))
	if r.Check() == nil {
		t.Fatal("setup: expected unready")
	}
	r.set("https://x/y", nil)
	if err := r.Check(); err != nil {
		t.Errorf("a recovered probe still reports unready: %v", err)
	}
}
