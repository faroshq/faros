// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// secrets is a faros provider that projects externally-stored secrets into
// tenant workspaces. See docs/plan-secrets-mcp-governance-edge-autonomy.md
// (workstream A.2) for the design.
//
// Routes on a single port ($PORT, default 8090):
//
//   - /, /main.js, /icon.svg, /assets/*  — embedded Vite bundle
//   - /healthz                           — liveness; gates BackendHealthy
//
// SecretStore / SyncedSecret are NOT served as REST here: the portal and
// tenants drive them as CRDs directly against kcp (secrets.faros.sh),
// projected to tenant workspaces via the APIExport. The controllers reconcile
// them across all tenant workspaces (controller_manager.go).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/faroshq/provider-secrets/backend"
	"github.com/faroshq/provider-secrets/backend/stub"
	"github.com/faroshq/provider-secrets/backend/vault"
)

// Subcommands:
//
//	secrets-provider init
//	    One-shot bootstrap (thin — see init_cmd.go). The hub provisioner
//	    already creates the sub-workspace, schemas, APIExport, SA, and
//	    kubeconfig from the CatalogEntry, so init only fills any gaps the
//	    provider's own multicluster manager needs (e.g. an
//	    APIExportEndpointSlice). Exits when done.
//
//	secrets-provider serve  (default if no subcommand)
//	    Runtime. Reads the minted kubeconfig from FAROS_PROVIDER_KUBECONFIG
//	    and starts the portal server plus the multicluster controller
//	    manager. Does NOT need admin credentials.
func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "init":
			if err := runInit(); err != nil {
				log.Printf("init: %v", err)
				os.Exit(1)
			}
			return
		case "serve":
			// Fall through to runServe below.
		default:
			log.Printf("unknown subcommand: %s", os.Args[1])
			log.Printf("usage: secrets-provider [init|serve]")
			os.Exit(2)
		}
	}
	runServe()
}

func runInit() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return runInitCmd(ctx)
}

func runServe() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8090"
	}

	// Load the provider's kcp connection once: the controller manager is the
	// only kcp consumer. nil config => portal-only dev.
	kcpConfig, kcpErr := loadControllerConfig()
	if kcpErr != nil {
		log.Printf("kcp config unavailable (%v); controller manager disabled", kcpErr)
	}

	// Store backends, registered once and dispatched per SecretStore by the
	// controllers. No backend holds a global credential — every SecretStore
	// authenticates with its own tenant-referenced token.
	backends := backend.NewRegistry()
	if os.Getenv("SECRETS_DEV_STUB_BACKEND") == "true" {
		log.Printf("SECRETS_DEV_STUB_BACKEND=true: canned in-memory backend stands in for vault")
		if err := backends.Register(stub.New()); err != nil {
			log.Fatalf("register stub backend: %v", err)
		}
	} else if err := backends.Register(vault.New()); err != nil {
		log.Fatalf("register vault backend: %v", err)
	}

	fileServer, distFS, err := portalHandler()
	if err != nil {
		log.Fatalf("portal embed: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.NotFound(w, r)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if servePortalAsset(w, r, distFS, name) {
			return
		}
		// SPA fallback: unknown paths get the index so deep links resolve.
		if !servePortalAsset(w, r, distFS, "index.html") {
			http.NotFound(w, r)
		}
	})
	_ = fileServer // routing handled via servePortalAsset for SPA fallback

	httpSrv := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("secrets provider listening on :%s (kcp=%v backends=%v)", port, kcpConfig != nil, backends.Names())
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	if err := startControllerManager(ctx, kcpConfig, backends); err != nil {
		if errors.Is(err, errControllerDisabled) {
			log.Printf("controller manager: disabled (no kubeconfig); set FAROS_PROVIDER_KUBECONFIG to enable")
		} else {
			log.Printf("controller manager: NOT started: %v", err)
		}
	}

	go runHeartbeat(ctx)

	<-ctx.Done()
	log.Printf("shutting down")
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdown); err != nil {
		log.Printf("shutdown error: %v", err)
	}
}
