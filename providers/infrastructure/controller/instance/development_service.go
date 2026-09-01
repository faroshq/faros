// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package instance

// DevelopmentService is the Infrastructure-owned runtime bridge for one
// project listener. App Studio writes only the intent CR in the tenant
// workspace. This reconciler owns the data-plane Service, private access gate,
// Gateway route, and the argv-only process registration sent to the sandbox's
// dev-agent. Keeping this seam here means App Studio never becomes a second
// Kubernetes resource writer for runtime objects.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/multicluster-runtime/pkg/reconcile"

	infrav1alpha1 "github.com/faroshq/provider-infrastructure/apis/v1alpha1"
	"github.com/faroshq/provider-infrastructure/apps"
	backendkro "github.com/faroshq/provider-infrastructure/backend/kro"
	"github.com/faroshq/provider-infrastructure/kro"
)

var (
	developmentServiceGVR = schema.GroupVersionResource{
		Group: infrav1alpha1.GroupName, Version: infrav1alpha1.Version, Resource: "developmentservices",
	}
	coreServiceGVR       = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "services"}
	deploymentGVR        = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	httpRouteGVR         = schema.GroupVersionResource{Group: "gateway.networking.k8s.io", Version: "v1", Resource: "httproutes"}
	developmentSecretGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "secrets"}
)

const (
	developmentEnvironment       = "development"
	controlPort                  = int32(7070)
	developmentServiceMaxArgv    = 32
	developmentServiceMaxEnabled = 8
	serviceRequeue               = 5 * time.Second

	developmentServiceManagedLabel = "faros.sh/managed-by"
	developmentServiceNameLabel    = "faros.sh/development-service"
	developmentSandboxNameLabel    = "faros.sh/sandbox"
	developmentServiceManagedValue = "infrastructure-development-service"

	processPhaseRunning = "Running"
	processPhaseStopped = "Stopped"
	processPhaseFailed  = "Failed"
)

type developmentServiceReconciler struct{ c *Controller }

type normalizedDevelopmentService struct {
	Enabled       bool
	Environment   string
	ComponentRef  string
	Argv          []string
	WorkingDir    string
	Protocol      string
	Port          int32
	HealthPath    string
	Visibility    infrav1alpha1.DevelopmentServiceVisibility
	RestartPolicy infrav1alpha1.DevelopmentServiceRestartPolicy
	SandboxName   string
	SandboxUID    string
	ProjectName   string
	ProjectUID    string
}

type sandboxControlRef struct {
	RuntimeNamespace string
	ServiceName      string
	ServiceNamespace string
	SecretName       string
	SecretNamespace  string
	WorkloadName     string
	Ready            bool
	Suspended        bool
}

type devAgentServiceRequest struct {
	Name               string            `json:"name"`
	Argv               []string          `json:"argv"`
	WorkDir            string            `json:"workDir,omitempty"`
	Port               int32             `json:"port"`
	HealthPath         string            `json:"healthPath,omitempty"`
	EnvFiles           map[string]string `json:"envFiles,omitempty"`
	ConnectionRevision string            `json:"connectionRevision,omitempty"`
	Enabled            bool              `json:"enabled"`
	RestartPolicy      string            `json:"restartPolicy,omitempty"`
}

type devAgentServiceStatus struct {
	Name          string `json:"name"`
	Phase         string `json:"phase,omitempty"`
	Running       bool   `json:"running"`
	PortListening bool   `json:"portListening"`
	Reachable     bool   `json:"reachable"`
	RestartCount  int64  `json:"restartCount,omitempty"`
	LastExitCode  *int32 `json:"lastExitCode,omitempty"`
	Message       string `json:"message,omitempty"`
}

