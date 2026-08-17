// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

func runHeartbeat(ctx context.Context, ready *atomic.Bool) {
	hub := strings.TrimRight(os.Getenv("FAROS_HUB_URL"), "/")
	if hub == "" {
		return
	}
	name := os.Getenv("FAROS_PROVIDER_NAME")
	if name == "" {
		name = "deployments"
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if os.Getenv("FAROS_HUB_INSECURE") == "true" {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	client := &http.Client{Timeout: 10 * time.Second, Transport: transport}
	runHeartbeatLoop(ctx, ready, client, hub, name, os.Getenv("FAROS_HUB_TOKEN"))
}

func runHeartbeatLoop(ctx context.Context, ready *atomic.Bool, client *http.Client, hub, name, token string) {
	body, _ := json.Marshal(map[string]string{"version": "0.1.0", "status": "healthy"})
	send := func() {
		// Registration is an availability claim. When the controller cache has
		// not synchronized (or the manager exited), do not refresh the hub TTL.
		if ready == nil || !ready.Load() {
			return
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, hub+"/api/providers/"+name+"/heartbeat", bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("heartbeat: %v", err)
			return
		}
		_ = resp.Body.Close()
	}
	send()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			send()
		}
	}
}
