// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package repositorysync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	capabilityPath         = "/internal/bundles"
	capabilityScopeHeader  = "X-Faros-Bundle-Scope"
	capabilityNameHeader   = "X-Faros-Bundle-Name"
	capabilityDigestHeader = "X-Faros-Bundle-Digest"
	maxBundleResponseBytes = 2 << 20
)

// HTTPBundleFetcher redeems the short-lived capability exposed on a Code
// RepositoryCheckout. It deliberately has no dependency on provider-code Go
// packages and never receives a Git-host credential.
type HTTPBundleFetcher struct {
	endpoint string
	client   *http.Client
}

func NewHTTPBundleFetcher(rawBaseURL string) (*HTTPBundleFetcher, error) {
	base, err := url.Parse(strings.TrimSpace(rawBaseURL))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" || base.User != nil {
		return nil, fmt.Errorf("DEPLOYMENTS_CODE_URL must be an http(s) provider URL")
	}
	base.RawQuery, base.Fragment = "", ""
	base.Path = strings.TrimRight(base.Path, "/") + capabilityPath
	return &HTTPBundleFetcher{
		endpoint: base.String(),
		client: &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

type transferredBundle struct {
	Name   string                  `json:"name"`
	Digest string                  `json:"digest"`
	Scope  string                  `json:"scope"`
	Files  []transferredBundleFile `json:"files"`
}

type transferredBundleFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Delete  bool   `json:"delete,omitempty"`
}

func (f *HTTPBundleFetcher) Fetch(ctx context.Context, scope string, checkout *unstructured.Unstructured) (CheckoutResult, error) {
	result := CheckoutResult{}
	result.Ref, _, _ = unstructured.NestedString(checkout.Object, "status", "ref")
	result.CommitSHA, _, _ = unstructured.NestedString(checkout.Object, "status", "commitSHA")
	result.Skipped, _, _ = unstructured.NestedStringSlice(checkout.Object, "status", "skipped")
	name, nameFound, _ := unstructured.NestedString(checkout.Object, "status", "bundleRef", "name")
	digest, digestFound, _ := unstructured.NestedString(checkout.Object, "status", "bundleRef", "digest")
	if !nameFound && !digestFound {
		return result, nil
	}
	token, tokenFound, _ := unstructured.NestedString(checkout.Object, "status", "access", "token")
	if !nameFound || !digestFound || !tokenFound || strings.TrimSpace(name) == "" || strings.TrimSpace(digest) == "" || strings.TrimSpace(token) == "" {
		return CheckoutResult{}, fmt.Errorf("Code RepositoryCheckout %q has incomplete bundle capability metadata", checkout.GetName())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.endpoint, nil)
	if err != nil {
		return CheckoutResult{}, fmt.Errorf("create Code bundle request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(capabilityScopeHeader, strings.TrimSpace(scope))
	req.Header.Set(capabilityNameHeader, name)
	req.Header.Set(capabilityDigestHeader, digest)
	resp, err := f.client.Do(req)
	if err != nil {
		return CheckoutResult{}, fmt.Errorf("fetch Code checkout bundle: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusGone {
			return CheckoutResult{}, fmt.Errorf("%w: HTTP %d", errCapabilityRejected, resp.StatusCode)
		}
		return CheckoutResult{}, fmt.Errorf("fetch Code checkout bundle: HTTP %d", resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, maxBundleResponseBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return CheckoutResult{}, fmt.Errorf("read Code checkout bundle: %w", err)
	}
	if len(data) > maxBundleResponseBytes {
		return CheckoutResult{}, fmt.Errorf("Code checkout bundle exceeds %d bytes", maxBundleResponseBytes)
	}
	var bundle transferredBundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		return CheckoutResult{}, fmt.Errorf("decode Code checkout bundle: %w", err)
	}
	if bundle.Scope != strings.TrimSpace(scope) || bundle.Name != name || bundle.Digest != digest {
		return CheckoutResult{}, fmt.Errorf("Code checkout bundle coordinates do not match the claimed RepositoryCheckout")
	}
	result.Files = make([]SourceFile, 0, len(bundle.Files))
	for _, file := range bundle.Files {
		result.Files = append(result.Files, SourceFile{Path: file.Path, Content: file.Content, Delete: file.Delete})
	}
	return result, nil
}
