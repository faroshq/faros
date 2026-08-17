// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package main

// Platform controller manager — the one that reconciles Template CRs
// into per-template CRDs + backend setup. Lives alongside the legacy
// REST surface; the two coexist for PRs A-D and the REST handlers get
// deleted in PR E once the UI + MCP have migrated to the kcp-native
// path.
//
// The manager is required whenever a controller config is supplied or
// configured. Explicit REST-only mode remains available for deployments that
// intentionally expose only the legacy REST surface.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/faroshq/provider-infrastructure/backend"
	krobackend "github.com/faroshq/provider-infrastructure/backend/kro"
	"github.com/faroshq/provider-infrastructure/backend/stub"
	"github.com/faroshq/provider-infrastructure/controller/template"
	"github.com/faroshq/provider-infrastructure/install"
)

const (
	controllerRetryInterval              = 15 * time.Second
	controllerPublicationStartupInterval = time.Second
	controllerPublicationReadyInterval   = 30 * time.Second
)

type controllerMode string

const (
	controllerModeRESTOnly controllerMode = "rest-only"
	controllerModeRequired controllerMode = "required"
)

type controllerState string

const (
	controllerStateRESTOnly controllerState = "rest-only"
	controllerStateStarting controllerState = "starting"
	controllerStateReady    controllerState = "ready"
	controllerStateFailed   controllerState = "failed"
	controllerStateStopped  controllerState = "stopped"
)

type controllerHealthSnapshot struct {
	Required bool
	State    controllerState
	Error    string
}

type controllerHealth struct {
	mu       sync.RWMutex
	required bool
	state    controllerState
	lastErr  string
}

func newControllerHealth(required bool) *controllerHealth {
	state := controllerStateRESTOnly
	if required {
		state = controllerStateStarting
	}
	return &controllerHealth{required: required, state: state}
}

func (h *controllerHealth) snapshot() controllerHealthSnapshot {
	if h == nil {
		return controllerHealthSnapshot{State: controllerStateRESTOnly}
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return controllerHealthSnapshot{Required: h.required, State: h.state, Error: h.lastErr}
}

func (h *controllerHealth) set(state controllerState, err error) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.state = state
	h.lastErr = ""
	if err != nil {
		h.lastErr = err.Error()
	}
}

func (h *controllerHealth) markStarting() { h.set(controllerStateStarting, nil) }
func (h *controllerHealth) markReady()    { h.set(controllerStateReady, nil) }
func (h *controllerHealth) markFailed(err error) {
	h.set(controllerStateFailed, err)
}
func (h *controllerHealth) markStopped(err error) {
	h.set(controllerStateStopped, err)
}
func (h *controllerHealth) markRESTOnly() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.required = false
	h.state = controllerStateRESTOnly
	h.lastErr = ""
}

func (h *controllerHealth) ready() bool {
	snapshot := h.snapshot()
	return !snapshot.Required || snapshot.State == controllerStateReady
}

