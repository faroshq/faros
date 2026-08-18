/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package providers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	providersv1alpha1 "github.com/faroshq/faros/apis/providers/v1alpha1"
)

// CatalogReconciler keeps the in-process Registry and CatalogEntry status in
// sync with provider-owned registration state. On create/update it parses the
// declared runtime endpoints, probes backend health, and verifies the observed
// APIExport identity, stable required resources, every referenced schema, and
// exact permission claims before marking the provider ready. On delete it drops
// the routing entry.
//
// It deliberately does not provision the provider workspace or API surface.
// Admin Provider reconciliation owns workspace/ServiceAccount/kubeconfig
// onboarding; provider init owns APIResourceSchemas, APIExport, endpoint slice,
// bind grant, and CatalogEntry.
type CatalogReconciler struct {
	mgr   mcmanager.Manager
	reg   *Registry
	prov  *Provisioner
	noKCP bool // true when running without kcp — skip workspace-cluster resolve
	// hubExternalURL / hubInternalURL are retained for parity with the
	// onboarding service's kubeconfig minting; the reconciler itself no longer
	// mints kubeconfigs (admin onboarding does).
	hubExternalURL string
	hubInternalURL string
	healthClient   httpDoer
	exportChecker  apiExportChecker
	workspaceOwner catalogWorkspaceOwner
}

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type apiExportChecker interface {
	CheckAPIExport(context.Context, string, string, []APIExportResource, []PermissionClaim) error
}

type catalogWorkspaceOwner interface {
	ResolveCatalogEntryOwnerCluster(context.Context, string, bool) (string, error)
}

const backendHealthTimeout = 3 * time.Second

const catalogBuiltinAnnotation = "providers.faros.sh/builtin"