// Reconcile converges one DevelopmentService in the tenant workspace.
func (r *developmentServiceReconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	c := r.c
	cluster := string(req.ClusterName)
	cl, err := c.mgr.GetCluster(ctx, req.ClusterName)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("getting workspace cluster %s: %w", cluster, err)
	}
	tenantClient := cl.GetClient()

	service := &infrav1alpha1.DevelopmentService{}
	if err := tenantClient.Get(ctx, req.NamespacedName, service); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !service.GetDeletionTimestamp().IsZero() {
		return r.finalize(ctx, tenantClient, cluster, service)
	}
	if !containsFinalizer(service, infrav1alpha1.DevelopmentServiceFinalizer) {
		service.Finalizers = append(service.Finalizers, infrav1alpha1.DevelopmentServiceFinalizer)
		if err := tenantClient.Update(ctx, service); err != nil {
			return ctrl.Result{}, fmt.Errorf("adding development service finalizer: %w", err)
		}
		return ctrl.Result{}, nil
	}

	normalized, err := normalizeDevelopmentService(service)
	if err != nil {
		return ctrl.Result{RequeueAfter: serviceRequeue}, c.persistDevelopmentServiceStatus(ctx, tenantClient, service,
			developmentServiceErrorStatus(service, "InvalidSpec", err.Error()))
	}
	if normalized.Enabled {
		if err := validateDevelopmentServiceQuota(ctx, tenantClient, service, normalized); err != nil {
			return ctrl.Result{RequeueAfter: serviceRequeue}, c.persistDevelopmentServiceStatus(ctx, tenantClient, service,
				developmentServiceErrorStatus(service, "ServiceQuotaExceeded", err.Error()))
		}
	}
	if c.cfg.BaseDomain == "" {
		return ctrl.Result{RequeueAfter: serviceRequeue}, c.persistDevelopmentServiceStatus(ctx, tenantClient, service,
			developmentServiceErrorStatus(service, "PreviewDomainUnavailable", "private preview requires FAROS_APP_BASE_DOMAIN"))
	}
	if c.cfg.RuntimeConfig == nil {
		return ctrl.Result{RequeueAfter: serviceRequeue}, c.persistDevelopmentServiceStatus(ctx, tenantClient, service,
			developmentServiceErrorStatus(service, "RuntimeUnavailable", "the Infrastructure runtime cluster is not configured"))
	}

	_, sandboxRef, err := c.resolveSandboxControl(ctx, tenantClient, cluster, normalized)
	if err != nil {
		return ctrl.Result{RequeueAfter: serviceRequeue}, c.persistDevelopmentServiceStatus(ctx, tenantClient, service,
			developmentServiceErrorStatus(service, reasonForSandboxError(err), err.Error()))
	}
	host, err := developmentServiceHost(service.Name, cluster, c.cfg.BaseDomain)
	if err != nil {
		return ctrl.Result{RequeueAfter: serviceRequeue}, c.persistDevelopmentServiceStatus(ctx, tenantClient, service,
			developmentServiceErrorStatus(service, "InvalidHost", err.Error()))
	}
	publicScheme := normalizedPublicScheme()
	publicPort := publicPortSuffix()
	publicHost := host + publicPort
	previewURL := publicScheme + "://" + publicHost
	cleanupNamespace := sandboxRef.RuntimeNamespace
	if cleanupNamespace == "" {
		cleanupNamespace = service.Status.RuntimeNamespace
	}

	if !normalized.Enabled {
		if err := c.removeSandboxService(ctx, sandboxRef, service.Name); err != nil {
			return ctrl.Result{RequeueAfter: serviceRequeue}, c.persistDevelopmentServiceStatus(ctx, tenantClient, service,
				developmentServiceErrorStatus(service, "ProcessUnavailable", "disable process: "+err.Error()))
		}
		if err := c.removeDevelopmentServiceResources(ctx, cleanupNamespace, developmentServiceRuntimeNames(service.Name)); err != nil {
			return ctrl.Result{RequeueAfter: serviceRequeue}, c.persistDevelopmentServiceStatus(ctx, tenantClient, service,
				developmentServiceErrorStatus(service, "RuntimeResourcesUnavailable", "disable preview resources: "+err.Error()))
		}
		return ctrl.Result{RequeueAfter: serviceRequeue}, c.persistDevelopmentServiceStatus(ctx, tenantClient, service,
			developmentServiceDisabledStatus(service, host, previewURL, sandboxRef))
	}
	if sandboxRef.Suspended {
		if err := c.removeSandboxService(ctx, sandboxRef, service.Name); err != nil {
			return ctrl.Result{RequeueAfter: serviceRequeue}, c.persistDevelopmentServiceStatus(ctx, tenantClient, service,
				developmentServiceErrorStatus(service, "ProcessUnavailable", "suspended sandbox process cleanup: "+err.Error()))
		}
		if err := c.removeDevelopmentServiceResources(ctx, cleanupNamespace, developmentServiceRuntimeNames(service.Name)); err != nil {
			return ctrl.Result{RequeueAfter: serviceRequeue}, c.persistDevelopmentServiceStatus(ctx, tenantClient, service,
				developmentServiceErrorStatus(service, "RuntimeResourcesUnavailable", "suspend preview resources: "+err.Error()))
		}
		return ctrl.Result{RequeueAfter: serviceRequeue}, c.persistDevelopmentServiceStatus(ctx, tenantClient, service,
			developmentServiceSandboxStatusWithURL(service, sandboxRef, host, previewURL, "SandboxSuspended", "the sandbox is suspended; resume the Instance before starting services"))
	}
	if !sandboxRef.Ready {
		return ctrl.Result{RequeueAfter: serviceRequeue}, c.persistDevelopmentServiceStatus(ctx, tenantClient, service,
			developmentServiceSandboxStatusWithURL(service, sandboxRef, host, previewURL, "SandboxNotReady", "the universal coding sandbox is still provisioning"))
	}

	names := developmentServiceRuntimeNames(service.Name)
	labels := developmentServiceLabels(service.Name, normalized.SandboxName)
	if err := c.ensureDevelopmentServiceResources(ctx, cluster, service, normalized, sandboxRef, host, publicHost, names, labels); err != nil {
		return ctrl.Result{RequeueAfter: serviceRequeue}, c.persistDevelopmentServiceStatus(ctx, tenantClient, service,
			developmentServiceErrorStatus(service, "RuntimeResourcesUnavailable", err.Error()))
	}

	envFiles, connectionRevision, err := c.developmentServiceConnectionFiles(ctx, tenantClient, cluster, service)
	if err != nil {
		cleanupErr := c.failClosedDevelopmentService(ctx, sandboxRef, cleanupNamespace, service.Name)
		message := err.Error()
		if cleanupErr != nil {
			message = fmt.Sprintf("%s; fail-closed cleanup: %v", message, cleanupErr)
		}
		return ctrl.Result{RequeueAfter: serviceRequeue}, c.persistDevelopmentServiceStatus(ctx, tenantClient, service,
			developmentServiceErrorStatus(service, "ConnectionsUnavailable", message))
	}
	processStatus, err := c.configureSandboxService(ctx, sandboxRef, service.Name, normalized, envFiles, connectionRevision)
	if err != nil {
		status := developmentServiceRuntimeStatus(service, sandboxRef, host, previewURL, names, processStatus)
		setDevelopmentServiceCondition(&status, infrav1alpha1.DevelopmentServiceConditionProcessReady, metav1.ConditionFalse, "ProcessUnavailable", err.Error(), service.Generation)
		setDevelopmentServiceCondition(&status, infrav1alpha1.DevelopmentServiceConditionPortListening, metav1.ConditionFalse, "PortNotListening", err.Error(), service.Generation)
		setDevelopmentServiceCondition(&status, infrav1alpha1.DevelopmentServiceConditionReachable, metav1.ConditionFalse, "ProcessUnavailable", err.Error(), service.Generation)
		setDevelopmentServiceCondition(&status, infrav1alpha1.DevelopmentServiceConditionRouteAccepted, metav1.ConditionUnknown, "RoutePending", "waiting for the Gateway to accept the preview route", service.Generation)
		setDevelopmentServiceCondition(&status, infrav1alpha1.DevelopmentServiceConditionReady, metav1.ConditionFalse, "ProcessUnavailable", "the service process is not configured", service.Generation)
		return ctrl.Result{RequeueAfter: serviceRequeue}, c.persistDevelopmentServiceStatus(ctx, tenantClient, service, status)
	}

	status := developmentServiceRuntimeStatus(service, sandboxRef, host, previewURL, names, processStatus)
	setDevelopmentServiceCondition(&status, infrav1alpha1.DevelopmentServiceConditionSandboxReady, metav1.ConditionTrue, "Ready", "the universal coding sandbox is ready", service.Generation)
	setDevelopmentServiceCondition(&status, infrav1alpha1.DevelopmentServiceConditionProcessReady, boolCondition(processStatus.Running), processReason(processStatus), processMessage(processStatus), service.Generation)
	setDevelopmentServiceCondition(&status, infrav1alpha1.DevelopmentServiceConditionPortListening, boolCondition(processStatus.PortListening), processReason(processStatus), processMessage(processStatus), service.Generation)
	setDevelopmentServiceCondition(&status, infrav1alpha1.DevelopmentServiceConditionReachable, boolCondition(processStatus.Reachable), processReason(processStatus), processMessage(processStatus), service.Generation)

	route, routeErr := c.cfg.Runtime.Resource(httpRouteGVR).Namespace(sandboxRef.RuntimeNamespace).Get(ctx, names.route, metav1.GetOptions{})
	if routeErr != nil && !apierrors.IsNotFound(routeErr) {
		setDevelopmentServiceCondition(&status, infrav1alpha1.DevelopmentServiceConditionRouteAccepted, metav1.ConditionUnknown, "RouteReadFailed", routeErr.Error(), service.Generation)
	} else if routeErr == nil && httpRouteAccepted(route, gatewayName(), gatewayNamespace()) {
		setDevelopmentServiceCondition(&status, infrav1alpha1.DevelopmentServiceConditionRouteAccepted, metav1.ConditionTrue, "Accepted", "the preview route is accepted by the Gateway", service.Generation)
	} else {
		setDevelopmentServiceCondition(&status, infrav1alpha1.DevelopmentServiceConditionRouteAccepted, metav1.ConditionFalse, "RoutePending", "the preview route is not accepted by the Gateway", service.Generation)
	}

	ready := processStatus.Running && processStatus.PortListening && processStatus.Reachable && conditionStatus(&status, infrav1alpha1.DevelopmentServiceConditionRouteAccepted) == metav1.ConditionTrue
	if ready {
		setDevelopmentServiceCondition(&status, infrav1alpha1.DevelopmentServiceConditionReady, metav1.ConditionTrue, "Ready", "the process and preview route are ready", service.Generation)
	} else {
		setDevelopmentServiceCondition(&status, infrav1alpha1.DevelopmentServiceConditionReady, metav1.ConditionFalse, "NotReady", "the process, listener, or preview route is not ready", service.Generation)
	}
	status.Ready = ready
	return ctrl.Result{RequeueAfter: serviceRequeue}, c.persistDevelopmentServiceStatus(ctx, tenantClient, service, status)
}

