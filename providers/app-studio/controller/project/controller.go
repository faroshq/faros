/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package project reconciles Project CRs: for every live-mode provider
// binding the spec declares, it ensures the referenced infrastructure
// instance exists and matches the binding's values (create-if-missing,
// converge-on-drift, delete-on-project-delete via finalizer), and mirrors the
// instances' observed state into Project.status.environments. Handlers write
// spec; this loop owns convergence.
//
// The binding contract is self-contained: resourceRef records the full
// group/version/resource/kind (from Template.spec.instanceCRD at bind time),
// so this reconciler never reads Templates (they ride virtual storage with a
// separate identity).
//
// Removed provider resources are swept from the controller-maintained binding
// inventory, which retains their dynamic GVR and lifecycle policy after spec
// deletion. Derived Infrastructure Connections have a fixed GVK and are swept
// by Project identity labels.
package project

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	apisv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha2"
	"github.com/kcp-dev/sdk/apis/core"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	"github.com/faroshq/provider-sdk/tenantaccess"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	"github.com/faroshq/provider-app-studio/bindings"
	"github.com/faroshq/provider-app-studio/store"
	"github.com/faroshq/provider-app-studio/workspace"
)

const (
	// finalizer guards instance teardown on Project deletion.
	finalizer = "ai.faros.sh/instances"
	// requeueInterval polls instance status while not Ready. Instances are
	// not watched (their kinds are per-template and dynamic); polling keeps
	// the controller simple and deterministic.
	requeueInterval = 15 * time.Second
	// instanceConvergenceMaxAttempts bounds optimistic-concurrency recovery.
	// A fresh GET/recompute is enough to absorb the provider's usual computed
	// field update; persistent contention is surfaced to the normal reconcile
	// poll instead of spinning in one request.
	instanceConvergenceMaxAttempts = 2

	projectDevelopmentEnvironmentName = "development"
	projectDevelopmentBindingName     = "dev"
	projectDevelopmentProvider        = "app-studio"
	appStudioAPIExportName            = "ai.faros.sh"
	appStudioAPIExportPath            = "root:faros:providers:app-studio"
)

type tenantPathResolver func(context.Context, client.Client, string) (string, error)

// Reconciler lifecycles infrastructure instances, the git backing
// repository, and workspace→git commit convergence for Projects.
type Reconciler struct {
	Manager mcmanager.Manager
	// Actions is operator-owned transport configuration. Tenant and project
	// identity are derived per reconcile from the selected logical cluster and
	// Project object, never from this configuration or Project annotations.
	Actions bindings.ActionsRuntimeConfig
	// ResolveTenantPath is a test seam for the authoritative LogicalCluster
	// lookup. Production leaves it nil and uses resolveLogicalClusterPath.
	ResolveTenantPath tenantPathResolver
	// Workspace is the shared on-disk project file store (nil disables
	// commit convergence).
	Workspace *workspace.FileStore
	// Attachments owns the durable project attachment scope. The controller adds
	// its finalizer only after the Project's tenant scope and UID are available;
	// API-created Projects carry the finalizer from creation time so an immediate
	// direct KCP delete still has a cleanup owner.
	Attachments store.AttachmentStore
	// Busy reports whether an assistant turn currently owns the project's
	// workspace — commits wait for idle.
	Busy func(workspace.Scope) bool
	// Owns reports whether THIS replica owns the project's workspace (the
	// durable project claim). Every replica runs this reconciler; only the
	// owner's local tree is authoritative, and a stale tree left behind on a
	// previous owner must never be committed over the live one. Nil means
	// single-replica: always owner.
	Owns func(workspace.Scope) bool
	// HubBase / HubInsecure address the hub for MCP commit calls and for the
	// tenant-path client below.
	HubBase     string
	HubInsecure bool
	// TenantClientFor is a test seam for the tenant-path client: a client on
	// the workspace cluster authenticated as the project ServiceAccount.
	// Production leaves it nil and dials {HubBase}/clusters/{cluster} with the
	// identity token. Instance and repository writes go through THIS client —
	// the workspace's own bindings — rather than the claimed VW, so they reach
	// whichever copy of infrastructure/code the workspace binds (platform or
	// self-hosted) with no permission-claim identity pinning (see package
	// tenantaccess).
	TenantClientFor func(clusterName, token string) (client.Client, error)
}

