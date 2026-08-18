// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	repositorysynccontroller "github.com/faroshq/provider-deployments/controller/repositorysync"
)

func main() {
	command, err := providerCommand(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "usage: deployments-provider [init|serve]")
		os.Exit(2)
	}
	if command == "init" {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		if err := runInitCmd(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "init:", err)
			os.Exit(1)
		}
		return
	}
	if err := runServe(); err != nil {
		fmt.Fprintln(os.Stderr, "serve:", err)
		os.Exit(1)
	}
}

func providerCommand(args []string) (string, error) {
	if len(args) == 0 {
		return "serve", nil
	}
	if len(args) != 1 {
		return "", fmt.Errorf("expected exactly one subcommand, got %d arguments", len(args))
	}
	switch args[0] {
	case "init", "serve":
		return args[0], nil
	default:
		return "", fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func runServe() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	var ready atomic.Bool
	cfg, err := loadControllerConfig()
	if err != nil {
		return fmt.Errorf("controller config: %w", err)
	}
	managerExited := make(chan error, 1)
	fetcher, err := repositorysynccontroller.NewHTTPBundleFetcher(os.Getenv("DEPLOYMENTS_CODE_URL"))
	if err != nil {
		return fmt.Errorf("Code provider client: %w", err)
	}
	source := repositorysynccontroller.NewCodeRepositoryCheckoutReader(fetcher)
	if err := startControllerManager(ctx, cfg, &ready, stop, managerExited, source); err != nil {
		return fmt.Errorf("controller manager: %w", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/readyz", readinessHandler(&ready))
	fileServer, distFS, err := portalHandler()
	if err != nil {
		return fmt.Errorf("portal embed: %w", err)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		clean := strings.TrimPrefix(r.URL.Path, "/")
		if clean != "" && servePortalAsset(w, r, distFS, clean) {
			return
		}
		fallback := r.Clone(r.Context())
		fallback.URL.Path = "/"
		fileServer.ServeHTTP(w, fallback)
	})
	port := os.Getenv("PORT")
	if port == "" {
		port = "8093"
	}
	srv := &http.Server{Addr: ":" + port, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	serverExited := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("server: %v", err)
			serverExited <- err
			stop()
		}
	}()
	go runHeartbeat(ctx, &ready)
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil {
		return fmt.Errorf("shutdown HTTP server: %w", err)
	}
	select {
	case err := <-managerExited:
		if err != nil {
			return fmt.Errorf("controller manager exited: %w", err)
		}
	default:
	}
	select {
	case err := <-serverExited:
		return fmt.Errorf("HTTP server exited: %w", err)
	default:
	}
	return nil
}

func readinessHandler(ready *atomic.Bool) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			http.Error(w, "controller manager not ready", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok\n"))
	}
}