func (r *developmentServiceReconciler) finalize(ctx context.Context, tenantClient client.Client, cluster string, service *infrav1alpha1.DevelopmentService) (ctrl.Result, error) {
	if !containsFinalizer(service, infrav1alpha1.DevelopmentServiceFinalizer) {
		return ctrl.Result{}, nil
	}
	// Process cleanup is best-effort when the sandbox is already gone. Runtime
	// Kubernetes objects are exact-name owned resources and are always removed
	// before releasing the tenant-side finalizer.
	var sandboxRef sandboxControlRef
	if _, ref, err := r.c.resolveSandboxControl(ctx, tenantClient, cluster, normalizedFromStoredService(service)); err == nil {
		sandboxRef = ref
		if err := r.c.removeSandboxService(ctx, ref, service.Name); err != nil {
			return ctrl.Result{RequeueAfter: serviceRequeue}, err
		}
	}
	names := developmentServiceRuntimeNames(service.Name)
	namespace := service.Status.RuntimeNamespace
	if namespace == "" {
		namespace = sandboxRef.RuntimeNamespace
	}
	if namespace != "" {
		for _, target := range []struct {
			gvr  schema.GroupVersionResource
			name string
		}{{httpRouteGVR, names.route}, {coreServiceGVR, names.gateService}, {deploymentGVR, names.gateDeployment}, {coreServiceGVR, names.backendService}} {
			if err := deleteRuntimeObject(ctx, r.c.cfg.Runtime, target.gvr, namespace, target.name); err != nil {
				return ctrl.Result{RequeueAfter: serviceRequeue}, err
			}
		}
	}
	service.Finalizers = removeFinalizer(service.Finalizers, infrav1alpha1.DevelopmentServiceFinalizer)
	if err := tenantClient.Update(ctx, service); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// removeDevelopmentServiceResources removes only the exact objects this
// reconciler creates for one DevelopmentService. Disabled and suspended
// services retain their CR identity and stable host, but must not leave a
// routable gate or backend behind while their process is absent.
func (c *Controller) removeDevelopmentServiceResources(ctx context.Context, namespace string, names developmentServiceNames) error {
	if c == nil || c.cfg.Runtime == nil || namespace == "" {
		return nil
	}
	for _, target := range []struct {
		gvr  schema.GroupVersionResource
		name string
	}{
		{httpRouteGVR, names.route},
		{coreServiceGVR, names.gateService},
		{deploymentGVR, names.gateDeployment},
		{coreServiceGVR, names.backendService},
	} {
		if err := deleteRuntimeObject(ctx, c.cfg.Runtime, target.gvr, namespace, target.name); err != nil {
			return fmt.Errorf("delete %s %q: %w", target.gvr.Resource, target.name, err)
		}
	}
	return nil
}

// failClosedDevelopmentService removes both halves of a service's exposure
// before reporting a connection/readiness failure. A service process may have
// already inherited the previous connection values, so retaining either the
// process or its route would continue serving stale credentials. Attempt every
// cleanup independently so a failed control-plane DELETE cannot leave a route
// or gate behind.
func (c *Controller) failClosedDevelopmentService(ctx context.Context, sandbox sandboxControlRef, namespace, serviceName string) error {
	var cleanupErrs []error
	if err := c.removeSandboxService(ctx, sandbox, serviceName); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("stop service process: %w", err))
	}
	if err := c.removeDevelopmentServiceResources(ctx, namespace, developmentServiceRuntimeNames(serviceName)); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("remove service exposure: %w", err))
	}
	return errors.Join(cleanupErrs...)
}

func normalizeDevelopmentService(service *infrav1alpha1.DevelopmentService) (normalizedDevelopmentService, error) {
	if service == nil {
		return normalizedDevelopmentService{}, errors.New("development service is required")
	}
	spec := service.Spec
	if err := validateDevelopmentServiceReferences(spec); err != nil {
		return normalizedDevelopmentService{}, err
	}
	if strings.TrimSpace(spec.Environment) != developmentEnvironment {
		return normalizedDevelopmentService{}, fmt.Errorf("spec.environment must be %q", developmentEnvironment)
	}
	if spec.ComponentRef != "" && len(validation.IsDNS1123Subdomain(spec.ComponentRef)) != 0 {
		return normalizedDevelopmentService{}, fmt.Errorf("spec.componentRef %q is not a DNS name", spec.ComponentRef)
	}
	if len(spec.Command.Argv) == 0 || len(spec.Command.Argv) > developmentServiceMaxArgv {
		return normalizedDevelopmentService{}, fmt.Errorf("spec.command.argv must contain 1-%d arguments", developmentServiceMaxArgv)
	}
	for i, arg := range spec.Command.Argv {
		if arg == "" || strings.IndexByte(arg, 0) >= 0 || len(arg) > 4096 {
			return normalizedDevelopmentService{}, fmt.Errorf("spec.command.argv[%d] is empty, contains NUL, or exceeds 4096 bytes", i)
		}
	}
	workingDir, err := cleanDevelopmentWorkDir(spec.Command.WorkingDirectory)
	if err != nil {
		return normalizedDevelopmentService{}, err
	}
	protocol := strings.ToUpper(strings.TrimSpace(spec.Endpoint.Protocol))
	if protocol == "" {
		protocol = "HTTP"
	}
	if protocol != "HTTP" {
		return normalizedDevelopmentService{}, errors.New("spec.endpoint.protocol must be HTTP")
	}
	if spec.Endpoint.Port < 1 || spec.Endpoint.Port > 65535 {
		return normalizedDevelopmentService{}, errors.New("spec.endpoint.port must be between 1 and 65535")
	}
	if spec.Endpoint.Port >= controlPort && spec.Endpoint.Port <= controlPort+3 {
		return normalizedDevelopmentService{}, fmt.Errorf("spec.endpoint.port %d is reserved by the sandbox control plane", spec.Endpoint.Port)
	}
	if spec.Endpoint.HealthPath != "" && (len(spec.Endpoint.HealthPath) > 512 || !strings.HasPrefix(spec.Endpoint.HealthPath, "/") || strings.IndexByte(spec.Endpoint.HealthPath, 0) >= 0) {
		return normalizedDevelopmentService{}, errors.New("spec.endpoint.healthPath must be an absolute path of at most 512 bytes")
	}
	visibility := spec.Exposure.Visibility
	if visibility == "" {
		visibility = infrav1alpha1.DevelopmentServiceVisibilityPrivate
	}
	switch strings.ToLower(strings.TrimSpace(string(visibility))) {
	case "private":
		visibility = infrav1alpha1.DevelopmentServiceVisibilityPrivate
	case "public":
		visibility = infrav1alpha1.DevelopmentServiceVisibilityPublic
	default:
		return normalizedDevelopmentService{}, errors.New("spec.exposure.visibility must be private or public")
	}
	restartPolicy := spec.RestartPolicy
	if restartPolicy == "" {
		restartPolicy = infrav1alpha1.DevelopmentServiceRestartAlways
	}
	switch restartPolicy {
	case infrav1alpha1.DevelopmentServiceRestartAlways, infrav1alpha1.DevelopmentServiceRestartOnFailure, infrav1alpha1.DevelopmentServiceRestartNever:
	default:
		return normalizedDevelopmentService{}, errors.New("spec.restartPolicy must be Always, OnFailure, or Never")
	}
	enabled := true
	if spec.Enabled != nil {
		enabled = *spec.Enabled
	}
	return normalizedDevelopmentService{
		Enabled: enabled, Environment: developmentEnvironment,
		ComponentRef: spec.ComponentRef, Argv: append([]string(nil), spec.Command.Argv...),
		WorkingDir: workingDir, Protocol: protocol, Port: spec.Endpoint.Port,
		HealthPath: spec.Endpoint.HealthPath, Visibility: visibility, RestartPolicy: restartPolicy,
		SandboxName: spec.SandboxRef.Name, SandboxUID: spec.SandboxRef.UID,
		ProjectName: spec.ProjectRef.Name, ProjectUID: spec.ProjectRef.UID,
	}, nil
}