// tenantClient resolves the client used for instance/repository writes. vw is
// the claimed-VW fallback for deployments with no hub address configured
// (REST-only dev); that path still depends on first-party permission claims
// and cannot serve mixed platform/self-hosted workspaces.
func (r *Reconciler) tenantClient(clusterName, token string, vw client.Client) (client.Client, error) {
	if r.TenantClientFor != nil {
		return r.TenantClientFor(clusterName, token)
	}
	if r.HubBase == "" {
		log.Printf("WARNING app-studio project reconciler: FAROS_HUB_URL is empty; falling back to the claimed-VW client, which cannot serve workspaces whose infrastructure/code provider identity differs from this deployment's pins")
		return vw, nil
	}
	return tenantaccess.NewClient(r.HubBase, clusterName, token, r.HubInsecure)
}

func (r *Reconciler) SetupWithManager(mgr mcmanager.Manager) error {
	r.Manager = mgr
	return mcbuilder.ControllerManagedBy(mgr).
		Named("app-studio-project").
		For(&aiv1alpha1.Project{}).
		Complete(r)
}

func (r *Reconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (ctrl.Result, error) {
	cl, err := r.Manager.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("cluster %q: %w", req.ClusterName, err)
	}
	c := cl.GetClient()

	var p aiv1alpha1.Project
	if err := c.Get(ctx, req.NamespacedName, &p); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !p.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, c, &p, string(req.ClusterName))
	}
	previewPolicyChanged, err := reconcileDevelopmentPreviewPolicy(&p)
	if err != nil {
		return ctrl.Result{}, err
	}
	if previewPolicyChanged {
		if err := c.Update(ctx, &p); err != nil {
			return ctrl.Result{}, fmt.Errorf("converging development preview access policy: %w", err)
		}
		return ctrl.Result{Requeue: true}, nil
	}
	if r.Attachments != nil && !controllerutil.ContainsFinalizer(&p, store.AttachmentStorageFinalizer) {
		// A Project created directly through KCP may not carry the API layer's
		// org/workspace annotations. Do not add a finalizer that this controller
		// cannot later use to identify the attachment storage scope.
		if _, ok := attachmentScopeForProject(&p); ok {
			controllerutil.AddFinalizer(&p, store.AttachmentStorageFinalizer)
			if err := c.Update(ctx, &p); err != nil {
				return ctrl.Result{}, fmt.Errorf("adding attachment storage finalizer: %w", err)
			}
			return ctrl.Result{Requeue: true}, nil
		}
	}

	bound := providerBindings(&p)
	hasRepository := p.Spec.Repository != nil && p.Spec.Repository.RepositoryRef != ""
	hasInventory := len(p.Status.BindingInventory) > 0
	hasConnections := projectHasEnvironmentConnections(&p)
	if len(bound) == 0 && !hasRepository && !hasInventory && !hasConnections {
		// Nothing to lifecycle yet.
		return ctrl.Result{}, nil
	}
	clusterName := string(req.ClusterName)
	actionsTenantPath, err := r.actionsTenantPath(ctx, c, &p, bound, clusterName)
	if err != nil {
		// Resolve the authoritative tenant before adding a finalizer or mutating
		// any instance. A missing or inconsistent LogicalCluster path therefore
		// fails closed with no partial reconciliation side effects.
		return ctrl.Result{}, err
	}

	if !controllerutil.ContainsFinalizer(&p, finalizer) {
		controllerutil.AddFinalizer(&p, finalizer)
		if err := c.Update(ctx, &p); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Everything below that touches instances or repositories acts as the
	// project's ServiceAccount against the tenant workspace itself. The
	// identity objects are provisioned over the claimed VW (built-in types),
	// then the writes ride the workspace's own bindings.
	token, err := r.ensureIdentity(ctx, c, &p)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("project identity: %w", err)
	}
	if token == "" {
		// Token controller not done; nothing else can proceed safely.
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}
	tc, err := r.tenantClient(clusterName, token, c)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("tenant client: %w", err)
	}
	if err := cleanupRemovedBindings(ctx, tc, &p, bound); err != nil {
		return ctrl.Result{}, fmt.Errorf("cleaning removed provider bindings: %w", err)
	}

	// Converge each bound instance, folding observed state per environment.
	allReady := true
	instancesNeedRetry := false
	resolvedInstances := make(map[string]map[string]*unstructured.Unstructured, len(bound))
	liveStatuses := make([]aiv1alpha1.ProjectEnvironmentStatus, 0, len(bound))
	for _, env := range bound {
		resolvedInstances[env.spec.Name] = map[string]*unstructured.Unstructured{}
		bindingStatuses := make([]aiv1alpha1.ProjectProviderBindingStatus, 0, len(env.bindings))
		for _, binding := range env.bindings {
			effectiveBinding := binding
			if isProjectDevelopmentBinding(env.spec.Name, binding) {
				if actionsTenantPath != "" {
					effectiveBinding, err = r.overlayDevelopmentBinding(&p, binding, actionsTenantPath)
				}
				// Preview visibility is Project policy, not binding data, so it
				// is overlaid here on every pass. Applied outside the actions
				// branch above deliberately: a deployment with no Provider
				// Actions configured must still get a private preview.
				if err == nil {
					effectiveBinding, err = bindings.ApplyPreviewAccessToBinding(effectiveBinding, bindings.PreviewAccess(&p))
				}
				if err != nil {
					allReady = false
					st := bindings.InvalidStatus(binding)
					st.Outputs = map[string]string{"error": err.Error()}
					bindingStatuses = append(bindingStatuses, st)
					continue
				}
			}
			obj, err := r.ensureInstance(ctx, tc, &p, effectiveBinding)
			switch {
			case apierrors.IsInvalid(err) || bindings.IsInvalidBinding(err):
				// The API server rejects the spec, or the binding cannot even
				// produce a desired object: retrying cannot help, only a spec
				// change can. Record it where the user sees it and stop
				// hammering.
				st := bindings.InvalidStatus(binding)
				st.Outputs = map[string]string{"error": err.Error()}
				bindingStatuses = append(bindingStatuses, st)
				continue
			case err != nil:
				// Transient — most often "the object has been modified"
				// (an optimistic-concurrency conflict when the infra provider
				// updates the instance while we converge it). Do NOT abort the
				// whole reconcile here: returning early also skips repository
				// and commit convergence below, which is exactly why workspace
				// changes stopped reaching git. Mark the binding pending,
				// remember to retry soon, and keep going.
				log.Printf("app-studio project %s: instance for binding %q not converged (will retry): %v", p.Name, binding.Name, err)
				instancesNeedRetry = true
				allReady = false
				bindingStatuses = append(bindingStatuses, bindings.StatusFromObject(binding, nil))
				continue
			}
			st := bindings.StatusFromObject(binding, obj)
			resolvedInstances[env.spec.Name][binding.Name] = obj
			if st.Phase != "Ready" {
				allReady = false
			}
			bindingStatuses = append(bindingStatuses, st)
		}
		liveStatuses = append(liveStatuses, bindings.FoldEnvironment(env.spec, bindingStatuses))
	}

	connectionStatuses, connectionsNeedRetry, err := reconcileProjectConnections(ctx, tc, &p, resolvedInstances)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling project connections: %w", err)
	}
	liveStatuses = mergeProjectConnectionStatuses(liveStatuses, p.Spec.Environments, p.Status.Environments, connectionStatuses)
	if connectionsNeedRetry {
		allReady = false
	}

	// Mirror, touching only the environments the reconciler owns (other
	// status fields — Phase, UpdatedAt, artifact-env entries — belong to the
	// API layer).
	next := bindings.MergeEnvironmentStatuses(p.Status.Environments, liveStatuses)
	nextInventory := bindingInventory(&p, bound)
	if !environmentStatusesEqual(p.Status.Environments, next) || !bindingInventoryEqual(p.Status.BindingInventory, nextInventory) {
		p.Status.Environments = next
		p.Status.BindingInventory = nextInventory
		if err := c.Status().Update(ctx, &p); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Converge the git backing repository from the spec binding (autoInit
	// creates the repo on the git host), then keep git in step with the
	// workspace. A commit failure is retried on the poll, not escalated —
	// instances must keep converging regardless.
	repo, err := r.ensureRepository(ctx, tc, &p)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("repository: %w", err)
	}
	dirty, err := r.commitWorkspace(ctx, token, &p, repo)
	if err != nil {
		log.Printf("app-studio project %s: commit convergence: %v", p.Name, err)
		dirty = true
	}
	repositoryPending := p.Spec.Repository != nil && p.Spec.Repository.RepositoryRef != "" && !repositoryReady(repo)

	if !allReady || dirty || repositoryPending || instancesNeedRetry {
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}
	// Ready: keep a slow poll so drift (instance deleted out-of-band, status
	// regressions, new dirty files) is noticed without watching dynamic kinds.
	return ctrl.Result{RequeueAfter: 4 * requeueInterval}, nil
}

