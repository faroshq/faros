/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package github

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"time"

	gogithub "github.com/google/go-github/v66/github"

	"github.com/faroshq/provider-code/backend"
)

type listingLeaseKey struct{}
type listingLease struct {
	cache    *requestCache
	identity requestIdentity
	state    *requestState
}

// cachedPackageListing publishes only complete successful paginated results.
// The credential gate coalesces refreshes; no individual page is reused during
// a refresh. Serialized results share the request cache's bounds and decode
// into independent values so callers cannot mutate another caller's snapshot.
func cachedPackageListing[T any](ctx context.Context, b *Backend, client *gogithub.Client, cred backend.Credential, parts []string, fetch func(context.Context) ([]T, error)) ([]T, error) {
	cache := b.requestCache()
	identity := requestIdentity{credentialHash(cred.Token), client.BaseURL.Scheme + "://" + client.BaseURL.Host}
	state, err := cache.acquire(ctx, identity)
	if err != nil {
		return nil, err
	}
	defer cache.release(state)
	state.expire(cache.now())
	scope, _ := ctx.Value(cacheScopeKey{}).([32]byte)
	encodedKey, _ := json.Marshal(append([]string{client.BaseURL.String()}, parts...))
	key := sha256.Sum256(append(scope[:], encodedKey...))
	if cached, ok := state.responses[key]; ok {
		var result []T
		if err := json.Unmarshal(cached.body, &result); err != nil {
			return nil, err
		}
		return result, nil
	}
	// Retain ownership across all pages without recursively acquiring the gate.
	// Redirects to another host use that host's independent gate instead.
	ctx = context.WithValue(ctx, listingLeaseKey{}, &listingLease{cache, identity, state})
	ctx = context.WithValue(ctx, cacheTTLKey{}, time.Duration(0))
	result, err := fetch(ctx)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	state.store(key, cachedResponse{status: http.StatusOK, body: body, expires: cache.now().Add(packageCacheTTL)})
	return result, nil
}