// startControllerManager builds a controller-runtime manager pointed
// at the provider's own kcp workspace, installs the platform CRDs,
// registers the stub backend, and starts the Template controller.
// The caller loads the kcp config (shared with the tenant client) and
// passes it in; a nil config means "skip the manager, run REST-only".
func startControllerManager(ctx context.Context, config *rest.Config, health *controllerHealth) error {
	if config == nil {
		return errControllerDisabled
	}

	// In the init/serve split (INFRASTRUCTURE_KUBECONFIG set), init has
	// already done all the high-privilege bootstrap. Serve runs with a
	// narrow SA that doesn't have all the rights needed to re-apply
	// CachedResources, so we MUST skip these calls. In the legacy
	// single-binary mode we still run them so dev clusters that haven't
	// migrated to init/serve keep working.
	if os.Getenv("INFRASTRUCTURE_KUBECONFIG") == "" {
		if err := install.CRDs(ctx, config); err != nil {
			return fmt.Errorf("install CRDs: %w", err)
		}
		// Legacy single-binary path: CachedResource + EndpointSlice before
		// APIExport so templates use virtual storage. Templates MUST be served
		// via virtual storage (to project into tenant workspaces) — never fall
		// back to CRD storage; fail so a restart retries until the identityHash
		// is ready.
		if err := install.PlatformCachedResources(ctx, config); err != nil {
			return fmt.Errorf("install CachedResources: %w", err)
		}
		if err := install.PlatformCachedResourceEndpointSlices(ctx, config); err != nil {
			return fmt.Errorf("install EndpointSlice: %w", err)
		}
		hash, err := install.WaitForCachedResourceIdentity(ctx, config)
		if err != nil {
			return fmt.Errorf("CachedResource identityHash not ready (templates require virtual storage): %w", err)
		}
		if hash == "" {
			return fmt.Errorf("CachedResource identityHash empty (templates require virtual storage)")
		}
		if err := install.PlatformSchemaInAPIExport(ctx, config, hash); err != nil {
			return fmt.Errorf("register platform schemas on APIExport: %w", err)
		}
	}

	// Register controller-runtime's logger once before building the
	// manager. Without this, the first internal log call (e.g. the
	// priorityqueue depth report) prints a "log.SetLogger(...) was never
	// called" stack trace and swallows all controller-runtime logs.
	ctrl.SetLogger(klog.NewKlogr())

	mgr, err := manager.New(config, manager.Options{
		// Disable the metrics server in PR A; the bind on :8080 would
		// collide with the provider's own HTTP server in dev. PR E
		// adds it back on a configurable port.
		Metrics:    metricsserver.Options{BindAddress: "0"},
		Controller: controllerOptionsForRetryableManager(),
	})
	if err != nil {
		return fmt.Errorf("manager.New: %w", err)
	}

	registry := backend.NewRegistry()
	if err := registry.Register(stub.New()); err != nil {
		return fmt.Errorf("register stub backend: %w", err)
	}

	// kro backend: authors RGDs on the runtime cluster (where the kro
	// controller watches RGDs — a kind cluster in dev), NOT this provider's
	// kcp workspace. It needs a separate client; KRO_KUBECONFIG points at
	// that cluster (the same kubeconfig the legacy kro broker reads). When
	// unset we run stub-only so dev/REST-only flows still boot.
	// Resolve the kro runtime cluster: explicit KRO_KUBECONFIG, else the pod's
	// in-cluster config (the operator's in-cluster-runtime mode — serve runs in
	// the runtime cluster and authors RGDs against it via its pod SA). Falls
	// back to stub-only when neither is available (dev/REST-only).
	var kroCfg *rest.Config
	var kroSrc string
	if p := os.Getenv("KRO_KUBECONFIG"); p != "" {
		c, err := clientcmd.BuildConfigFromFlags("", p)
		if err != nil {
			return fmt.Errorf("loading KRO_KUBECONFIG for kro backend: %w", err)
		}
		kroCfg, kroSrc = c, "KRO_KUBECONFIG="+p
	} else if c, err := rest.InClusterConfig(); err == nil {
		kroCfg, kroSrc = c, "in-cluster"
	}
	if kroCfg != nil {
		kroDyn, err := dynamic.NewForConfig(kroCfg)
		if err != nil {
			return fmt.Errorf("kro backend dynamic client: %w", err)
		}
		if err := registry.Register(krobackend.New(kroDyn)); err != nil {
			return fmt.Errorf("register kro backend: %w", err)
		}
		log.Printf("controller manager: kro backend registered (RGD runtime cluster: %s)", kroSrc)
	} else {
		log.Printf("controller manager: no kro runtime config (KRO_KUBECONFIG unset, not in a pod) — kro backend not registered (stub-only)")
	}

	dyn, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("dynamic client: %w", err)
	}

	if err := (&template.Reconciler{
		Client:   mgr.GetClient(),
		Dynamic:  dyn,
		Backends: registry,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("template controller: %w", err)
	}

	var seedTemplatesReady func(context.Context) (bool, error)
	if os.Getenv("INFRASTRUCTURE_SKIP_SEED_TEMPLATES") == "" {
		required, err := install.RequiredSeedTemplateResources(registry.Names())
		if err != nil {
			return fmt.Errorf("derive required seed template resources: %w", err)
		}
		if len(required) > 0 {
			seedTemplatesReady = func(ctx context.Context) (bool, error) {
				return install.SeedTemplatesReady(ctx, dyn, required)
			}
		}
	}
	if err := mgr.Add(controllerReadyRunnable(health, seedTemplatesReady)); err != nil {
		return fmt.Errorf("controller health runnable: %w", err)
	}

	log.Printf("infrastructure controller manager starting (backends=%v)", registry.Names())
	if err := mgr.Start(ctx); err != nil {
		return fmt.Errorf("controller manager exited: %w", err)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.New("controller manager exited without an error")
}

func controllerOptionsForRetryableManager() ctrlconfig.Controller {
	// The lifecycle reconstructs this manager after readiness loss while
	// retaining stable controller names for metrics and logs. controller-runtime
	// keeps its name registry for the process lifetime, so a replacement manager
	// must skip that process-global check. runControllerAttempt cancels and joins
	// the old manager before starting its replacement, so the stable name cannot
	// identify two concurrently running controllers.
	skipNameValidation := true
	return ctrlconfig.Controller{SkipNameValidation: &skipNameValidation}
}

func controllerReadyRunnable(health *controllerHealth, seedTemplatesReady func(context.Context) (bool, error)) manager.Runnable {
	return controllerReadyRunnableWithIntervals(
		health,
		seedTemplatesReady,
		controllerPublicationStartupInterval,
		controllerPublicationReadyInterval,
	)
}

func controllerReadyRunnableWithInterval(health *controllerHealth, seedTemplatesReady func(context.Context) (bool, error), interval time.Duration) manager.Runnable {
	return controllerReadyRunnableWithIntervals(health, seedTemplatesReady, interval, interval)
}

func controllerReadyRunnableWithIntervals(
	health *controllerHealth,
	seedTemplatesReady func(context.Context) (bool, error),
	startupInterval time.Duration,
	readyInterval time.Duration,
) manager.Runnable {
	return manager.RunnableFunc(func(ctx context.Context) error {
		if seedTemplatesReady == nil {
			health.markReady()
			<-ctx.Done()
			return nil
		}

		for {
			ready, err := seedTemplatesReady(ctx)
			if err != nil {
				return fmt.Errorf("check seed template readiness: %w", err)
			}
			interval := startupInterval
			if ready {
				health.markReady()
				interval = readyInterval
			} else {
				health.markStarting()
			}
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			}
		}
	})
}