func validateDevelopmentServiceReferences(spec infrav1alpha1.DevelopmentServiceSpec) error {
	if strings.TrimSpace(spec.ProjectRef.Name) == "" {
		return errors.New("spec.projectRef.name is required")
	}
	if strings.TrimSpace(spec.ProjectRef.UID) == "" {
		return errors.New("spec.projectRef.uid is required")
	}
	if strings.TrimSpace(spec.SandboxRef.Name) == "" {
		return errors.New("spec.sandboxRef.name is required")
	}
	if strings.TrimSpace(spec.SandboxRef.UID) == "" {
		return errors.New("spec.sandboxRef.uid is required")
	}
	return nil
}

func validateDevelopmentServiceQuota(ctx context.Context, tenantClient client.Client, service *infrav1alpha1.DevelopmentService, normalized normalizedDevelopmentService) error {
	list := &infrav1alpha1.DevelopmentServiceList{}
	if err := tenantClient.List(ctx, list); err != nil {
		return fmt.Errorf("list development services for quota: %w", err)
	}
	count := 0
	for i := range list.Items {
		item := &list.Items[i]
		if item.Name == service.Name || !item.DeletionTimestamp.IsZero() {
			continue
		}
		itemSpec, err := normalizeDevelopmentService(item)
		if err != nil || !itemSpec.Enabled || itemSpec.Environment != normalized.Environment || itemSpec.SandboxName != normalized.SandboxName {
			continue
		}
		if normalized.SandboxUID != itemSpec.SandboxUID {
			continue
		}
		count++
	}
	if count >= developmentServiceMaxEnabled {
		return fmt.Errorf("at most %d enabled development services may target sandbox %q", developmentServiceMaxEnabled, normalized.SandboxName)
	}
	return nil
}

func cleanDevelopmentWorkDir(raw string) (string, error) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if raw == "" {
		return ".", nil
	}
	if len(raw) > 512 || path.IsAbs(raw) {
		return "", errors.New("spec.command.workingDirectory must be relative to the sandbox workspace")
	}
	clean := path.Clean(raw)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.IndexByte(clean, 0) >= 0 {
		if clean == "." {
			return clean, nil
		}
		return "", errors.New("spec.command.workingDirectory must remain inside the sandbox workspace")
	}
	return clean, nil
}

func (c *Controller) resolveSandboxControl(ctx context.Context, tenantClient client.Client, tenant string, spec normalizedDevelopmentService) (*unstructured.Unstructured, sandboxControlRef, error) {
	if strings.TrimSpace(spec.SandboxName) == "" {
		return nil, sandboxControlRef{}, errors.New("sandbox reference name is required")
	}
	if strings.TrimSpace(spec.SandboxUID) == "" {
		return nil, sandboxControlRef{}, errors.New("sandbox reference UID is required")
	}
	sandbox := &unstructured.Unstructured{}
	sandbox.SetGroupVersionKind(instanceGVK)
	if err := tenantClient.Get(ctx, client.ObjectKey{Name: spec.SandboxName}, sandbox); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, sandboxControlRef{}, fmt.Errorf("sandbox Instance %q was not found", spec.SandboxName)
		}
		return nil, sandboxControlRef{}, fmt.Errorf("get sandbox Instance %q: %w", spec.SandboxName, err)
	}
	if string(sandbox.GetUID()) != spec.SandboxUID {
		return nil, sandboxControlRef{}, fmt.Errorf("sandbox Instance %q UID does not match spec.sandboxRef.uid", spec.SandboxName)
	}
	if !sandboxOwnedByProject(sandbox, spec) {
		return nil, sandboxControlRef{}, fmt.Errorf("sandbox Instance %q is not owned by Project %q with UID %q", spec.SandboxName, spec.ProjectName, spec.ProjectUID)
	}
	templateName, _, _ := unstructured.NestedString(sandbox.Object, "spec", "template")
	if templateName != infrav1alpha1.UniversalCodingSandboxTemplateName {
		return nil, sandboxControlRef{}, fmt.Errorf("sandbox Instance %q must use template %q", spec.SandboxName, infrav1alpha1.UniversalCodingSandboxTemplateName)
	}
	mode, _, _ := unstructured.NestedString(sandbox.Object, "spec", "values", infrav1alpha1.FarosModeField)
	if mode != "" && mode != infrav1alpha1.FarosModeDevelopment {
		return nil, sandboxControlRef{}, fmt.Errorf("sandbox Instance %q is not in development mode", spec.SandboxName)
	}
	status, _, _ := unstructured.NestedMap(sandbox.Object, "status")
	ref := sandboxControlRef{}
	ref.RuntimeNamespace, _ = status["runtimeNamespace"].(string)
	if ref.RuntimeNamespace == "" {
		ref.RuntimeNamespace = kro.RuntimeNamespace(tenant, sandbox.GetNamespace())
	}
	ref.ServiceName, _, _ = unstructured.NestedString(sandbox.Object, "status", "components", "workspace", "controlServiceRef", "name")
	ref.ServiceNamespace, _, _ = unstructured.NestedString(sandbox.Object, "status", "components", "workspace", "controlServiceRef", "namespace")
	if ref.ServiceNamespace == "" {
		ref.ServiceNamespace = ref.RuntimeNamespace
	}
	ref.SecretName, _, _ = unstructured.NestedString(sandbox.Object, "status", "controlSecretRef", "name")
	ref.SecretNamespace, _, _ = unstructured.NestedString(sandbox.Object, "status", "controlSecretRef", "namespace")
	if ref.SecretNamespace == "" {
		ref.SecretNamespace = ref.RuntimeNamespace
	}
	ref.WorkloadName, _, _ = unstructured.NestedString(sandbox.Object, "spec", "values", "name")
	if ref.WorkloadName == "" {
		ref.WorkloadName = sandbox.GetName()
	}
	ref.Suspended, _, _ = unstructured.NestedBool(sandbox.Object, "spec", "lifecycle", "suspended")
	ref.Ready = sandboxReady(status) && ref.ServiceName != "" && ref.SecretName != ""
	if ref.ServiceName == "" || ref.SecretName == "" {
		return sandbox, ref, nil
	}
	return sandbox, ref, nil
}

