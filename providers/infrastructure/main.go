// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// infrastructure is a faros provider that brokers application
// templates from a central kro (Kube Resource Orchestrator) cluster
// into faros tenant workspaces. See /Users/mjudeikis/.claude/plans/
// zippy-baking-jellyfish.md for the staged plan + design notes.
//
// Routes on a single port ($PORT, default 8081):
//
//   - /, /main.js, /icon.svg, /assets/*  — embedded Vite bundle
//   - /healthz, /readyz                  — process liveness and controller readiness
//   - /mcp, /mcp/sse                     — MCP transport
//
// Templates and instances are NOT served as REST here: the portal and
// tenants drive them as CRDs directly against kcp
// (templates.infrastructure.faros.sh + instances.infrastructure.faros.sh),
// projected to tenant workspaces through the APIExport.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"k8s.io/client-go/rest"

	"github.com/faroshq/provider-infrastructure/mcpserver"
	"github.com/faroshq/provider-infrastructure/server"
	"github.com/faroshq/provider-infrastructure/tenant"
)

// Subcommands:
//
//	infrastructure-provider init
//	    One-shot bootstrap with admin credentials. Seeds the provider's
//	    kcp workspace: installs CRDs, registers APIExport schemas,
//	    creates the CachedResource projection, mints a ServiceAccount
//	    + RBAC + bearer, writes a kubeconfig the runtime mode reads,
//	    and seeds the kro install with a Secret pointing at the
//	    APIExport virtual workspace. Exits when done.
//
//	infrastructure-provider serve  (default if no subcommand)
//	    Runtime. Reads the minted kubeconfig from INFRASTRUCTURE_KUBECONFIG
//	    (or the legacy INFRASTRUCTURE_CONTROLLER_KUBECONFIG fallback) and
//	    starts the REST + portal + MCP server, plus the platform
//	    controller manager. Does NOT need admin credentials.
//
// The split lets dev clusters run init once (Makefile target) and
// keeps the long-lived process scoped to the minted SA's grants.
func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "init":
			if err := runInit(); err != nil {
				fmt.Fprintln(os.Stderr, "init:", err)
				os.Exit(1)
			}
			return
		case "operator":
			if err := runOperator(); err != nil {
				fmt.Fprintln(os.Stderr, "operator:", err)
				os.Exit(1)
			}
			return
		case "controller":
			if err := runController(); err != nil {
				fmt.Fprintln(os.Stderr, "controller:", err)
				os.Exit(1)
			}
			return
		case "serve":
			// Fall through to runServe below.
		default:
			fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[1])
			fmt.Fprintln(os.Stderr, "usage: infrastructure-provider [init|operator|controller|serve]")
			os.Exit(2)
		}
	}
	runServe()
}

// runInit is the high-privilege one-shot bootstrap. Implementation
// lives in the install/ package so it can be invoked from tests or
// a future controller pod independently of main.go.
//
// Expects an admin kubeconfig at INFRASTRUCTURE_ADMIN_KUBECONFIG (or
// the standard KUBECONFIG fallback). Writes a minted kubeconfig to
// INFRASTRUCTURE_KUBECONFIG (defaults to ./infrastructure.kubeconfig).
func runInit() error {
	// Implementation is in init_cmd.go so this file stays focused on
	// process orchestration. See that file for the chain of install
	// steps (CRDs → APIExport schemas → CachedResource → SA + RBAC →
	// token → runtime kubeconfig).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return runInitCmd(ctx)
}