func isProjectDevelopmentBinding(environment string, binding aiv1alpha1.ProjectProviderBindingSpec) bool {
	return strings.TrimSpace(environment) == projectDevelopmentEnvironmentName &&
		strings.TrimSpace(binding.Name) == projectDevelopmentBindingName &&
		strings.TrimSpace(binding.Provider) == projectDevelopmentProvider
}

// reconcileDevelopmentPreviewPolicy makes Project sharing intent and the
// access-proxy Template input one desired-state contract. New bindings carry
// access from creation; for legacy bindings, an observed URL proves that the
// selected Template is access-capable without teaching this controller how to
// read the Infrastructure provider's Template API identity.
func reconcileDevelopmentPreviewPolicy(p *aiv1alpha1.Project) (bool, error) {
	if p == nil {
		return false, nil
	}
	changed := false
	normalized := bindings.NormalizePreviewSharingMode(p.Spec.Sharing.Preview.Mode)
	if normalized != p.Spec.Sharing.Preview.Mode {
		p.Spec.Sharing.Preview.Mode = normalized
		changed = true
	}
	desiredAccess := bindings.PreviewAccessForMode(normalized)
	for envIndex := range p.Spec.Environments {
		env := &p.Spec.Environments[envIndex]
		for bindingIndex := range env.Bindings {
			binding := &env.Bindings[bindingIndex]
			if !isProjectDevelopmentBinding(env.Name, *binding) {
				continue
			}
			values, err := bindings.Values(*binding)
			if err != nil {
				return false, err
			}
			_, declaresAccess := values[bindings.PreviewAccessField]
			if !declaresAccess && !developmentBindingHasObservedURL(p, env.Name, binding.Name) {
				continue
			}
			if current, _ := values[bindings.PreviewAccessField].(string); current == desiredAccess {
				continue
			}
			values[bindings.PreviewAccessField] = desiredAccess
			raw, err := json.Marshal(values)
			if err != nil {
				return false, fmt.Errorf("marshal development binding %q: %w", binding.Name, err)
			}
			binding.Values.Raw = raw
			changed = true
		}
	}
	return changed, nil
}

