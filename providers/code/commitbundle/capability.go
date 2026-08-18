// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package commitbundle

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	CapabilityPath         = "/internal/bundles"
	CapabilityTokenHeader  = "Authorization"
	CapabilityScopeHeader  = "X-Faros-Bundle-Scope"
	CapabilityNameHeader   = "X-Faros-Bundle-Name"
	CapabilityDigestHeader = "X-Faros-Bundle-Digest"
	// EnvCapabilitySigningKey configures the HMAC key used by all Code
	// replicas. Leave it unset for the single-replica development fallback.
	EnvCapabilitySigningKey = "CODE_COMMIT_BUNDLE_SIGNING_KEY"
	capabilityTTL           = 2 * time.Minute
	minCapabilityKeyBytes   = 32
)

var (
	errCapabilityMalformed = errors.New("invalid bundle capability")
	errCapabilityExpired   = errors.New("bundle capability expired")
)

type capabilityClaims struct {
	Scope  string `json:"scope"`
	Name   string `json:"name"`
	Digest string `json:"digest"`
	Expiry int64  `json:"exp"`
}

// CapabilitySigner issues short-lived, opaque one-time bundle capabilities.
// Deployments may arrive at any Code replica, so production replicas must be
// configured with the same key through EnvCapabilitySigningKey. When that
// variable is absent, the process-random fallback remains useful for a
// single-replica local process.
type CapabilitySigner struct {
	key []byte
}

// NewCapabilitySigner creates a signer with a process-random HMAC key.
func NewCapabilitySigner() (*CapabilitySigner, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate bundle capability key: %w", err)
	}
	return newCapabilitySigner(key)
}

// NewCapabilitySignerWithKey creates a signer from a shared HMAC key. The key
// is copied so callers may safely reuse or clear their input buffer.
func NewCapabilitySignerWithKey(key []byte) (*CapabilitySigner, error) {
	return newCapabilitySigner(key)
}

// NewCapabilitySignerFromEnv creates a signer from the configured shared key,
// falling back to NewCapabilitySigner when the variable is unset or blank.
// Kubernetes Secret values are exposed as ordinary environment strings; the
// value is treated as opaque rather than being base64-decoded a second time.
func NewCapabilitySignerFromEnv() (*CapabilitySigner, error) {
	if key := strings.TrimSpace(os.Getenv(EnvCapabilitySigningKey)); key != "" {
		return NewCapabilitySignerWithKey([]byte(key))
	}
	return NewCapabilitySigner()
}

func newCapabilitySigner(key []byte) (*CapabilitySigner, error) {
	if len(key) < minCapabilityKeyBytes {
		return nil, fmt.Errorf("bundle capability key must be at least %d bytes", minCapabilityKeyBytes)
	}
	return &CapabilitySigner{key: append([]byte(nil), key...)}, nil
}

// Issue signs one scope/name/digest tuple. The returned expiry is persisted as
// metadata only so a consumer can know when to retry; the token remains opaque.
func (s *CapabilitySigner) Issue(scope, name, digest string, now time.Time) (string, time.Time, error) {
	if s == nil || len(s.key) == 0 {
		return "", time.Time{}, errors.New("bundle capability signer is unavailable")
	}
	claims := capabilityClaims{Scope: strings.TrimSpace(scope), Name: strings.TrimSpace(name), Digest: strings.TrimSpace(digest), Expiry: now.Add(capabilityTTL).Unix()}
	if claims.Scope == "" || claims.Name == "" || claims.Digest == "" {
		return "", time.Time{}, errors.New("bundle capability coordinates are required")
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("marshal bundle capability: %w", err)
	}
	signature := s.mac(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature), time.Unix(claims.Expiry, 0).UTC(), nil
}

// Validate verifies token authenticity, expiry, and exact request binding.
func (s *CapabilitySigner) Validate(token, scope, name, digest string, now time.Time) error {
	if s == nil || len(s.key) == 0 {
		return errCapabilityMalformed
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return errCapabilityMalformed
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return errCapabilityMalformed
	}
	provided, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(provided, s.mac(payload)) {
		return errCapabilityMalformed
	}
	var claims capabilityClaims
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Scope == "" || claims.Name == "" || claims.Digest == "" {
		return errCapabilityMalformed
	}
	if now.Unix() >= claims.Expiry {
		return errCapabilityExpired
	}
	if claims.Scope != strings.TrimSpace(scope) || claims.Name != strings.TrimSpace(name) || claims.Digest != strings.TrimSpace(digest) {
		return errCapabilityMalformed
	}
	return nil
}

func (s *CapabilitySigner) mac(payload []byte) []byte {
	h := hmac.New(sha256.New, s.key)
	_, _ = h.Write(payload)
	return h.Sum(nil)
}
