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