func sandboxOwnedByProject(sandbox *unstructured.Unstructured, spec normalizedDevelopmentService) bool {
	if sandbox == nil || strings.TrimSpace(spec.ProjectName) == "" || strings.TrimSpace(spec.ProjectUID) == "" {
		return false
	}
	for _, owner := range sandbox.GetOwnerReferences() {
		groupVersion, err := schema.ParseGroupVersion(owner.APIVersion)
		if err != nil || groupVersion.Group != "ai.faros.sh" || owner.Kind != "Project" {
			continue
		}
		if owner.Controller == nil || !*owner.Controller {
			continue
		}
		if owner.Name == spec.ProjectName && string(owner.UID) == spec.ProjectUID {
			return true
		}
	}
	return false
}

func sandboxReady(status map[string]any) bool {
	if status == nil {
		return false
	}
	if status["phase"] == "Ready" {
		return true
	}
	if ready, _ := status["ready"].(bool); ready {
		return true
	}
	conditions, _ := status["conditions"].([]any)
	return conditionTrue(conditions, "Ready")
}

func reasonForSandboxError(err error) string {
	if err == nil {
		return "SandboxUnavailable"
	}
	if strings.Contains(err.Error(), "UID") {
		return "SandboxIdentityMismatch"
	}
	if strings.Contains(err.Error(), "template") || strings.Contains(err.Error(), "development mode") {
		return "SandboxContractInvalid"
	}
	return "SandboxUnavailable"
}

func (c *Controller) ensureDevelopmentServiceResources(ctx context.Context, tenant string, service *infrav1alpha1.DevelopmentService, spec normalizedDevelopmentService, sandbox sandboxControlRef, host, publicHost string, names developmentServiceNames, labels map[string]string) error {
	backend := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Service",
		"metadata": map[string]any{"name": names.backendService, "namespace": sandbox.RuntimeNamespace, "labels": unstructuredLabels(labels)},
		"spec": map[string]any{
			"type": "ClusterIP", "selector": map[string]any{"app": sandbox.WorkloadName},
			"ports": []any{map[string]any{"name": "http", "port": int64(spec.Port), "targetPort": int64(spec.Port)}},
		},
	}}
	if _, err := ensureRuntimeObject(ctx, c.cfg.Runtime, coreServiceGVR, sandbox.RuntimeNamespace, names.backendService, backend); err != nil {
		return fmt.Errorf("backend Service: %w", err)
	}

	image := strings.TrimSpace(os.Getenv("FAROS_ACCESS_PROXY_IMAGE"))
	if image == "" {
		image = backendkro.DefaultAccessProxyImage
	}
	hubURL := strings.TrimSpace(os.Getenv("FAROS_ACCESS_HUB_URL"))
	if hubURL == "" {
		hubURL = strings.TrimSpace(os.Getenv("FAROS_HUB_URL"))
	}
	if spec.Visibility == infrav1alpha1.DevelopmentServiceVisibilityPrivate && hubURL == "" {
		return errors.New("private preview requires FAROS_ACCESS_HUB_URL or FAROS_HUB_URL")
	}
	hubPublicURL := strings.TrimSpace(os.Getenv("FAROS_ACCESS_HUB_PUBLIC_URL"))
	if hubPublicURL == "" {
		hubPublicURL = strings.TrimSpace(os.Getenv("FAROS_HUB_PUBLIC_URL"))
	}
	insecure := strings.EqualFold(strings.TrimSpace(os.Getenv("FAROS_HUB_INSECURE_SKIP_TLS_VERIFY")), "true") || strings.EqualFold(strings.TrimSpace(os.Getenv("FAROS_ACCESS_HUB_INSECURE")), "true")
	routeTarget := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", names.backendService, sandbox.RuntimeNamespace, spec.Port)
	gate := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": names.gateDeployment, "namespace": sandbox.RuntimeNamespace, "labels": unstructuredLabels(labels)},
		"spec": map[string]any{
			"replicas": int64(1), "strategy": map[string]any{"type": "Recreate"},
			"selector": map[string]any{"matchLabels": map[string]any{"app": names.gateService}},
			"template": map[string]any{
				"metadata": map[string]any{"labels": map[string]any{"app": names.gateService, developmentServiceManagedLabel: developmentServiceManagedValue}},
				"spec": map[string]any{
					"automountServiceAccountToken": false,
					"containers": []any{map[string]any{
						"name": "gate", "image": image, "imagePullPolicy": "IfNotPresent",
						"env": []any{
							map[string]any{"name": "FAROS_ACCESS_PROXY_MODE", "value": accessProxyVisibility(spec.Visibility)},
							map[string]any{"name": "FAROS_ACCESS_PROXY_HOST", "value": publicHost},
							map[string]any{"name": "FAROS_ACCESS_PROXY_PUBLIC_SCHEME", "value": normalizedPublicScheme()},
							map[string]any{"name": "FAROS_ACCESS_PROXY_ROUTES", "value": "/=" + routeTarget},
							map[string]any{"name": "FAROS_ACCESS_PROXY_INSTANCE_CLUSTER", "value": tenant},
							map[string]any{"name": "FAROS_ACCESS_PROXY_INSTANCE_GROUP", "value": infrav1alpha1.GroupName},
							map[string]any{"name": "FAROS_ACCESS_PROXY_INSTANCE_RESOURCE", "value": developmentServiceGVR.Resource},
							map[string]any{"name": "FAROS_ACCESS_PROXY_INSTANCE_NAME", "value": service.Name},
							map[string]any{"name": "FAROS_HUB_URL", "value": hubURL},
							map[string]any{"name": "FAROS_HUB_PUBLIC_URL", "value": hubPublicURL},
							map[string]any{"name": "FAROS_HUB_INSECURE_SKIP_TLS_VERIFY", "value": strconv.FormatBool(insecure)},
						},
						"ports":           []any{map[string]any{"name": "http", "containerPort": int64(8080)}},
						"readinessProbe":  map[string]any{"tcpSocket": map[string]any{"port": "http"}, "periodSeconds": int64(5)},
						"resources":       map[string]any{"requests": map[string]any{"cpu": "10m", "memory": "32Mi"}, "limits": map[string]any{"memory": "128Mi"}},
						"securityContext": map[string]any{"runAsNonRoot": true, "allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true, "capabilities": map[string]any{"drop": []any{"ALL"}}},
					}},
				},
			},
		},
	}}
	if _, err := ensureRuntimeObject(ctx, c.cfg.Runtime, deploymentGVR, sandbox.RuntimeNamespace, names.gateDeployment, gate); err != nil {
		return fmt.Errorf("private access gate Deployment: %w", err)
	}

	gateService := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1", "kind": "Service",
		"metadata": map[string]any{"name": names.gateService, "namespace": sandbox.RuntimeNamespace, "labels": unstructuredLabels(labels)},
		"spec":     map[string]any{"type": "ClusterIP", "selector": map[string]any{"app": names.gateService}, "ports": []any{map[string]any{"name": "http", "port": int64(8080), "targetPort": "http"}}},
	}}
	if _, err := ensureRuntimeObject(ctx, c.cfg.Runtime, coreServiceGVR, sandbox.RuntimeNamespace, names.gateService, gateService); err != nil {
		return fmt.Errorf("private access gate Service: %w", err)
	}

	route := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1", "kind": "HTTPRoute",
		"metadata": map[string]any{"name": names.route, "namespace": sandbox.RuntimeNamespace, "labels": unstructuredLabels(labels)},
		"spec": map[string]any{
			"parentRefs": []any{map[string]any{"group": "gateway.networking.k8s.io", "kind": "Gateway", "name": gatewayName(), "namespace": gatewayNamespace()}},
			"hostnames":  []any{host},
			"rules":      []any{map[string]any{"matches": []any{map[string]any{"path": map[string]any{"type": "PathPrefix", "value": "/"}}}, "backendRefs": []any{map[string]any{"name": names.gateService, "port": int64(8080)}}}},
		},
	}}
	if _, err := ensureRuntimeObject(ctx, c.cfg.Runtime, httpRouteGVR, sandbox.RuntimeNamespace, names.route, route); err != nil {
		return fmt.Errorf("preview HTTPRoute: %w", err)
	}
	_ = service
	return nil
}