func developmentBindingHasObservedURL(p *aiv1alpha1.Project, environment, binding string) bool {
	for _, env := range p.Status.Environments {
		if strings.TrimSpace(env.Name) != strings.TrimSpace(environment) {
			continue
		}
		for _, status := range env.Bindings {
			if strings.TrimSpace(status.Name) != strings.TrimSpace(binding) {
				continue
			}
			if strings.TrimSpace(status.URL) != "" || strings.TrimSpace(status.PreviewURL) != "" {
				return true
			}
			if strings.TrimSpace(status.Outputs["url"]) != "" || strings.TrimSpace(status.Outputs["previewURL"]) != "" {
				return true
			}
		}
	}
	return false
}

func hasProjectDevelopmentBinding(bound []boundEnv) bool {
	for _, env := range bound {
		for _, binding := range env.bindings {
			if isProjectDevelopmentBinding(env.spec.Name, binding) {
				return true
			}
		}
	}
	return false
}

// actionsTenantPath resolves the tenant path before any Project or instance
// mutation. Project org/workspace annotations are checked only as a
// consistency guard; they never supply the controller's authority.
func (r *Reconciler) actionsTenantPath(ctx context.Context, c client.Client, p *aiv1alpha1.Project, bound []boundEnv, clusterName string) (string, error) {
	if !hasProjectDevelopmentBinding(bound) {
		return "", nil
	}
	resolver := r.ResolveTenantPath
	if resolver == nil {
		resolver = resolveLogicalClusterPath
	}
	path, err := resolver(ctx, c, clusterName)
	if err != nil {
		return "", fmt.Errorf("resolve Project Actions tenant for cluster %q: %w", clusterName, err)
	}
	org, workspace, err := bindings.ParseTenantWorkspacePath(path)
	if err != nil {
		return "", fmt.Errorf("resolve Project Actions tenant for cluster %q: %w", clusterName, err)
	}
	annotations := p.GetAnnotations()
	if annotated := strings.TrimSpace(annotations[bindings.OrgUUIDAnnotation]); annotated != "" && annotated != org {
		return "", fmt.Errorf("Project %q organization annotation does not match authoritative tenant path", p.Name)
	}
	if annotated := strings.TrimSpace(annotations[bindings.WorkspaceUUIDAnnotation]); annotated != "" && annotated != workspace {
		return "", fmt.Errorf("Project %q workspace annotation does not match authoritative tenant path", p.Name)
	}
	return strings.TrimSpace(path), nil
}

