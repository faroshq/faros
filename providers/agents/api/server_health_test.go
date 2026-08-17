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

package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealthAndReadinessAreIndependent(t *testing.T) {
	server := &Server{started: time.Now().UTC()}

	live := httptest.NewRecorder()
	server.healthz(live, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if live.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want %d", live.Code, http.StatusOK)
	}

	notReady := httptest.NewRecorder()
	server.readyz(notReady, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if notReady.Code != http.StatusServiceUnavailable {
		t.Fatalf("readyz before discovery = %d, want %d", notReady.Code, http.StatusServiceUnavailable)
	}

	server.bg = &background{initialized: true}
	ready := httptest.NewRecorder()
	server.readyz(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusOK {
		t.Fatalf("readyz after discovery = %d, want %d", ready.Code, http.StatusOK)
	}
}