// runServe is the existing main loop, moved into its own function so
// runInit can short-circuit without touching it.
func runServe() {
	// Try the provider's kcp connection immediately. Required mode retries a
	// missing config and shares the recovered value across the controller and
	// request surfaces. The MCP tenant client borrows only host + TLS; every
	// request authenticates with the caller's bearer token.
	kcpConfig, kcpErr := loadControllerConfig()
	if kcpErr != nil {
		log.Printf("kcp config unavailable (%v); kubeconfig-dependent surfaces will retry when controller mode is required", kcpErr)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	serveWithConfigAndLoader(ctx, kcpConfig, loadControllerConfig)
}

// serveWithConfig runs the HTTP/MCP server + controller manager + heartbeat
// against the supplied kcp config, blocking until ctx is cancelled. The caller
// owns ctx (runServe wires signals; the operator shares its own ctx with the
// bootstrap loop). A nil config stays REST-only in explicit REST-only mode; in
// required mode the server stays live and atomically installs all config-backed
// surfaces when the controller lifecycle loads the kubeconfig.
func serveWithConfig(ctx context.Context, kcpConfig *rest.Config) {
	serveWithConfigAndLoader(ctx, kcpConfig, nil)
}

func serveWithConfigAndLoader(ctx context.Context, kcpConfig *rest.Config, reloadConfig func() (*rest.Config, error)) {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	fileServer, distFS, err := portalHandler()
	if err != nil {
		log.Fatalf("portal embed: %v", err)
	}

	health := newControllerHealth(controllerModeFromEnv(kcpConfig != nil) == controllerModeRequired)
	instanceHealth := newControllerHealth(instanceControllerConfigured())
	buildHandler := func(config *rest.Config) http.Handler {
		// Data-plane subresource proxy (logs/sync/restart/preview proxy/status).
		// nil in REST-only/dev (no kcp or runtime cluster); the handler then
		// reports 503 so the route exists but is clearly unavailable. Shared with
		// MCP so the dev_* tools drive the same verbs in-process.
		var dataPlaneHandler http.Handler
		if h := buildDataPlaneHandler(config); h != nil {
			dataPlaneHandler = h
		}
		mcpHandler := mcpserver.NewHandler(mcpserver.Deps{
			Tenant:    tenant.NewClientFactory(config),
			DataPlane: dataPlaneHandler,
		})
		return server.New(server.Deps{
			MCP:              mcpHandler,
			DataPlane:        dataPlaneHandler,
			WorkloadIdentity: buildWorkloadIdentityReviewHandler(),
			PortalFileServer: fileServer,
			PortalFS:         distFS,
			ServePortalAsset: servePortalAsset,
			Readiness: func() server.Readiness {
				return aggregateReadiness(health, instanceHealth)
			},
		})
	}

	// Keep liveness and the portal available while a required kubeconfig is
	// still being delivered. The controller lifecycle installs the recovered
	// config into every request surface before it can mark readiness true.
	surfaces := newServeSurfaces(buildHandler(nil), buildHandler, startInstanceController)

	httpSrv := &http.Server{
		Addr:              ":" + port,
		Handler:           surfaces,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("infrastructure provider listening on :%s (tenant=%v mcp=true)", port, kcpConfig != nil)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	// Provider bootstrap and serve ordering is not guaranteed. Own the manager
	// lifecycle here so setup failures and post-start exits both make readiness
	// false and retry with a fresh manager.
	go func() {
		loadConfig := controllerConfigLoader(kcpConfig, reloadConfig)
		start := func(startCtx context.Context, config *rest.Config, healthState *controllerHealth) error {
			return runControllerAttempt(startCtx, config, healthState, instanceHealth, surfaces, startControllerManager)
		}
		runControllerManager(ctx, health, loadConfig, start, controllerRetryInterval)
	}()

	go runHeartbeat(ctx, health, instanceHealth)

	<-ctx.Done()
	log.Printf("shutting down")
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdown); err != nil {
		log.Printf("shutdown error: %v", err)
	}
}

func runControllerAttempt(
	ctx context.Context,
	config *rest.Config,
	platformHealth, instanceHealth *controllerHealth,
	surfaces *serveSurfaces,
	startManager func(context.Context, *rest.Config, *controllerHealth) error,
) error {
	attemptCtx, cancelAttempt := context.WithCancel(ctx)
	instanceResult := surfaces.configure(attemptCtx, config, instanceHealth)
	managerResult := make(chan error, 1)
	go func() {
		managerResult <- startManager(attemptCtx, config, platformHealth)
	}()

	// The first terminal signal ends the attempt, but not this function: cancel
	// the shared child context and join every started controller before returning
	// to the retry loop. This prevents a slow old manager from reconciling or
	// mutating shared health alongside its replacement.
	managerCh := (<-chan error)(managerResult)
	instanceCh := (<-chan error)(instanceResult)
	remaining := 1
	if instanceCh != nil {
		remaining++
	}
	ctxDone := ctx.Done()
	var firstErr error
	firstTerminal := true
	for remaining > 0 {
		var source string
		var err error
		select {
		case result, ok := <-managerCh:
			source = "platform"
			if ok {
				err = result
			}
			managerCh = nil
			remaining--
		case result, ok := <-instanceCh:
			source = "instance"
			if ok {
				err = result
			}
			instanceCh = nil
			remaining--
		case <-ctxDone:
			source = "controller attempt"
			err = ctx.Err()
			ctxDone = nil
		}
		if firstTerminal {
			if err == nil {
				err = fmt.Errorf("%s exited without an error", source)
			}
			firstErr = err
			firstTerminal = false
			ctxDone = nil
			cancelAttempt()
		}
	}
	cancelAttempt()
	return firstErr
}

func controllerConfigLoader(initial *rest.Config, reload func() (*rest.Config, error)) func() (*rest.Config, error) {
	if reload != nil {
		// CLI serve owns a file-backed config source. Re-read it on every manager
		// attempt so a corrected Secret/token/host replaces a successfully parsed
		// but unusable startup config.
		return reload
	}
	// Operator mode passes an explicit in-memory config and owns its rotation by
	// restarting/reconciling the serve process.
	return func() (*rest.Config, error) {
		if initial == nil {
			return nil, errControllerDisabled
		}
		return rest.CopyConfig(initial), nil
	}
}

// serveSurfaces atomically replaces the degraded REST-only route set after a
// late provider kubeconfig arrives. Every manager attempt replaces MCP and the
// data-plane and starts the Instance controller with the same attempt-scoped
// config/context. A failed attempt is cancelled before its replacement starts.
type serveSurfaces struct {
	mu            sync.RWMutex
	handler       http.Handler
	build         func(*rest.Config) http.Handler
	startInstance func(context.Context, *rest.Config, *controllerHealth) <-chan error
}

func newServeSurfaces(
	initial http.Handler,
	build func(*rest.Config) http.Handler,
	startInstance func(context.Context, *rest.Config, *controllerHealth) <-chan error,
) *serveSurfaces {
	return &serveSurfaces{
		handler:       initial,
		build:         build,
		startInstance: startInstance,
	}
}

func (s *serveSurfaces) configure(ctx context.Context, config *rest.Config, instanceHealth *controllerHealth) <-chan error {
	if s == nil || config == nil {
		return nil
	}
	handler := s.build(config)
	s.mu.Lock()
	s.handler = handler
	s.mu.Unlock()
	if s.startInstance != nil {
		return s.startInstance(ctx, config, instanceHealth)
	}
	return nil
}

func (s *serveSurfaces) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	handler := s.handler
	s.mu.RUnlock()
	handler.ServeHTTP(w, r)
}

func aggregateReadiness(platform, instance *controllerHealth) server.Readiness {
	platformSnapshot := platform.snapshot()
	if !platform.ready() {
		return server.Readiness{Controller: string(platformSnapshot.State), Error: platformSnapshot.Error}
	}
	instanceSnapshot := instance.snapshot()
	if !instance.ready() {
		return server.Readiness{
			Controller: "instance-" + string(instanceSnapshot.State),
			Error:      instanceSnapshot.Error,
		}
	}
	state := platformSnapshot.State
	if state == "" {
		state = controllerStateRESTOnly
	}
	return server.Readiness{Ready: true, Controller: string(state)}
}
