// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package hub

import "testing"

func TestControllerOptionsForRetryableLeaderTermAllowsStableNames(t *testing.T) {
	options := controllerOptionsForRetryableLeaderTerm()
	if options.SkipNameValidation == nil || !*options.SkipNameValidation {
		t.Fatal("leader-term managers must allow stable controller names across process-lifetime rebuilds")
	}
}