func controllerModeFromEnv(configured bool) controllerMode {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("INFRASTRUCTURE_CONTROLLER_MODE"))) {
	case string(controllerModeRESTOnly), "rest_only", "rest":
		return controllerModeRESTOnly
	case string(controllerModeRequired), "controller":
		return controllerModeRequired
	case "":
		if strings.EqualFold(strings.TrimSpace(os.Getenv("INFRASTRUCTURE_REST_ONLY")), "true") {
			return controllerModeRESTOnly
		}
		if configured || strings.TrimSpace(os.Getenv("INFRASTRUCTURE_KUBECONFIG")) != "" ||
			strings.TrimSpace(os.Getenv("INFRASTRUCTURE_CONTROLLER_KUBECONFIG")) != "" ||
			strings.TrimSpace(os.Getenv("KUBECONFIG")) != "" {
			return controllerModeRequired
		}
		return controllerModeRESTOnly
	default:
		log.Printf("unknown INFRASTRUCTURE_CONTROLLER_MODE=%q; requiring controller", os.Getenv("INFRASTRUCTURE_CONTROLLER_MODE"))
		return controllerModeRequired
	}
}

func runControllerManager(
	ctx context.Context,
	health *controllerHealth,
	loadConfig func() (*rest.Config, error),
	start func(context.Context, *rest.Config, *controllerHealth) error,
	retryInterval time.Duration,
) {
	runControllerManagerWithRetryGate(ctx, health, loadConfig, start, retryInterval, waitControllerRetry)
}

