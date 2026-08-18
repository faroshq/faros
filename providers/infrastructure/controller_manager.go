// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package main

// Platform controller manager — the one that reconciles Template CRs
// into backend setup (kro RGDs). Lives alongside the legacy
// REST surface; the two coexist for PRs A-D and the REST handlers get
// deleted in PR E once the UI + MCP have migrated to the kcp-native
// path.
//
// The manager is OPT-IN via INFRASTRUCTURE_CONTROLLER_KUBECONFIG (or
// the standard KUBECONFIG fallback). When neither is set the provider
// runs as it does today: REST broker, no controller. That keeps the
// dev-mode/stub flow intact while the new code lands.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/faroshq/provider-infrastructure/backend"
	krobackend "github.com/faroshq/provider-infrastructure/backend/kro"
	"github.com/faroshq/provider-infrastructure/backend/stub"
	"github.com/faroshq/provider-infrastructure/controller/template"
	"github.com/faroshq/provider-infrastructure/install"
)

// controllerReadiness reports lifecycle transitions back to the HTTP
// readiness gate. The manager is not ready when it is merely constructed: the
// provider cache must synchronize first, and an actual backend must be
// registered before /readyz can pass.
type controllerReadiness struct {
	mu       sync.Mutex
	terminal bool

	onBackendReady func()
	onReady        func()
	onStopped      func(error)
}

func (r *controllerReadiness) backendReady() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.terminal || r.onBackendReady == nil {
		return
	}
	r.onBackendReady()
}

func (r *controllerReadiness) ready() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.terminal || r.onReady == nil {
		return
	}
	r.onReady()
}

func (r *controllerReadiness) stopped(err error) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.terminal {
		return
	}
	r.terminal = true
	if r.onStopped != nil {
		r.onStopped(err)
	}
}

// startControllerManager builds a controller-runtime manager pointed
// at the provider's own kcp workspace, installs the platform CRDs,
// registers the stub backend, and starts the Template controller.
// The caller loads the kcp config (shared with the tenant client) and
// passes it in; a nil config means "skip the manager, run REST-only".
func startControllerManager(ctx context.Context, config *rest.Config, readiness ...*controllerReadiness) error {
	var lifecycle *controllerReadiness
	if len(readiness) > 0 {
		lifecycle = readiness[0]
	}
	if config == nil {
		lifecycle.stopped(errControllerDisabled)
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
		Metrics: metricsserver.Options{BindAddress: "0"},
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
	// The stub is a valid backend for the intentionally supported local
	// REST-only/demo flow; a kro backend is added above when a runtime cluster is
	// configured. In either case, readiness must wait until registration has
	// completed before the manager cache can advertise the provider as usable.
	// A runtime cluster selects kro as the required backend. The stub is only
	// the intentionally supported local/demo backend when no runtime cluster is
	// configured. Verify the selected backend is actually in the registry before
	// acknowledging backend readiness; registering an unconditional stub must
	// never mask a configured-but-unavailable kro backend.
	requiredBackend := stub.Name
	if kroCfg != nil {
		requiredBackend = krobackend.Name
	}
	if _, ok := registry.Get(requiredBackend); !ok {
		return fmt.Errorf("required backend %q is not registered", requiredBackend)
	}
	if err := (&template.Reconciler{
		Client:   mgr.GetClient(),
		Backends: registry,
	}).SetupWithManager(mgr); err != nil {
		return fmt.Errorf("template controller: %w", err)
	}
	// The selected backend is now registered and the controller that dispatches
	// to it is wired. Cache sync below still gates provider/controller
	// readiness, but backend readiness must not be acknowledged if setup failed.
	lifecycle.backendReady()

	go func() {
		log.Printf("infrastructure controller manager starting (backends=%v)", registry.Names())
		if err := mgr.Start(ctx); err != nil {
			log.Printf("controller manager exited: %v", err)
			lifecycle.stopped(fmt.Errorf("controller manager exited: %w", err))
			return
		}
		lifecycle.stopped(errors.New("controller manager stopped"))
	}()
	go func() {
		// WaitForCacheSync observes the actual provider cache, not merely the
		// asynchronous Start call. This keeps /readyz false until the manager
		// can serve the API resources its controllers reconcile.
		if mgr.GetCache().WaitForCacheSync(ctx) && ctx.Err() == nil {
			lifecycle.ready()
			return
		}
		if ctx.Err() == nil {
			lifecycle.stopped(errors.New("controller manager cache did not synchronize"))
		}
	}()
	return nil
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
