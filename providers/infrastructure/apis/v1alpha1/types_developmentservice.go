/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// DevelopmentService declares one user application process that runs inside
// a universal coding sandbox and may be exposed through the platform's
// authenticated preview gateway. It is intentionally separate from
// Instance: a sandbox can host several independently supervised services.
//
// +crd
// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories=faros,shortName=devsvc
// +kubebuilder:printcolumn:name="Sandbox",type=string,JSONPath=`.spec.sandboxRef.name`
// +kubebuilder:printcolumn:name="Port",type=integer,JSONPath=`.spec.endpoint.port`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.status.url`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type DevelopmentService struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DevelopmentServiceSpec   `json:"spec"`
	Status DevelopmentServiceStatus `json:"status,omitempty"`
}

// DevelopmentServiceList is the standard Kubernetes list wrapper.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type DevelopmentServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DevelopmentService `json:"items"`
}

// DevelopmentServiceSpec is the project-owned desired process and endpoint
// contract. The shape intentionally mirrors App Studio's environment model:
// project/environment/component identify ownership, while command/endpoint
// describe one independently supervised process. Infrastructure owns all
// Kubernetes runtime objects and status; App Studio only writes this intent.
type DevelopmentServiceSpec struct {
	// ProjectRef identifies the project that owns this service. The UID is
	// carried so a recreated project with the same name cannot inherit it.
	// +required
	ProjectRef DevelopmentServiceProjectReference `json:"projectRef"`

	// Environment identifies the project environment. DevelopmentService is
	// currently only supported for the development environment.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Environment string `json:"environment"`

	// ComponentRef optionally attributes the process to a project component.
	// It is not used as a Kubernetes selector and may be empty for an ad-hoc
	// listener created directly in the development environment.
	// +optional
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^$|^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	ComponentRef string `json:"componentRef,omitempty"`

	// Enabled controls whether the process and preview route are active while
	// retaining this resource's identity and status. A nil value has the same
	// meaning as true; a pointer distinguishes omission from an explicit false
	// before an apiserver default is applied.
	// +optional
	// +kubebuilder:default=true
	Enabled *bool `json:"enabled,omitempty"`

	// Command is an argv-only process command. The dev agent never evaluates it
	// through a shell. Shell pipelines can be represented by an explicit shell
	// argv when a user intentionally asks for one.
	// +required
	Command DevelopmentServiceCommand `json:"command"`

	// Endpoint is the network contract exposed by this service.
	// +required
	Endpoint DevelopmentServiceEndpoint `json:"endpoint"`

	// Exposure controls preview access. Private uses the existing authenticated
	// access proxy; public uses the same route with the proxy's explicit
	// passthrough mode and is admitted only after App Studio's confirmation.
	// +optional
	Exposure DevelopmentServiceExposure `json:"exposure,omitempty"`

	// RestartPolicy determines whether a stopped process is restarted by the
	// service supervisor.
	// +optional
	// +kubebuilder:default=Always
	// +kubebuilder:validation:Enum=Always;OnFailure;Never
	RestartPolicy DevelopmentServiceRestartPolicy `json:"restartPolicy,omitempty"`

	// ConnectionRefs reserves the project connection attachment point. The
	// Infrastructure controller records and preserves these references but
	// does not resolve them until the Connection API is available.
	// +optional
	// +kubebuilder:validation:MaxItems=32
	ConnectionRefs []string `json:"connectionRefs,omitempty"`

	// SandboxRef identifies the project-scoped universal coding sandbox. UID
	// prevents a deleted-and-recreated sandbox with the same name from being
	// accidentally reused.
	// +required
	SandboxRef DevelopmentServiceSandboxReference `json:"sandboxRef"`
}

// DevelopmentServiceProjectReference identifies the owning App Studio
// project. It is intentionally a reference rather than a cross-provider Go
// type so the standalone providers retain independent API modules.
type DevelopmentServiceProjectReference struct {
	// Name is the Project name in the tenant workspace.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// UID is the Project UID observed at binding time.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	UID string `json:"uid"`
}

// DevelopmentServiceSandboxReference identifies the Instance providing the
// workspace and dev-agent control plane.
type DevelopmentServiceSandboxReference struct {
	// Name is the sandbox Instance name in the tenant workspace.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// UID is the sandbox Instance UID observed at binding time.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	UID string `json:"uid"`
}

// DevelopmentServiceCommand describes a process without embedding shell
// semantics in the Kubernetes API.
type DevelopmentServiceCommand struct {
	// Argv contains the executable and arguments.
	// +required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=32
	Argv []string `json:"argv"`

	// WorkingDirectory is relative to the sandbox workspace root. Empty uses
	// the workspace root.
	// +optional
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:Pattern=`^$|^[^/\\].*$`
	WorkingDirectory string `json:"workingDirectory,omitempty"`
}

// DevelopmentServiceEndpoint describes the listener inside the sandbox.
type DevelopmentServiceEndpoint struct {
	// Protocol is currently HTTP. HTTP servers may upgrade connections to
	// WebSocket through the same Gateway route.
	// +optional
	// +kubebuilder:default=HTTP
	// +kubebuilder:validation:Enum=HTTP
	Protocol string `json:"protocol,omitempty"`

	// Port is the process port inside the sandbox Pod.
	// +required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`

	// HealthPath is an optional HTTP path used for a readiness probe. Empty
	// means the process port is probed only.
	// +optional
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:Pattern=`^$|^/.*`
	HealthPath string `json:"healthPath,omitempty"`
}