func resolveLogicalClusterPath(ctx context.Context, c client.Client, clusterName string) (string, error) {
	clusterName = strings.TrimSpace(clusterName)
	if c == nil {
		return "", fmt.Errorf("logical-cluster client is nil")
	}
	if clusterName == "" {
		return "", fmt.Errorf("multicluster cluster name is required")
	}
	bindingsList := &apisv1alpha2.APIBindingList{}
	if err := c.List(ctx, bindingsList); err != nil {
		return "", fmt.Errorf("list APIBindings: %w", err)
	}

	matches := make([]*apisv1alpha2.APIBinding, 0, len(bindingsList.Items))
	preferred := make([]*apisv1alpha2.APIBinding, 0, len(bindingsList.Items))
	for i := range bindingsList.Items {
		binding := &bindingsList.Items[i]
		export := binding.Spec.Reference.Export
		if export == nil || strings.TrimSpace(export.Name) != appStudioAPIExportName {
			continue
		}
		matches = append(matches, binding)
		if strings.TrimSpace(export.Path) == appStudioAPIExportPath {
			preferred = append(preferred, binding)
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no APIBinding references App Studio APIExport %q", appStudioAPIExportName)
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("multiple APIBindings reference App Studio APIExport %q", appStudioAPIExportName)
	}
	if len(preferred) == 1 {
		matches = preferred
	}

	annotations := matches[0].GetAnnotations()
	if got := strings.TrimSpace(annotations["kcp.io/cluster"]); got == "" {
		return "", fmt.Errorf("App Studio APIBinding has no kcp.io/cluster annotation")
	} else if got != clusterName {
		return "", fmt.Errorf("App Studio APIBinding cluster %q does not match request cluster %q", got, clusterName)
	}
	path := strings.TrimSpace(annotations[core.LogicalClusterPathAnnotationKey])
	if path == "" {
		return "", fmt.Errorf("App Studio APIBinding has no %s annotation", core.LogicalClusterPathAnnotationKey)
	}
	return path, nil
}

func (r *Reconciler) overlayDevelopmentBinding(p *aiv1alpha1.Project, binding aiv1alpha1.ProjectProviderBindingSpec, tenantPath string) (aiv1alpha1.ProjectProviderBindingSpec, error) {
	values, err := bindings.Values(binding)
	if err != nil {
		return binding, err
	}
	org, workspace, err := bindings.ParseTenantWorkspacePath(tenantPath)
	if err != nil {
		return binding, err
	}
	overlay, err := bindings.NewActionsOverlay(bindings.ActionsIdentity{
		TenantPath:  tenantPath,
		Org:         org,
		Workspace:   workspace,
		Project:     strings.TrimSpace(p.Name),
		ProjectUID:  string(p.UID),
		Environment: projectDevelopmentEnvironmentName,
		Instance:    bindings.ResourceName(p, binding, values),
	}, r.Actions, bindings.HasActiveProviderActionGrant(p))
	if err != nil {
		return binding, err
	}
	return bindings.ApplyActionsOverlayToBinding(binding, overlay)
}

// ensureInstance gets or creates the bound instance, converging spec, labels,
// and ownerRef on drift (promote rolls image refs by updating binding values —
// the update path is what makes that land).
func (r *Reconciler) ensureInstance(ctx context.Context, c client.Client, p *aiv1alpha1.Project, binding aiv1alpha1.ProjectProviderBindingSpec) (*unstructured.Unstructured, error) {
	want, _, err := bindings.Desired(p, binding)
	if err != nil {
		return nil, err
	}

	for attempt := 0; attempt < instanceConvergenceMaxAttempts; attempt++ {
		// Every retry starts with a fresh read and recomputes the merge. Provider
		// controllers commonly stamp spec fields (fqdn, credential references)
		// between our read and update; reusing the stale object would overwrite
		// those values on the retry.
		got := &unstructured.Unstructured{}
		got.SetGroupVersionKind(want.GroupVersionKind())
		err = c.Get(ctx, types.NamespacedName{Name: want.GetName()}, got)
		if apierrors.IsNotFound(err) {
			created := want.DeepCopy()
			if createErr := c.Create(ctx, created); createErr == nil {
				return created, nil
			} else if apierrors.IsAlreadyExists(createErr) && attempt+1 < instanceConvergenceMaxAttempts {
				continue
			} else {
				return nil, createErr
			}
		}
		if err != nil {
			return nil, err
		}

		next := got.DeepCopy()
		// The merge operates on the values level — spec.template is the
		// instance's immutable identity and spec.values is where the provider
		// stamps its computed fields (fqdn, credential references).
		observedValues, _, _ := unstructured.NestedMap(got.Object, "spec", "values")
		desiredValues, _, _ := unstructured.NestedMap(want.Object, "spec", "values")
		// A template that exposes no URL declares no access input; asking for
		// it anyway would make every reconcile see drift it can never resolve.
		bindings.DropUnsupportedAccess(observedValues, desiredValues)
		observedTemplate, _, _ := unstructured.NestedString(got.Object, "spec", "template")
		if observedTemplate == "" {
			observedTemplate, _, _ = unstructured.NestedString(want.Object, "spec", "template")
		}
		next.Object["spec"] = map[string]any{
			"template": observedTemplate,
			"values":   bindings.MergeProviderSpec(observedValues, desiredValues),
		}
		labels := next.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		labels[bindings.ProjectLabel] = p.Name
		next.SetLabels(labels)
		if owner := bindings.OwnerRef(p); owner != nil {
			next.SetOwnerReferences(want.GetOwnerReferences())
		}
		if equalSpecAndMeta(got, next) {
			return got, nil
		}
		if updateErr := c.Update(ctx, next); updateErr == nil {
			return next, nil
		} else if !apierrors.IsConflict(updateErr) || attempt+1 >= instanceConvergenceMaxAttempts {
			return nil, updateErr
		}
	}
	return nil, fmt.Errorf("instance convergence retry budget exhausted")
}

// finalize deletes bound instances according to their lifecycle policy, then
// releases the finalizer. Retained instances are detached from the Project
// owner reference before this Project disappears, so Kubernetes garbage
// collection cannot remove them after the fact. The infrastructure provider's
// template owns the runtime namespace and garbage-collects every materialized
// workload when a deleted instance goes away.
func (r *Reconciler) finalize(ctx context.Context, c client.Client, p *aiv1alpha1.Project, clusterName string) (ctrl.Result, error) {
	instanceFinalizer := controllerutil.ContainsFinalizer(p, finalizer)
	attachmentFinalizer := controllerutil.ContainsFinalizer(p, store.AttachmentStorageFinalizer)
	if !instanceFinalizer && !attachmentFinalizer {
		return ctrl.Result{}, nil
	}
	if attachmentFinalizer {
		if r.Attachments == nil {
			return ctrl.Result{}, fmt.Errorf("attachment storage finalizer present but attachment store is unavailable")
		}
		scope, ok := attachmentScopeForProject(p)
		if !ok {
			// This can only be a legacy object (or a manually-created object
			// carrying the old finalizer). There is no authenticated scope with
			// which to delete bytes, so retaining the finalizer would make the CR
			// undeletable forever. The API creation path now installs the
			// finalizer only alongside the scope annotations, while unattached
			// direct KCP Projects never receive it.
			log.Printf("app-studio project %s: releasing attachment finalizer without cleanup because tenant scope is unavailable", p.Name)
			controllerutil.RemoveFinalizer(p, store.AttachmentStorageFinalizer)
		} else {
			if err := r.Attachments.DeleteProjectAttachments(ctx, scope); err != nil {
				return ctrl.Result{}, fmt.Errorf("deleting project attachments: %w", err)
			}
			controllerutil.RemoveFinalizer(p, store.AttachmentStorageFinalizer)
		}
	}
	if instanceFinalizer {
		bound := providerBindings(p)
		// Teardown always obtains the tenant-path client, even when spec and
		// binding inventory are empty: a derived Connection can still be waiting
		// on its Infrastructure finalizer after its logical intent was removed.
		token, err := r.ensureIdentity(ctx, c, p)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("project identity for teardown: %w", err)
		}
		if token == "" {
			return ctrl.Result{RequeueAfter: requeueInterval}, nil
		}
		tc, err := r.tenantClient(clusterName, token, c)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("tenant client for teardown: %w", err)
		}
		if len(bound) > 0 || len(p.Status.BindingInventory) > 0 {
			if err := teardownProviderBindings(ctx, tc, p, bound); err != nil {
				return ctrl.Result{}, fmt.Errorf("tearing down provider bindings: %w", err)
			}
		}
		connectionsPending, err := deleteProjectConnections(ctx, tc, p)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("tearing down project connections: %w", err)
		}
		if connectionsPending {
			return ctrl.Result{RequeueAfter: requeueInterval}, nil
		}
	}
	if instanceFinalizer {
		controllerutil.RemoveFinalizer(p, finalizer)
	}
	if err := c.Update(ctx, p); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// cleanupRemovedBindings uses the controller-maintained status inventory to