func (c *Controller) configureSandboxService(ctx context.Context, sandbox sandboxControlRef, name string, spec normalizedDevelopmentService, envFiles map[string]string, connectionRevision string) (devAgentServiceStatus, error) {
	token, err := c.controlToken(ctx, sandbox)
	if err != nil {
		return devAgentServiceStatus{}, err
	}
	request := devAgentServiceRequest{Name: name, Argv: spec.Argv, WorkDir: spec.WorkingDir, Port: spec.Port, HealthPath: spec.HealthPath, EnvFiles: envFiles, ConnectionRevision: connectionRevision, Enabled: true, RestartPolicy: string(spec.RestartPolicy)}
	var status devAgentServiceStatus
	if err := c.proxyControl(ctx, sandbox, token, http.MethodPost, "/service", request, &status); err != nil {
		return devAgentServiceStatus{}, err
	}
	return status, nil
}

func (c *Controller) removeSandboxService(ctx context.Context, sandbox sandboxControlRef, serviceName string) error {
	// A suspended sandbox intentionally has zero app/coordinator replicas, so
	// its control Service has no endpoint to receive a DELETE. The replica
	// scale-down already removed every named process; retain the desired
	// cleanup as a no-op until resume, when normal reconciliation can configure
	// the service again.
	if sandbox.Suspended || sandbox.ServiceName == "" || sandbox.SecretName == "" || c.cfg.RuntimeConfig == nil {
		return nil
	}
	token, err := c.controlToken(ctx, sandbox)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	return c.proxyControl(ctx, sandbox, token, http.MethodDelete, "/service?name="+url.QueryEscape(serviceName), nil, nil)
}

func (c *Controller) controlToken(ctx context.Context, sandbox sandboxControlRef) (string, error) {
	secret, err := c.cfg.Runtime.Resource(developmentSecretGVR).Namespace(sandbox.SecretNamespace).Get(ctx, sandbox.SecretName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get sandbox control Secret: %w", err)
	}
	data, found, err := unstructured.NestedStringMap(secret.Object, "data")
	if err != nil || !found {
		return "", errors.New("sandbox control Secret has no data")
	}
	encoded := strings.TrimSpace(data["token"])
	if encoded == "" {
		return "", errors.New("sandbox control Secret has no token")
	}
	// Kubernetes Secret data is base64 in the unstructured representation.
	decoded, err := decodeBase64(encoded)
	if err != nil {
		return "", fmt.Errorf("decode sandbox control token: %w", err)
	}
	return decoded, nil
}

func (c *Controller) proxyControl(ctx context.Context, sandbox sandboxControlRef, token, method, endpoint string, requestBody, responseBody any) error {
	if c.cfg.RuntimeConfig == nil {
		return errors.New("runtime REST config is unavailable")
	}
	httpClient, err := rest.HTTPClientFor(c.cfg.RuntimeConfig)
	if err != nil {
		return fmt.Errorf("runtime HTTP client: %w", err)
	}
	proxyURL := strings.TrimRight(c.cfg.RuntimeConfig.Host, "/") + "/api/v1/namespaces/" + url.PathEscape(sandbox.ServiceNamespace) + "/services/" + url.PathEscape(sandbox.ServiceName) + ":" + strconv.Itoa(int(controlPort)) + "/proxy" + endpoint
	var body *strings.Reader
	if requestBody != nil {
		raw, err := json.Marshal(requestBody)
		if err != nil {
			return err
		}
		body = strings.NewReader(string(raw))
	} else {
		body = strings.NewReader("")
	}
	request, err := http.NewRequestWithContext(ctx, method, proxyURL, body)
	if err != nil {
		return err
	}
	request.Header.Set("X-Sandbox-Control-Token", token)
	if requestBody != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("sandbox control request %s: %w", endpoint, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("sandbox control request %s returned HTTP %d", endpoint, response.StatusCode)
	}
	if responseBody != nil {
		if err := json.NewDecoder(response.Body).Decode(responseBody); err != nil {
			return fmt.Errorf("decode sandbox control response: %w", err)
		}
	}
	return nil
}

