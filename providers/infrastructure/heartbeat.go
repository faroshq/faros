// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

const (
	heartbeatVersion  = "0.1.0" // align with manifest.yaml spec.version
	heartbeatInterval = 30 * time.Second
)

// runHeartbeat POSTs to /api/providers/{name}/heartbeat every 30s once the
// provider's readiness gate passes. Skips silently when FAROS_HUB_URL is empty
// so local invocations don't need a hub. Mirrors providers/quickstart/main.go
// runHeartbeat — keep the two implementations aligned until the heartbeat
// loop moves into a shared provider SDK.
//
// Env:
//
//	FAROS_HUB_URL        - hub base URL (https://localhost:9443 in dev)
//	FAROS_HUB_TOKEN      - bearer token for the heartbeat request
//	FAROS_PROVIDER_NAME  - this provider's CatalogEntry name (default: infrastructure)
//	FAROS_HUB_INSECURE   - "true" → skip TLS verification (dev with self-signed certs)
func runHeartbeat(ctx context.Context, readinessChecks ...func() error) {
	hub := os.Getenv("FAROS_HUB_URL")
	token := os.Getenv("FAROS_HUB_TOKEN")
	name := os.Getenv("FAROS_PROVIDER_NAME")
	if name == "" {
		name = "infrastructure"
	}
	if hub == "" {
		log.Printf("heartbeat disabled (set FAROS_HUB_URL to enable)")
		return
	}
	url := hub + "/api/providers/" + name + "/heartbeat"
	body, _ := json.Marshal(map[string]string{"version": heartbeatVersion, "status": "healthy"})

	client := &http.Client{Timeout: 5 * time.Second}
	if os.Getenv("FAROS_HUB_INSECURE") == "true" {
		client.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // dev-only; opt-in via FAROS_HUB_INSECURE
		}
	}

	send := func(sendCtx context.Context) error {
		req, err := http.NewRequestWithContext(sendCtx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("send: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			return fmt.Errorf("%s: %d %s", url, resp.StatusCode, resp.Status)
		}
		return nil
	}
	var readiness func() error
	if len(readinessChecks) > 0 {
		readiness = readinessChecks[0]
	}
	runHeartbeatLoop(ctx, readiness, heartbeatInterval, send)
}

// runHeartbeatLoop is the readiness-gated scheduling seam for heartbeat
// delivery. It is intentionally independent from environment lookup and HTTP
// construction so tests can control the interval and observe sends without a
// real hub. A heartbeat is never attempted while readiness reports an error,
// including after a previously-ready controller stops.
func runHeartbeatLoop(
	ctx context.Context,
	readiness func() error,
	interval time.Duration,
	send func(context.Context) error,
) {
	if readiness == nil {
		log.Printf("heartbeat disabled (readiness check unavailable)")
		return
	}
	if send == nil {
		log.Printf("heartbeat disabled (sender unavailable)")
		return
	}

	trySend := func() {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if err := readiness(); err != nil {
			log.Printf("heartbeat gated: %v", err)
			return
		}
		if err := send(ctx); err != nil {
			log.Printf("heartbeat: %v", err)
		}
	}
	trySend()
	if interval <= 0 {
		return
	}

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			trySend()
		}
	}
}