// lifecycle resources whose binding disappeared from spec. Dynamic provider
// kinds cannot be rediscovered from the new spec, so the inventory retains the
// complete GVR/name until cleanup has succeeded.
func cleanupRemovedBindings(ctx context.Context, c client.Client, p *aiv1alpha1.Project, bound []boundEnv) error {
	if p == nil || len(p.Status.BindingInventory) == 0 {
		return nil
	}
	current := map[string]struct{}{}
	for _, env := range bound {
		for _, binding := range env.bindings {
			if key := bindingIdentity(p, env.spec.Name, binding); key != "" {
				current[key] = struct{}{}
			}
		}
	}
	for _, item := range p.Status.BindingInventory {
		if key := inventoryIdentity(item); key != "" {
			if _, stillBound := current[key]; stillBound {
				continue
			}
		}
		obj, err := objectForResourceRef(item.ResourceRef)
		if err != nil {
			return fmt.Errorf("binding %q in environment %q: %w", item.Binding, item.Environment, err)
		}
		if err := lifecycleProviderObject(ctx, c, obj, p, item.Environment, item.DeletionPolicy, item.Binding); err != nil {
			return err
		}
	}
	return nil
}

// teardownProviderBindings handles both currently-declared bindings and stale
// inventory entries during Project deletion. The inventory pass is deduped so
// a binding observed in both places is handled exactly once.
func teardownProviderBindings(ctx context.Context, c client.Client, p *aiv1alpha1.Project, bound []boundEnv) error {
	current := map[string]struct{}{}
	for _, env := range bound {
		for _, binding := range env.bindings {
			key := bindingIdentity(p, env.spec.Name, binding)
			if key != "" {
				current[key] = struct{}{}
			}
			obj, err := objectForBinding(p, binding)
			if err != nil {
				// A malformed binding could not have been created by this
				// controller, so there is no object identity to tear down.
				continue
			}
			if err := lifecycleProviderObject(ctx, c, obj, p, env.spec.Name, bindings.BindingDeletionPolicy(env.spec, binding), binding.Name); err != nil {
				return err
			}
		}
	}
	for _, item := range p.Status.BindingInventory {
		if key := inventoryIdentity(item); key != "" {
			if _, handled := current[key]; handled {
				continue
			}
		}
		obj, err := objectForResourceRef(item.ResourceRef)
		if err != nil {
			return fmt.Errorf("inventory binding %q in environment %q: %w", item.Binding, item.Environment, err)
		}
		if err := lifecycleProviderObject(ctx, c, obj, p, item.Environment, item.DeletionPolicy, item.Binding); err != nil {
			return err
		}
	}
	return nil
}