func decodeBase64(value string) (string, error) {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

type developmentServiceNames struct {
	backendService string
	gateDeployment string
	gateService    string
	route          string
}

func developmentServiceRuntimeNames(name string) developmentServiceNames {
	return developmentServiceNames{
		backendService: developmentRuntimeName(name, "backend"),
		gateDeployment: developmentRuntimeName(name, "preview-gate"),
		gateService:    developmentRuntimeName(name, "preview-gate"),
		route:          developmentRuntimeName(name, "preview"),
	}
}

func developmentRuntimeName(base, suffix string) string {
	raw := strings.ToLower(strings.TrimSpace(base) + "-" + suffix)
	var b strings.Builder
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	raw = strings.Trim(b.String(), "-")
	if raw == "" {
		raw = "development-service"
	}
	if len(raw) <= 63 {
		return raw
	}
	digest := sha256.Sum256([]byte(raw))
	return strings.Trim(raw[:54], "-") + "-" + hex.EncodeToString(digest[:])[:8]
}

func developmentServiceLabels(service, sandbox string) map[string]string {
	return map[string]string{
		developmentServiceManagedLabel: developmentServiceManagedValue,
		developmentServiceNameLabel:    service,
		developmentSandboxNameLabel:    sandbox,
	}
}

// unstructuredLabels converts typed labels to the JSON-compatible map shape
// required by unstructured.Unstructured. Keeping this conversion at the
// object-construction boundary avoids dynamic clients (including the fake
// client used by tests) seeing a map[string]string nested in Object.
func unstructuredLabels(labels map[string]string) map[string]any {
	result := make(map[string]any, len(labels))
	for key, value := range labels {
		result[key] = value
	}
	return result
}

// accessProxyVisibility is the wire representation consumed by the existing
// access-proxy binary. The Infrastructure API uses title-cased values to
// match App Studio's provider contract, while the proxy's environment mode
// remains the lower-case private/public setting used by its config parser.
func accessProxyVisibility(visibility infrav1alpha1.DevelopmentServiceVisibility) string {
	if visibility == infrav1alpha1.DevelopmentServiceVisibilityPublic {
		return "public"
	}
	return "private"
}

func developmentServiceHost(name, tenant, baseDomain string) (string, error) {
	prefix := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range prefix {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	prefix = strings.Trim(b.String(), "-")
	if prefix == "" {
		prefix = "service"
	}
	if len(prefix) > 50 {
		digest := sha256.Sum256([]byte(prefix))
		prefix = strings.Trim(prefix[:41], "-") + "-" + hex.EncodeToString(digest[:])[:8]
	}
	return apps.Host(prefix, name, tenant, baseDomain)
}

func normalizedPublicScheme() string {
	scheme := strings.ToLower(strings.TrimSpace(os.Getenv("FAROS_ACCESS_PUBLIC_SCHEME")))
	if scheme != "http" && scheme != "https" {
		scheme = strings.ToLower(strings.TrimSpace(os.Getenv("FAROS_PUBLIC_SCHEME")))
	}
	if scheme != "http" && scheme != "https" {
		return "https"
	}
	return scheme
}

func publicPortSuffix() string {
	raw := strings.TrimSpace(os.Getenv("FAROS_APP_PUBLIC_PORT"))
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, ":") {
		raw = strings.TrimPrefix(raw, ":")
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 {
		return ""
	}
	return ":" + strconv.Itoa(port)
}

func gatewayName() string {
	if name := strings.TrimSpace(os.Getenv("FAROS_GATEWAY_NAME")); name != "" {
		return name
	}
	return backendkro.DefaultGatewayName
}

func gatewayNamespace() string {
	if namespace := strings.TrimSpace(os.Getenv("FAROS_GATEWAY_NAMESPACE")); namespace != "" {
		return namespace
	}
	return backendkro.DefaultGatewayNamespace
}

func httpRouteAccepted(route *unstructured.Unstructured, wantName, wantNamespace string) bool {
	if route == nil {
		return false
	}
	parents, _, _ := unstructured.NestedSlice(route.Object, "status", "parents")
	for _, raw := range parents {
		parent, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		ref, _ := parent["parentRef"].(map[string]any)
		group, _ := ref["group"].(string)
		kind, _ := ref["kind"].(string)
		name, _ := ref["name"].(string)
		namespace, _ := ref["namespace"].(string)
		if group != "gateway.networking.k8s.io" || kind != "Gateway" || name != wantName || namespace != wantNamespace {
			continue
		}
		conditions, _ := parent["conditions"].([]any)
		accepted, resolved := false, false
		for _, item := range conditions {
			condition, _ := item.(map[string]any)
			if condition["status"] != "True" {
				continue
			}
			observed, ok := generationValue(condition["observedGeneration"])
			if !ok || observed != route.GetGeneration() {
				continue
			}
			switch condition["type"] {
			case "Accepted":
				accepted = true
			case "ResolvedRefs":
				resolved = true
			}
		}
		if accepted && resolved {
			return true
		}
	}
	return false
}

func ensureRuntimeObject(ctx context.Context, runtimeClient dynamic.Interface, gvr schema.GroupVersionResource, namespace, name string, desired *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	resource := runtimeClient.Resource(gvr).Namespace(namespace)
	existing, err := resource.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return resource.Create(ctx, desired, metav1.CreateOptions{})
	}
	if err != nil {
		return nil, err
	}
	wantSpec, _, _ := unstructured.NestedMap(desired.Object, "spec")
	gotSpec, _, _ := unstructured.NestedMap(existing.Object, "spec")
	wantLabels := desired.GetLabels()
	gotLabels := existing.GetLabels()
	labelsCurrent := true
	for key, value := range wantLabels {
		if gotLabels[key] != value {
			labelsCurrent = false
			break
		}
	}
	if labelsCurrent && equality.Semantic.DeepEqual(wantSpec, gotSpec) {
		return existing, nil
	}
	if err := unstructured.SetNestedMap(existing.Object, runtime.DeepCopyJSON(wantSpec), "spec"); err != nil {
		return nil, err
	}
	if gotLabels == nil {
		gotLabels = map[string]string{}
	}
	for key, value := range wantLabels {
		gotLabels[key] = value
	}
	existing.SetLabels(gotLabels)
	return resource.Update(ctx, existing, metav1.UpdateOptions{})
}

func deleteRuntimeObject(ctx context.Context, runtimeClient dynamic.Interface, gvr schema.GroupVersionResource, namespace, name string) error {
	if runtimeClient == nil || namespace == "" || name == "" {
		return nil
	}
	err := runtimeClient.Resource(gvr).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (c *Controller) persistDevelopmentServiceStatus(ctx context.Context, tenantClient client.Client, service *infrav1alpha1.DevelopmentService, status infrav1alpha1.DevelopmentServiceStatus) error {
	if equality.Semantic.DeepEqual(service.Status, status) {
		return nil
	}
	copy := service.DeepCopy()
	copy.Status = status
	return tenantClient.Status().Update(ctx, copy)
}

func baseDevelopmentServiceStatus(service *infrav1alpha1.DevelopmentService) infrav1alpha1.DevelopmentServiceStatus {
	status := infrav1alpha1.DevelopmentServiceStatus{ObservedGeneration: service.Generation, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	// Conditions are reconstructed on every poll, but their transition time is
	// semantic state. Seed the previous conditions so unchanged observations do
	// not look like a transition to consumers watching the CR.
	status.Conditions = append([]metav1.Condition(nil), service.Status.Conditions...)
	return status
}

func developmentServiceErrorStatus(service *infrav1alpha1.DevelopmentService, reason, message string) infrav1alpha1.DevelopmentServiceStatus {
	status := baseDevelopmentServiceStatus(service)
	// Error status must not keep a previously routable URL, backend reference,
	// or process observation. RuntimeNamespace is intentionally also cleared so
	// consumers cannot mistake an old runtime for an active service.
	status.Host = ""
	status.URL = ""
	status.RuntimeNamespace = ""
	status.BackendServiceRef = nil
	status.Process = infrav1alpha1.DevelopmentServiceProcessStatus{}
	for _, typ := range []string{infrav1alpha1.DevelopmentServiceConditionSandboxReady, infrav1alpha1.DevelopmentServiceConditionProcessReady, infrav1alpha1.DevelopmentServiceConditionPortListening, infrav1alpha1.DevelopmentServiceConditionReachable, infrav1alpha1.DevelopmentServiceConditionRouteAccepted, infrav1alpha1.DevelopmentServiceConditionReady} {
		setDevelopmentServiceCondition(&status, typ, metav1.ConditionFalse, reason, message, service.Generation)
	}
	status.Ready = false
	return status
}

func developmentServiceSandboxStatus(service *infrav1alpha1.DevelopmentService, sandbox sandboxControlRef, reason, message string) infrav1alpha1.DevelopmentServiceStatus {
	return developmentServiceSandboxStatusWithURL(service, sandbox, "", "", reason, message)
}

func developmentServiceSandboxStatusWithURL(service *infrav1alpha1.DevelopmentService, sandbox sandboxControlRef, host, previewURL, reason, message string) infrav1alpha1.DevelopmentServiceStatus {
	status := baseDevelopmentServiceStatus(service)
	status.RuntimeNamespace = sandbox.RuntimeNamespace
	status.Host = host
	status.URL = previewURL
	setDevelopmentServiceCondition(&status, infrav1alpha1.DevelopmentServiceConditionSandboxReady, metav1.ConditionFalse, reason, message, service.Generation)
	for _, typ := range []string{infrav1alpha1.DevelopmentServiceConditionProcessReady, infrav1alpha1.DevelopmentServiceConditionPortListening, infrav1alpha1.DevelopmentServiceConditionReachable, infrav1alpha1.DevelopmentServiceConditionRouteAccepted, infrav1alpha1.DevelopmentServiceConditionReady} {
		setDevelopmentServiceCondition(&status, typ, metav1.ConditionFalse, reason, message, service.Generation)
	}
	return status
}

func developmentServiceDisabledStatus(service *infrav1alpha1.DevelopmentService, host, previewURL string, sandbox sandboxControlRef) infrav1alpha1.DevelopmentServiceStatus {
	status := developmentServiceSandboxStatusWithURL(service, sandbox, host, previewURL, "Disabled", "the development service is disabled")
	for _, typ := range []string{infrav1alpha1.DevelopmentServiceConditionSandboxReady, infrav1alpha1.DevelopmentServiceConditionProcessReady, infrav1alpha1.DevelopmentServiceConditionPortListening, infrav1alpha1.DevelopmentServiceConditionReachable, infrav1alpha1.DevelopmentServiceConditionRouteAccepted, infrav1alpha1.DevelopmentServiceConditionReady} {
		setDevelopmentServiceCondition(&status, typ, metav1.ConditionFalse, "Disabled", "the development service is disabled", service.Generation)
	}
	return status
}

func developmentServiceRuntimeStatus(service *infrav1alpha1.DevelopmentService, sandbox sandboxControlRef, host, previewURL string, names developmentServiceNames, process devAgentServiceStatus) infrav1alpha1.DevelopmentServiceStatus {
	status := baseDevelopmentServiceStatus(service)
	status.Host = host
	status.URL = previewURL
	status.RuntimeNamespace = sandbox.RuntimeNamespace
	status.BackendServiceRef = &infrav1alpha1.DevelopmentServiceObjectReference{Name: names.backendService, Namespace: sandbox.RuntimeNamespace}
	status.Process = infrav1alpha1.DevelopmentServiceProcessStatus{Phase: process.Phase, Running: process.Running, PortListening: process.PortListening, Reachable: process.Reachable, RestartCount: process.RestartCount, LastExitCode: process.LastExitCode, Message: process.Message}
	return status
}

func setDevelopmentServiceCondition(status *infrav1alpha1.DevelopmentServiceStatus, typ string, conditionStatus metav1.ConditionStatus, reason, message string, generation int64) {
	if status == nil {
		return
	}
	now := metav1.Now()
	for i := range status.Conditions {
		condition := &status.Conditions[i]
		if condition.Type != typ {
			continue
		}
		if condition.Status == conditionStatus && condition.Reason == reason && condition.Message == message {
			now = condition.LastTransitionTime
		}
		*condition = metav1.Condition{Type: typ, Status: conditionStatus, Reason: reason, Message: message, ObservedGeneration: generation, LastTransitionTime: now}
		return
	}
	status.Conditions = append(status.Conditions, metav1.Condition{Type: typ, Status: conditionStatus, Reason: reason, Message: message, ObservedGeneration: generation, LastTransitionTime: now})
}

func conditionStatus(status *infrav1alpha1.DevelopmentServiceStatus, typ string) metav1.ConditionStatus {
	if status == nil {
		return metav1.ConditionUnknown
	}
	for _, condition := range status.Conditions {
		if condition.Type == typ {
			return condition.Status
		}
	}
	return metav1.ConditionUnknown
}

func boolCondition(value bool) metav1.ConditionStatus {
	if value {
		return metav1.ConditionTrue
	}
	return metav1.ConditionFalse
}

func processReason(status devAgentServiceStatus) string {
	if status.Running {
		return "Running"
	}
	if status.Phase == processPhaseFailed {
		return "ProcessFailed"
	}
	return "ProcessStopped"
}

func processMessage(status devAgentServiceStatus) string {
	if status.Message != "" {
		return status.Message
	}
	if status.Running {
		return "the service process is running"
	}
	return "the service process is not running"
}

func containsFinalizer(service *infrav1alpha1.DevelopmentService, finalizer string) bool {
	if service == nil {
		return false
	}
	for _, value := range service.Finalizers {
		if value == finalizer {
			return true
		}
	}
	return false
}

func removeFinalizer(finalizers []string, target string) []string {
	result := finalizers[:0]
	for _, value := range finalizers {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}

func normalizedFromStoredService(service *infrav1alpha1.DevelopmentService) normalizedDevelopmentService {
	if service == nil {
		return normalizedDevelopmentService{}
	}
	normalized, _ := normalizeDevelopmentService(service)
	// Deletion must still clean the process when a newer invalid spec has
	// already been persisted. Keep only the immutable routing identity as a
	// cleanup fallback; normal reconciliation continues to reject that spec.
	if normalized.SandboxName == "" {
		normalized.SandboxName = strings.TrimSpace(service.Spec.SandboxRef.Name)
	}
	if normalized.SandboxUID == "" {
		normalized.SandboxUID = strings.TrimSpace(service.Spec.SandboxRef.UID)
	}
	return normalized
}