// DevelopmentServiceExposure controls preview access.
type DevelopmentServiceExposure struct {
	// Visibility is private by default. Public is an explicit opt-in and is
	// enforced by the existing access-proxy mode rather than a second route.
	// +optional
	// +kubebuilder:default=Private
	// +kubebuilder:validation:Enum=Private;Public
	Visibility DevelopmentServiceVisibility `json:"visibility,omitempty"`
}

// DevelopmentServiceVisibility controls the route access mode.
type DevelopmentServiceVisibility string

const (
	DevelopmentServiceVisibilityPrivate DevelopmentServiceVisibility = "Private"
	DevelopmentServiceVisibilityPublic  DevelopmentServiceVisibility = "Public"
)

// DevelopmentServiceRestartPolicy controls process restart behavior.
type DevelopmentServiceRestartPolicy string

const (
	DevelopmentServiceRestartAlways    DevelopmentServiceRestartPolicy = "Always"
	DevelopmentServiceRestartOnFailure DevelopmentServiceRestartPolicy = "OnFailure"
	DevelopmentServiceRestartNever     DevelopmentServiceRestartPolicy = "Never"
)

// DevelopmentServiceStatus is observed runtime state. URL/Host describe the
// stable configured endpoint; Ready is true only after the sandbox process,
// Service, and Gateway route are all observed healthy.
type DevelopmentServiceStatus struct {
	// ObservedGeneration identifies the latest spec generation processed by the
	// Infrastructure controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Host is the stable per-service hostname allocated by Infrastructure.
	// +optional
	Host string `json:"host,omitempty"`

	// URL is the HTTPS preview URL for Host. It may be present while Ready is
	// false; callers must use conditions to determine availability.
	// +optional
	URL string `json:"url,omitempty"`

	// Ready is a convenience projection of the Ready condition. Consumers
	// should still inspect Conditions for the reason a service is unavailable.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// ObservedAt is the provider time at which this status was collected.
	// +optional
	ObservedAt string `json:"observedAt,omitempty"`

	// RuntimeNamespace is the sandbox workload namespace on the data-plane
	// cluster.
	// +optional
	RuntimeNamespace string `json:"runtimeNamespace,omitempty"`

	// BackendServiceRef identifies the provider-owned Service forwarding to the
	// sandbox Pod.
	// +optional
	BackendServiceRef *DevelopmentServiceObjectReference `json:"backendServiceRef,omitempty"`

	// Process reports the latest observed dev-agent process state.
	// +optional
	Process DevelopmentServiceProcessStatus `json:"process,omitempty"`

	// Conditions include SandboxReady, ProcessReady, PortListening,
	// RouteAccepted, and aggregate Ready. Every condition carries the
	// DevelopmentService generation it describes.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// DevelopmentServiceObjectReference points at a runtime object without
// embedding arbitrary object data in status.
type DevelopmentServiceObjectReference struct {
	// Name is the object name.
	// +required
	Name string `json:"name"`
	// Namespace is the object namespace.
	// +required
	Namespace string `json:"namespace"`
}

// DevelopmentServiceProcessStatus is the dev-agent's process observation.
type DevelopmentServiceProcessStatus struct {
	// Phase is Running, Stopped, or Failed.
	// +optional
	Phase string `json:"phase,omitempty"`
	// Running reports whether the process currently exists.
	// +optional
	Running bool `json:"running,omitempty"`
	// PortListening reports that the process currently has a listener on the
	// declared port. It is deliberately distinct from route reachability.
	// +optional
	PortListening bool `json:"portListening,omitempty"`
	// Reachable reports a successful loopback TCP probe from the sandbox.
	// +optional
	Reachable bool `json:"reachable,omitempty"`
	// RestartCount changes whenever the process is restarted.
	// +optional
	RestartCount int64 `json:"restartCount,omitempty"`
	// LastExitCode is the most recent process exit code, when known.
	// +optional
	LastExitCode *int32 `json:"lastExitCode,omitempty"`
	// Message is bounded human-readable process detail.
	// +optional
	// +kubebuilder:validation:MaxLength=1024
	Message string `json:"message,omitempty"`
}

const (
	// DevelopmentServiceFinalizer protects runtime Services, access gates, and
	// routes that live on the data-plane cluster.
	DevelopmentServiceFinalizer = "infrastructure.faros.sh/development-service"

	// DevelopmentServicesResource is the stable resource segment used by the
	// authenticated data-plane logs subresource.
	DevelopmentServicesResource = "developmentservices"

	DevelopmentServiceConditionSandboxReady  = "SandboxReady"
	DevelopmentServiceConditionProcessReady  = "ProcessReady"
	DevelopmentServiceConditionPortListening = "PortListening"
	DevelopmentServiceConditionRouteAccepted = "RouteAccepted"
	DevelopmentServiceConditionReachable     = "Reachable"
	DevelopmentServiceConditionReady         = "Ready"
)