func lifecycleProviderObject(ctx context.Context, c client.Client, obj *unstructured.Unstructured, p *aiv1alpha1.Project, environment string, policy aiv1alpha1.ProjectBindingDeletionPolicy, bindingName string) error {
	if policy == aiv1alpha1.ProjectBindingDeletionPolicyRetain {
		if err := retainInstance(ctx, c, obj, p, environment); err != nil {
			return fmt.Errorf("retaining instance for binding %q: %w", bindingName, err)
		}
		return nil
	}
	if err := c.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting instance for binding %q: %w", bindingName, err)
	}
	return nil
}

func objectForBinding(p *aiv1alpha1.Project, binding aiv1alpha1.ProjectProviderBindingSpec) (*unstructured.Unstructured, error) {
	ref := normalizedResourceRef(p, binding)
	return objectForResourceRef(ref)
}

func objectForResourceRef(ref *aiv1alpha1.ProjectProviderResourceReference) (*unstructured.Unstructured, error) {
	if ref == nil {
		return nil, fmt.Errorf("resourceRef is required")
	}
	if _, err := bindings.GVR(ref); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(ref.Name)
	if name == "" {
		return nil, fmt.Errorf("resourceRef.name is required for lifecycle cleanup")
	}
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion(strings.TrimSpace(ref.APIVersion))
	obj.SetKind(strings.TrimSpace(ref.Kind))
	obj.SetName(name)
	return obj, nil
}

func normalizedResourceRef(p *aiv1alpha1.Project, binding aiv1alpha1.ProjectProviderBindingSpec) *aiv1alpha1.ProjectProviderResourceReference {
	if binding.ResourceRef == nil {
		return nil
	}
	ref := *binding.ResourceRef
	if strings.TrimSpace(ref.Name) == "" {
		values, err := bindings.Values(binding)
		if err != nil {
			return nil
		}
		ref.Name = bindings.ResourceName(p, binding, values)
	}
	return &ref
}

func bindingIdentity(p *aiv1alpha1.Project, environment string, binding aiv1alpha1.ProjectProviderBindingSpec) string {
	ref := normalizedResourceRef(p, binding)
	return resourceIdentity(environment, binding.Provider, binding.Name, ref)
}

func inventoryIdentity(item aiv1alpha1.ProjectBindingInventoryStatus) string {
	return resourceIdentity(item.Environment, item.Provider, item.Binding, item.ResourceRef)
}

func resourceIdentity(environment, provider, binding string, ref *aiv1alpha1.ProjectProviderResourceReference) string {
	if ref == nil {
		return ""
	}
	return strings.Join([]string{
		strings.TrimSpace(environment),
		strings.TrimSpace(provider),
		strings.TrimSpace(binding),
		strings.TrimSpace(ref.APIVersion),
		strings.TrimSpace(ref.Kind),
		strings.TrimSpace(ref.Resource),
		strings.TrimSpace(ref.Name),
	}, "\x00")
}

