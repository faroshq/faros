/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package syncedsecret

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"sort"

	secretsv1alpha1 "github.com/faroshq/provider-secrets/apis/v1alpha1"
	"github.com/faroshq/provider-secrets/backend"
)

// assemble builds the projected Secret's data from the spec: dataFrom paths
// first (verbatim, later entries winning on collisions), then explicit data
// mappings on top (so cherry-picked remappings always win). Each distinct
// remote path is fetched exactly once.
func assemble(ctx context.Context, b backend.StoreBackend, store *secretsv1alpha1.SecretStore, cred backend.Credential, spec *secretsv1alpha1.SyncedSecretSpec) (map[string][]byte, error) {
	fetched := map[string]backend.SecretValues{}
	fetch := func(path string) (backend.SecretValues, error) {
		if values, ok := fetched[path]; ok {
			return values, nil
		}
		values, _, err := b.Fetch(ctx, store, cred, path)
		if err != nil {
			return nil, fmt.Errorf("fetch %q: %w", path, err)
		}
		fetched[path] = values
		return values, nil
	}

	out := map[string][]byte{}
	for _, ref := range spec.DataFrom {
		values, err := fetch(ref.Path)
		if err != nil {
			return nil, err
		}
		maps.Copy(out, values)
	}
	for _, d := range spec.Data {
		values, err := fetch(d.RemoteRef.Path)
		if err != nil {
			return nil, err
		}
		property := d.RemoteRef.Property
		if property == "" {
			property = d.SecretKey
		}
		v, ok := values[property]
		if !ok {
			return nil, fmt.Errorf("path %q has no property %q", d.RemoteRef.Path, property)
		}
		out[d.SecretKey] = v
	}
	return out, nil
}

// HashData is the content hash surfaced as status.syncedVersion: sha256 over
// the sorted key/value pairs, so it changes exactly when the projected
// material changes. Exported for the portal-facing REST layer and tests.
func HashData(data map[string][]byte) string {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write(data[k])
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