func defaultBackendHealthClient() *http.Client {
	return &http.Client{
		Timeout: backendHealthTimeout,
		// A provider-controlled redirect must not turn the hub's health probe into
		// a request to a different authority. A 3xx response is unhealthy.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// CatalogReconcilerOptions threads optional extras into the reconciler
// without bloating its constructor signature. All fields optional.
type CatalogReconcilerOptions struct {
	// HubExternalURL / HubInternalURL are kept for symmetry with the
	// admin onboarding service; the catalog controller no longer provisions or
	// mints kubeconfigs, so they are currently unused by the reconciler.
	HubExternalURL string
	HubInternalURL string
}

// SetupCatalogWithManager wires the reconciler into a multicluster manager.
// kcpConfig is the admin rest.Config used for read-only provider-workspace
// checks: resolving each workspace cluster ID and verifying its declared
// APIExport is usable before Enable. Pass nil to run the controller in
// registry-only mode (no kcp reads). The hub no longer provisions providers —
// that moved to admin onboarding + provider Helm init.
func SetupCatalogWithManager(mgr mcmanager.Manager, reg *Registry, kcpConfig *rest.Config, opts CatalogReconcilerOptions) error {
	r := &CatalogReconciler{
		mgr:            mgr,
		reg:            reg,
		noKCP:          kcpConfig == nil,
		hubExternalURL: opts.HubExternalURL,
		hubInternalURL: opts.HubInternalURL,
		healthClient:   defaultBackendHealthClient(),
	}
	if kcpConfig != nil {
		r.prov = NewProvisioner(kcpConfig)
		r.exportChecker = r.prov
		r.workspaceOwner = r.prov
	}
	return mcbuilder.ControllerManagedBy(mgr).
		Named("provider-catalog").
		For(&providersv1alpha1.CatalogEntry{}).
		Complete(r)
}

// Reconcile parses one CatalogEntry and updates the registry + status.
func (r *CatalogReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	logger := klog.FromContext(ctx).WithValues("catalogentry", req.Name, "cluster", req.ClusterName)

	cl, err := r.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		// A logical-cluster removal can make the multicluster manager forget the
		// cluster before the CatalogEntry delete event is reconciled. Fail closed
		// in that case, but preserve spoof resistance: DeleteOwned only removes a
		// route whose last observed CatalogEntry came from this exact cluster.
		if r.reg.DeleteOwned(req.Name, string(req.ClusterName)) {
			logger.Info("Removed provider from registry after catalog cluster became unavailable")
		}
		return ctrl.Result{}, fmt.Errorf("getting cluster %s: %w", req.ClusterName, err)
	}
	c := cl.GetClient()

	var entry providersv1alpha1.CatalogEntry
	if err := c.Get(ctx, req.NamespacedName, &entry); err != nil {
		if apierrors.IsNotFound(err) {
			// The watch spans all APIExport consumers. Only the logical cluster
			// that owns the current route may remove it.
			if r.reg.DeleteOwned(req.Name, string(req.ClusterName)) {
				logger.Info("Removed provider from registry")
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// A consumer of providers.faros.sh can create a CatalogEntry with any
	// metadata.name. Bind the global route identity to the authoritative
	// workspace before parsing endpoints or mutating the registry.
	if r.workspaceOwner != nil {
		builtin := entry.GetAnnotations()[catalogBuiltinAnnotation] == "true"
		if builtin {
			if _, registered := BuiltinByName(entry.Name); !registered {
				logger.Info("Rejected unknown builtin CatalogEntry")
				return ctrl.Result{}, nil
			}
		}
		ownerCluster, err := r.workspaceOwner.ResolveCatalogEntryOwnerCluster(ctx, entry.Name, builtin)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("resolving authoritative CatalogEntry workspace: %w", err)
		}
		if ownerCluster == "" || ownerCluster != string(req.ClusterName) {
			logger.Info("Rejected CatalogEntry from non-authoritative workspace", "authoritativeCluster", ownerCluster)
			return ctrl.Result{}, nil
		}
	}

	// Snapshot the status as observed. Every hub replica runs this reconciler
	// (the registry it maintains is request-path state, so it cannot be
	// leader-gated), which makes an unconditional status write a cross-replica
	// write storm: each Update bumps the resource version, every other
	// replica's watch fires, and they all write again. Every exit path below
	// goes through updateStatusIfChanged, which writes only a real diff.
	observedStatus := *entry.Status.DeepCopy()

	// Validate the action map before any endpoint is admitted into the
	// registry. A malformed declaration must fail closed: keeping a previous
	// registry record would allow an action whose contract no longer matches
	// the CatalogEntry observed by the controller.
	if err := providersv1alpha1.ValidateProviderActions(entry.Spec.Actions); err != nil {
		r.reg.DeleteOwned(entry.Name, string(req.ClusterName))
		now := metav1.NewTime(time.Now())
		entry.Status.Endpoints = &providersv1alpha1.ProviderEndpoints{}
		setCondition(&entry.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "InvalidActions",
			Message:            err.Error(),
			LastTransitionTime: now,
			ObservedGeneration: entry.Generation,
		})
		if requeue, statusErr := updateStatusIfChanged(ctx, c, &entry, observedStatus); statusErr != nil {
			return ctrl.Result{}, fmt.Errorf("updating invalid-action status: %w", statusErr)
		} else if requeue {
			return ctrl.Result{Requeue: true}, nil
		}
		logger.Info("Rejected invalid provider action declarations", "error", err.Error())
		return ctrl.Result{}, nil
	}

	dependencies := make([]Dependency, 0, len(entry.Spec.Dependencies))
	for _, dep := range entry.Spec.Dependencies {
		dependencies = append(dependencies, Dependency{Name: dep.Name})
	}

	prov := Provider{
		Name:         entry.Name,
		DisplayName:  entry.Spec.DisplayName,
		Description:  entry.Spec.Description,
		IconURL:      entry.Spec.IconURL,
		Category:     entry.Spec.Category,
		Dependencies: dependencies,
		Version:      entry.Spec.Version,
		// The cluster this entry was observed in is where a heartbeat must be
		// written back. Providers register their CatalogEntry in their own
		// workspace, so there is no single path the recorder could assume.
		CatalogEntryCluster: string(req.ClusterName),
	}
	prov.EdgeProxyAccess = entry.Spec.EdgeProxyAccess
	prov.AllowUntrustedClaims = entry.Annotations[AcceptUntrustedClaimsAnnotation] == "true"
	// Liveness travels through status so it reaches every hub replica, not just
	// the one whose heartbeat endpoint the provider happened to hit.
	if entry.Status.LastHeartbeat != nil {
		prov.LastHeartbeat = entry.Status.LastHeartbeat.Time
		prov.HeartbeatRequired = true
		prov.HeartbeatStale = time.Since(prov.LastHeartbeat) > HeartbeatTTL
		prov.ReportedVersion = entry.Status.ReportedVersion
	}
	if entry.Spec.APIExport != nil {
		prov.APIExportName = entry.Spec.APIExport.Name
		prov.APIExportPath = providersParentWorkspace + ":" + entry.Name
		for _, resource := range entry.Spec.APIExport.RequiredResources {
			prov.RequiredResources = append(prov.RequiredResources, APIExportResource{
				Group: resource.Group,
				Name:  resource.Name,
			})
		}
		for _, c := range entry.Spec.APIExport.PermissionClaims {
			claim := PermissionClaim{
				Group:        c.Group,
				Resource:     c.Resource,
				Verbs:        append([]string(nil), c.Verbs...),
				TenantScoped: c.TenantScoped,
			}
			if c.IdentitySource != nil {
				claim.IdentitySourceKind = c.IdentitySource.Kind
				claim.IdentitySourceProvider = c.IdentitySource.Provider
			}
			prov.PermissionClaims = append(prov.PermissionClaims, claim)
		}
	}

	// Builtin (first-party) providers declare spec.ui.builtinRoute instead
	// of a URL. The portal renders the named Vue route in-tree, so there's
	// no proxy target and no /main.js bundle to load — UIURL stays nil.
	if entry.Spec.UI != nil {
		prov.BuiltinRoute = entry.Spec.UI.BuiltinRoute
		for _, c := range entry.Spec.UI.Children {
			prov.Children = append(prov.Children, NavChild{
				DisplayName:  c.DisplayName,
				BuiltinRoute: c.BuiltinRoute,
			})
		}
	}
	parsedActions, actionSchemaErr := ParseProviderActions(entry.Spec.Actions)
	if actionSchemaErr != nil {
		r.reg.DeleteOwned(entry.Name, string(req.ClusterName))
		now := metav1.NewTime(time.Now())
		entry.Status.Endpoints = &providersv1alpha1.ProviderEndpoints{}
		setCondition(&entry.Status.Conditions, metav1.Condition{
			Type:               "Ready",
			Status:             metav1.ConditionFalse,
			Reason:             "InvalidActionSchemas",
			Message:            actionSchemaErr.Error(),
			LastTransitionTime: now,
			ObservedGeneration: entry.Generation,
		})
		if requeue, statusErr := updateStatusIfChanged(ctx, c, &entry, observedStatus); statusErr != nil {
			return ctrl.Result{}, fmt.Errorf("updating invalid-action-schema status: %w", statusErr)
		} else if requeue {
			return ctrl.Result{Requeue: true}, nil
		}
		logger.Info("Rejected provider action schemas", "error", actionSchemaErr.Error())
		return ctrl.Result{}, nil
	}
	prov.Actions = parsedActions
	seenSkillPackages := make(map[string]struct{}, len(entry.Spec.AssistantSkills))
	var assistantSkillBytes int64
	for _, skill := range entry.Spec.AssistantSkills {
		if skillErr := providersv1alpha1.ValidateProviderAssistantSkill(skill); skillErr != nil {
			// Skill packages are independent of provider routing and action
			// declarations. Omit only the malformed package so valid sibling
			// skills and actions remain available; App Studio receives no
			// partially validated artifact.
			logger.Info("Omitting invalid provider assistant skill", "packageName", skill.PackageName, "error", skillErr.Error())
			continue
		}
		if _, duplicate := seenSkillPackages[skill.PackageName]; duplicate {
			logger.Info("Omitting duplicate provider assistant skill", "packageName", skill.PackageName)
			continue
		}
		packageBytes := int64(len([]byte(skill.Skill)))
		for _, resource := range skill.Resources {
			packageBytes += int64(len([]byte(resource.Content)))
		}
		if assistantSkillBytes+packageBytes > providersv1alpha1.ProviderAssistantSkillsMaxAggregateBytes {
			logger.Info("Omitting provider assistant skill after aggregate bound", "packageName", skill.PackageName, "maxBytes", providersv1alpha1.ProviderAssistantSkillsMaxAggregateBytes)
			continue
		}
		seenSkillPackages[skill.PackageName] = struct{}{}
		assistantSkillBytes += packageBytes
		resources := make([]ProviderAssistantSkillResource, 0, len(skill.Resources))
		for _, resource := range skill.Resources {
			resources = append(resources, ProviderAssistantSkillResource{Path: resource.Path, Content: resource.Content})
		}
		prov.AssistantSkills = append(prov.AssistantSkills, ProviderAssistantSkill{
			PackageName: skill.PackageName,
			Version:     skill.Version,
			Digest:      skill.Digest,
			Skill:       skill.Skill,
			Resources:   resources,
		})
	}
	sort.Slice(prov.AssistantSkills, func(i, j int) bool {
		if prov.AssistantSkills[i].PackageName != prov.AssistantSkills[j].PackageName {
			return prov.AssistantSkills[i].PackageName < prov.AssistantSkills[j].PackageName
		}
		return prov.AssistantSkills[i].Version < prov.AssistantSkills[j].Version
	})

	var parseErrs []string
	var backendHealthErr error
	var apiExportErr error
	if entry.Spec.UI != nil && entry.Spec.UI.URL != "" {
		u, err := ParseURL(entry.Spec.UI.URL)
		if err != nil {
			parseErrs = append(parseErrs, "ui.url: "+err.Error())
		} else {
			prov.UIURL = u
		}
	}
	if entry.Spec.Backend != nil {
		prov.BackendHealthRequired = true
		u, err := ParseURL(entry.Spec.Backend.URL)
		if err != nil {
			parseErrs = append(parseErrs, "backend.url: "+err.Error())
		} else {
			prov.BackendURL = u
			healthClient := r.healthClient
			if healthClient == nil {
				healthClient = defaultBackendHealthClient()
			}
			if err := probeBackendHealth(ctx, healthClient, u, entry.Spec.Backend.HealthPath); err != nil {
				backendHealthErr = err
			} else {
				prov.BackendHealthy = true
			}
		}
	}
	if entry.Spec.VirtualWorkspace != nil {
		u, err := ParseURL(entry.Spec.VirtualWorkspace.URL)
		if err != nil {
			parseErrs = append(parseErrs, "virtualWorkspace.url: "+err.Error())
		} else {
			prov.VirtualWorkspaceURL = u
		}
	}

	// If this CatalogEntry name matches a first-party provider that
	// registered LocalUIAssets via BuiltinSpec, plumb the embedded FS into
	// the registry record so the UI proxy serves /ui/providers/{name}/*
	// from the hub binary instead of forwarding to an external URL.
	if spec, ok := BuiltinByName(entry.Name); ok && spec.LocalUIAssets != nil && prov.UIURL == nil && prov.BuiltinRoute == "" {
		prov.LocalUIAssets = spec.LocalUIAssets
	}

	// RuntimeDeclared preserves the distinction between an APIExport-only
	// provider and a provider whose declared endpoint was invalid. EndpointsValid
	// covers spec parse health and "the provider has
	// somewhere to render": a URL endpoint OR a builtin Vue route OR a
	// backend proxy target OR embedded UI assets. Heartbeat-driven
	// readiness is layered on by the sweeper (see Provider.Ready()).
	prov.RuntimeDeclared = (entry.Spec.UI != nil && (entry.Spec.UI.URL != "" || entry.Spec.UI.BuiltinRoute != "")) ||
		entry.Spec.Backend != nil || entry.Spec.VirtualWorkspace != nil || prov.LocalUIAssets != nil
	prov.EndpointsValid = len(parseErrs) == 0 &&
		(prov.UIURL != nil || prov.BackendURL != nil || prov.VirtualWorkspaceURL != nil || prov.BuiltinRoute != "" || prov.LocalUIAssets != nil)
	if entry.Spec.APIExport != nil {
		if r.exportChecker == nil {
			apiExportErr = fmt.Errorf("APIExport verification is unavailable")
		} else if err := r.exportChecker.CheckAPIExport(ctx, prov.APIExportPath, prov.APIExportName, prov.RequiredResources, prov.PermissionClaims); err != nil {
			apiExportErr = err
		} else {
			prov.APIExportReady = true
		}
	}

	r.reg.Upsert(prov)
	// Render URLs as strings: a nil *url.URL panics klog's stringer (shows as
	// "<panic: ...>" in logs), which is the common case for builtins (localUI).
	logger.Info("Upserted provider", "endpointsValid", prov.EndpointsValid, "ui", urlString(prov.UIURL), "backend", urlString(prov.BackendURL), "localUI", prov.LocalUIAssets != nil)

	// The hub no longer provisions the per-provider workspace, schemas,
	// APIExport, SA, or kubeconfig — that moved to admin onboarding
	// (pkg/hub/admin) plus the provider's own Helm `init` (faros-provider-sdk).
	// The other read-only provider-workspace operation above verifies the
	// APIExport. Here we resolve the logical cluster ID so the Enable endpoint
	// can build the edges-proxy RBAC subject. An empty result means the provider
	// has not finished onboarding yet.
	if r.prov != nil && entry.Spec.APIExport != nil {
		if cluster, err := r.prov.ResolveWorkspaceCluster(ctx, entry.Name); err != nil {
			logger.Info("WARNING could not resolve provider workspace cluster", "err", err.Error())
		} else if cluster != "" {
			entry.Status.Workspace = providersParentWorkspace + ":" + entry.Name
			r.reg.SetWorkspaceCluster(entry.Name, cluster)
		}
	}

	// Update status.
	now := metav1.NewTime(time.Now())
	entry.Status.Endpoints = &providersv1alpha1.ProviderEndpoints{}
	if prov.UIURL != nil {
		entry.Status.Endpoints.UI = prov.UIURL.String()
	}
	if prov.BackendURL != nil {
		entry.Status.Endpoints.Backend = prov.BackendURL.String()
	}

	if entry.Spec.Backend == nil {
		removeCondition(&entry.Status.Conditions, "BackendHealthy")
	} else {
		backendCondition := metav1.Condition{
			Type:               "BackendHealthy",
			LastTransitionTime: now,
			ObservedGeneration: entry.Generation,
		}
		if prov.BackendHealthy {
			backendCondition.Status = metav1.ConditionTrue
			backendCondition.Reason = "HealthCheckSucceeded"
			backendCondition.Message = "Provider backend health check succeeded."
		} else {
			backendCondition.Status = metav1.ConditionFalse
			if prov.BackendURL == nil {
				backendCondition.Reason = "InvalidEndpoint"
				backendCondition.Message = "Provider backend URL is invalid."
			} else {
				backendCondition.Reason = "HealthCheckFailed"
				backendCondition.Message = "Provider backend health check failed: " + backendHealthErr.Error()
			}
		}
		setCondition(&entry.Status.Conditions, backendCondition)
	}
	if entry.Spec.APIExport == nil {
		removeCondition(&entry.Status.Conditions, "APIExportReady")
	} else {
		exportCondition := metav1.Condition{
			Type:               "APIExportReady",
			LastTransitionTime: now,
			ObservedGeneration: entry.Generation,
		}
		if prov.APIExportReady {
			exportCondition.Status = metav1.ConditionTrue
			exportCondition.Reason = "APIExportAvailable"
			exportCondition.Message = "The declared APIExport identity, resources, schemas, and permission claims are ready."
		} else {
			exportCondition.Status = metav1.ConditionFalse
			exportCondition.Reason = "APIExportUnavailable"
			exportCondition.Message = "The declared APIExport is not ready: " + apiExportErr.Error()
		}
		setCondition(&entry.Status.Conditions, exportCondition)
	}

	cond := metav1.Condition{
		Type:               "Ready",
		LastTransitionTime: now,
		ObservedGeneration: entry.Generation,
	}
	switch {
	case len(parseErrs) > 0:
		cond.Status = metav1.ConditionFalse
		cond.Reason = "InvalidEndpoint"
		cond.Message = fmt.Sprintf("Endpoint errors: %v", parseErrs)
	case entry.Spec.APIExport != nil && !prov.APIExportReady:
		cond.Status = metav1.ConditionFalse
		cond.Reason = "APIExportUnavailable"
		cond.Message = "Provider APIExport is not ready: " + apiExportErr.Error()
	case prov.BackendHealthRequired && !prov.BackendHealthy:
		cond.Status = metav1.ConditionFalse
		cond.Reason = "BackendUnhealthy"
		cond.Message = "Provider backend is not healthy: " + backendHealthErr.Error()
	case prov.HeartbeatRequired && prov.HeartbeatStale:
		cond.Status = metav1.ConditionFalse
		cond.Reason = "HeartbeatStale"
		cond.Message = "Provider heartbeat is stale."
	case prov.Ready():
		cond.Status = metav1.ConditionTrue
		cond.Reason = "Ready"
		if prov.EndpointsValid {
			cond.Message = "Provider endpoints and health checks are ready."
		} else {
			cond.Message = "Provider APIExport is available."
		}
	default:
		cond.Status = metav1.ConditionFalse
		cond.Reason = "NoEndpoint"
		cond.Message = "CatalogEntry declares no UI or Backend endpoint."
	}
	setCondition(&entry.Status.Conditions, cond)

	if requeue, err := updateStatusIfChanged(ctx, c, &entry, observedStatus); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating status: %w", err)
	} else if requeue {
		return ctrl.Result{Requeue: true}, nil
	}
	if entry.Spec.APIExport != nil || entry.Spec.Backend != nil || prov.HeartbeatRequired {
		// Backend health is runtime state, not spec state. Reconcile it even when
		// the provider does not heartbeat. Heartbeat freshness is also runtime
		// state: periodically reconciling every provider that has ever heartbeated
		// publishes TTL expiry into CatalogEntry.status instead of changing only
		// this replica's in-memory routing verdict.
		return ctrl.Result{RequeueAfter: SweepInterval}, nil
	}
	return ctrl.Result{}, nil
}

// probeBackendHealth constructs a same-authority endpoint from the parsed
// backend URL and the CatalogEntry healthPath, then requires HTTP 200 within a
// bounded interval. Absolute/network-path health URLs and traversal segments
// are rejected so healthPath cannot override the admitted backend authority.
func probeBackendHealth(ctx context.Context, client httpDoer, backend *url.URL, healthPath string) error {
	if backend == nil {
		return fmt.Errorf("backend URL is missing")
	}
	if backend.Scheme != "http" && backend.Scheme != "https" {
		return fmt.Errorf("backend URL must use http or https")
	}
	if healthPath == "" {
		healthPath = "/healthz"
	}
	rel, err := url.Parse(healthPath)
	if err != nil {
		return fmt.Errorf("invalid healthPath: %w", err)
	}
	if rel.IsAbs() || rel.Host != "" || rel.User != nil || rel.Opaque != "" || rel.Fragment != "" {
		return fmt.Errorf("healthPath must be a local HTTP path")
	}
	decodedPath, err := url.PathUnescape(rel.EscapedPath())
	if err != nil {
		return fmt.Errorf("invalid healthPath escaping: %w", err)
	}
	for _, segment := range strings.Split(decodedPath, "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("healthPath must not contain traversal segments")
		}
	}
	if decodedPath == "" {
		return fmt.Errorf("healthPath must not be empty")
	}
	if !strings.HasPrefix(decodedPath, "/") {
		decodedPath = "/" + decodedPath
	}
	target := *backend
	target.Path = singleJoiningSlash(backend.Path, decodedPath)
	target.RawPath = ""
	target.RawQuery = rel.RawQuery
	target.Fragment = ""

	probeCtx, cancel := context.WithTimeout(ctx, backendHealthTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, target.String(), nil)
	if err != nil {
		return fmt.Errorf("build health request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// updateStatusIfChanged writes entry's status only when it differs from what
// was observed, and reports whether the caller should requeue after a conflict.
// Skipping no-op writes is what keeps N hub replicas reconciling the same
// CatalogEntry from bumping its resource version in a loop.
func updateStatusIfChanged(
	ctx context.Context,
	c client.Client,
	entry *providersv1alpha1.CatalogEntry,
	observed providersv1alpha1.CatalogEntryStatus,
) (requeue bool, err error) {
	if equality.Semantic.DeepEqual(observed, entry.Status) {
		return false, nil
	}
	if err := c.Status().Update(ctx, entry); err != nil {
		if apierrors.IsConflict(err) {
			return true, nil
		}
		return false, err
	}
	return false, nil
}

// urlString renders a *url.URL for logging, returning "" for nil (a nil
// *url.URL panics klog's stringer).
func urlString(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.String()
}

// setCondition is a small upsert helper for metav1.Condition slices.
func setCondition(conds *[]metav1.Condition, c metav1.Condition) {
	for i, existing := range *conds {
		if existing.Type == c.Type {
			// LastTransitionTime describes a status transition, not a reconcile,
			// generation observation, or reason/message refresh. Preserve it while
			// Status is stable, while still publishing ObservedGeneration.
			if existing.Status == c.Status {
				c.LastTransitionTime = existing.LastTransitionTime
			}
			if equality.Semantic.DeepEqual(existing, c) {
				return
			}
			(*conds)[i] = c
			return
		}
	}
	*conds = append(*conds, c)
}

func removeCondition(conds *[]metav1.Condition, conditionType string) {
	for i, condition := range *conds {
		if condition.Type == conditionType {
			*conds = append((*conds)[:i], (*conds)[i+1:]...)
			return
		}
	}
}
