// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package commitbundle

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"
)

// NewCapabilityHandler returns the narrow internal endpoint used by
// Deployments to consume a path-scoped RepositoryCheckout bundle. The bearer
// capability is bound to the exact scope, name, and digest supplied in the
// headers. A successful transfer removes the bundle.
func NewCapabilityHandler(store Store, signer *CapabilitySigner) http.Handler {
	var mu sync.Mutex
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get(CapabilityTokenHeader), "Bearer "))
		scope := strings.TrimSpace(r.Header.Get(CapabilityScopeHeader))
		name := strings.TrimSpace(r.Header.Get(CapabilityNameHeader))
		digest := strings.TrimSpace(r.Header.Get(CapabilityDigestHeader))
		if token == "" || scope == "" || name == "" || digest == "" || signer.Validate(token, scope, name, digest, time.Now()) != nil {
			http.Error(w, "invalid or expired bundle capability", http.StatusUnauthorized)
			return
		}

		// Serialize the read/delete pair so one process cannot successfully
		// redeem the same capability twice concurrently.
		mu.Lock()
		defer mu.Unlock()
		bundle, err := store.Get(r.Context(), scope, name, digest)
		if err != nil {
			if IsNotFound(err) {
				http.Error(w, "bundle is unavailable", http.StatusGone)
				return
			}
			http.Error(w, "bundle read failed", http.StatusInternalServerError)
			return
		}
		var payload bytes.Buffer
		if err := json.NewEncoder(&payload).Encode(bundle); err != nil {
			http.Error(w, "bundle encoding failed", http.StatusInternalServerError)
			return
		}
		if err := store.Delete(r.Context(), scope, name, digest); err != nil && !errors.Is(err, errBundleNotFound) {
			http.Error(w, "bundle consumption failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload.Bytes())
	})
}