func bindingInventory(p *aiv1alpha1.Project, bound []boundEnv) []aiv1alpha1.ProjectBindingInventoryStatus {
	var out []aiv1alpha1.ProjectBindingInventoryStatus
	for _, env := range bound {
		for _, binding := range env.bindings {
			ref := normalizedResourceRef(p, binding)
			if ref == nil || strings.TrimSpace(ref.Name) == "" {
				continue
			}
			refCopy := *ref
			out = append(out, aiv1alpha1.ProjectBindingInventoryStatus{
				Environment:    env.spec.Name,
				Binding:        binding.Name,
				Provider:       binding.Provider,
				ResourceRef:    &refCopy,
				DeletionPolicy: bindings.BindingDeletionPolicy(env.spec, binding),
			})
		}
	}
	return out
}

func bindingInventoryEqual(a, b []aiv1alpha1.ProjectBindingInventoryStatus) bool {
	return equality.Semantic.DeepEqual(a, b)
}

// retainInstance removes only this Project's owner reference and records the
// former identity as labels. The provider resource remains under its own
// finalizers and provider ownership after the Project is deleted.
func retainInstance(ctx context.Context, c client.Client, key *unstructured.Unstructured, p *aiv1alpha1.Project, environment string) error {
	if c == nil || key == nil || p == nil {
		return fmt.Errorf("retention requires client, instance key, and project")
	}
	current := &unstructured.Unstructured{}
	current.SetGroupVersionKind(key.GroupVersionKind())
	if err := c.Get(ctx, types.NamespacedName{Name: key.GetName()}, current); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}

	changed := false
	owner := bindings.OwnerRef(p)
	if owner != nil {
		refs := current.GetOwnerReferences()
		kept := make([]metav1.OwnerReference, 0, len(refs))
		for _, ref := range refs {
			if ref.APIVersion == owner.APIVersion && ref.Kind == owner.Kind && ref.Name == owner.Name && ref.UID == owner.UID {
				changed = true
				continue
			}
			kept = append(kept, ref)
		}
		if changed {
			current.SetOwnerReferences(kept)
		}
	}

	labels := current.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	for key, value := range map[string]string{
		bindings.RetainedLabel:            "true",
		bindings.RetainedProjectLabel:     p.Name,
		bindings.RetainedEnvironmentLabel: environment,
	} {
		if labels[key] != value {
			labels[key] = value
			changed = true
		}
	}
	if _, ok := labels[bindings.ProjectLabel]; ok {
		delete(labels, bindings.ProjectLabel)
		changed = true
	}
	if !changed {
		return nil
	}
	current.SetLabels(labels)
	return c.Update(ctx, current)
}

func attachmentScopeForProject(p *aiv1alpha1.Project) (store.Scope, bool) {
	if p == nil || strings.TrimSpace(string(p.UID)) == "" {
		return store.Scope{}, false
	}
	annotations := p.GetAnnotations()
	org := strings.TrimSpace(annotations[bindings.OrgUUIDAnnotation])
	workspace := strings.TrimSpace(annotations[bindings.WorkspaceUUIDAnnotation])
	if org == "" || workspace == "" || strings.TrimSpace(p.Name) == "" {
		return store.Scope{}, false
	}
	return store.Scope{OrgUUID: org, WorkspaceUUID: workspace, ProjectName: p.Name, ProjectUID: string(p.UID)}, true
}

// environmentStatusesEqual compares mirrored environment statuses.
func environmentStatusesEqual(a, b []aiv1alpha1.ProjectEnvironmentStatus) bool {
	return equality.Semantic.DeepEqual(a, b)
}

// equalSpecAndMeta reports whether converging would be a no-op (spec, labels,
// and ownerReferences already match the desired state).
func equalSpecAndMeta(got, next *unstructured.Unstructured) bool {
	return equality.Semantic.DeepEqual(got.Object["spec"], next.Object["spec"]) &&
		equality.Semantic.DeepEqual(got.GetLabels(), next.GetLabels()) &&
		equality.Semantic.DeepEqual(got.GetOwnerReferences(), next.GetOwnerReferences())
}

// boundEnv pairs an environment spec with its provider-resource bindings.
type boundEnv struct {
	spec     aiv1alpha1.ProjectEnvironmentSpec
	bindings []aiv1alpha1.ProjectProviderBindingSpec
}

// providerBindings selects every environment's provider-resource bindings —
// live (development) AND artifact (production) alike. Promotion is a spec
// write appending the production binding; converging it here is what
// provisions the production instance (the HTTP layer no longer does).
func providerBindings(p *aiv1alpha1.Project) []boundEnv {
	var out []boundEnv
	for _, env := range p.Spec.Environments {
		var bs []aiv1alpha1.ProjectProviderBindingSpec
		for _, binding := range env.Bindings {
			if binding.Kind != aiv1alpha1.ProjectBindingKindProviderResource || binding.ResourceRef == nil {
				continue
			}
			bs = append(bs, binding)
		}
		if len(bs) == 0 {
			continue
		}
		out = append(out, boundEnv{spec: env, bindings: bs})
	}
	return out
}