func waitControllerRetry(ctx context.Context, retryInterval time.Duration) bool {
	if retryInterval < 0 {
		retryInterval = 0
	}
	timer := time.NewTimer(retryInterval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func runControllerManagerWithRetryGate(
	ctx context.Context,
	health *controllerHealth,
	loadConfig func() (*rest.Config, error),
	start func(context.Context, *rest.Config, *controllerHealth) error,
	retryInterval time.Duration,
	retryGate func(context.Context, time.Duration) bool,
) {
	if health == nil {
		health = newControllerHealth(true)
	}
	if !health.snapshot().Required {
		health.markRESTOnly()
		log.Printf("controller manager disabled: explicit REST-only mode")
		return
	}
	if loadConfig == nil || start == nil {
		err := errors.New("controller manager lifecycle dependencies are not configured")
		health.markFailed(err)
		log.Printf("controller manager not ready: %v", err)
		return
	}

	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			health.markStopped(err)
			return
		}
		health.markStarting()

		config, err := loadConfig()
		if err == nil {
			// Everything initialized by one attempt (manager, request surfaces,
			// auxiliary controllers) shares a child context. Cancel it before a
			// retry so a corrected kubeconfig cannot leave stale controllers from
			// the failed attempt running beside the replacement.
			attemptCtx, cancelAttempt := context.WithCancel(ctx)
			err = start(attemptCtx, config, health)
			cancelAttempt()
			if err == nil && ctx.Err() == nil {
				err = errors.New("controller manager exited without an error")
			}
		}
		if ctx.Err() != nil {
			health.markStopped(ctx.Err())
			return
		}
		if err == nil {
			err = errors.New("controller manager exited without an error")
		}
		health.markFailed(err)
		log.Printf("controller manager not ready (attempt %d): %v; retrying in %s", attempt, err, retryInterval)

		if retryGate == nil {
			retryGate = waitControllerRetry
		}
		if !retryGate(ctx, retryInterval) {
			health.markStopped(ctx.Err())
			return
		}
	}
}

// loadControllerConfig returns a rest.Config for the workspace the
// platform controllers target. Looked up in this order:
//
//	INFRASTRUCTURE_KUBECONFIG             — minted SA kubeconfig from `init`
//	INFRASTRUCTURE_CONTROLLER_KUBECONFIG  — legacy provider-specific override
//	KUBECONFIG                            — standard env var
//	in-cluster service account            — when run as a pod
//
// The minted path wins because serve mode is supposed to run with
// the lowest-privilege identity available. If init has already run,
// INFRASTRUCTURE_KUBECONFIG points at a SA token bound to the
// narrow ClusterRole in install/identity.go. The remaining entries
// stay as escape hatches for dev clusters that haven't migrated to
// the init/serve split.
//
// Returns errControllerDisabled when none of the four resolve; the
// caller logs + continues without the controller.
func loadControllerConfig() (*rest.Config, error) {
	c, err := loadControllerConfigRaw()
	if err != nil {
		return nil, err
	}
	// When INFRASTRUCTURE_WORKSPACE_PATH is set, retarget the config host at
	// /clusters/<path>. This lets serve run with a root-scoped (admin)
	// kubeconfig pointed at the provider workspace — so the operator-driven
	// flow no longer needs `init` to mint a workspace-scoped kubeconfig.
	// Idempotent: an already workspace-scoped kubeconfig (prod) is unchanged.
	if ws := os.Getenv("INFRASTRUCTURE_WORKSPACE_PATH"); ws != "" {
		host, herr := retargetHostToWorkspace(c.Host, ws)
		if herr != nil {
			return nil, fmt.Errorf("retarget controller kubeconfig to workspace %q: %w", ws, herr)
		}
		c.Host = host
	}
	return c, nil
}

func loadControllerConfigRaw() (*rest.Config, error) {
	if p := os.Getenv("INFRASTRUCTURE_KUBECONFIG"); p != "" {
		c, err := clientcmd.BuildConfigFromFlags("", p)
		if err != nil {
			return nil, fmt.Errorf("INFRASTRUCTURE_KUBECONFIG: %w", err)
		}
		return c, nil
	}
	if p := os.Getenv("INFRASTRUCTURE_CONTROLLER_KUBECONFIG"); p != "" {
		c, err := clientcmd.BuildConfigFromFlags("", p)
		if err != nil {
			return nil, fmt.Errorf("INFRASTRUCTURE_CONTROLLER_KUBECONFIG: %w", err)
		}
		return c, nil
	}
	if p := os.Getenv("KUBECONFIG"); p != "" {
		c, err := clientcmd.BuildConfigFromFlags("", p)
		if err != nil {
			return nil, fmt.Errorf("KUBECONFIG: %w", err)
		}
		return c, nil
	}
	// In-cluster fallback. The error returned by InClusterConfig is
	// the right "not running in a pod" signal so we let it surface
	// up the chain as errControllerDisabled.
	c, err := rest.InClusterConfig()
	if err != nil {
		return nil, errControllerDisabled
	}
	return c, nil
}

// errControllerDisabled is the sentinel main() checks for so it can
// log + continue without the manager when no kubeconfig is in scope.
var errControllerDisabled = errors.New("no kubeconfig available; controller manager disabled")
