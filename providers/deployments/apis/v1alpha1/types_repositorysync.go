// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// RepositorySync projects a bounded, reviewed desired-state tree from a Code
// repository into this workspace. The objects in that tree may belong to any
// API available in the workspace; their owning providers remain responsible
// for runtime reconciliation and readiness.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,categories=faros,shortName=gsync
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Repository",type=string,JSONPath=`.spec.repositoryRef`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Revision",type=string,JSONPath=`.status.appliedRevision`
type RepositorySync struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              RepositorySyncSpec   `json:"spec"`
	Status            RepositorySyncStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type RepositorySyncList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RepositorySync `json:"items"`
}

type RepositorySyncSpec struct {
	// +kubebuilder:validation:MinLength=1
	RepositoryRef string `json:"repositoryRef"`
	// +optional
	Ref string `json:"ref,omitempty"`
	// +optional
	Path string `json:"path,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=10
	// +kubebuilder:validation:Maximum=3600
	IntervalSeconds int32 `json:"intervalSeconds,omitempty"`
	// +optional
	Prune bool `json:"prune,omitempty"`
}

// +kubebuilder:validation:Enum=Pending;Reconciling;AwaitingAuthorization;Synced;Failed
type RepositorySyncPhase string

const (
	RepositorySyncPhasePending               RepositorySyncPhase = "Pending"
	RepositorySyncPhaseReconciling           RepositorySyncPhase = "Reconciling"
	RepositorySyncPhaseAwaitingAuthorization RepositorySyncPhase = "AwaitingAuthorization"
	RepositorySyncPhaseSynced                RepositorySyncPhase = "Synced"
	RepositorySyncPhaseFailed                RepositorySyncPhase = "Failed"
)

type RepositorySyncInventoryItem struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Resource   string `json:"resource"`
	// +optional
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
	// +optional
	UID string `json:"uid,omitempty"`
	// +optional
	SourcePath string `json:"sourcePath,omitempty"`
}

// +kubebuilder:validation:Enum=Authorized;AwaitingAuthorization;Unavailable;Conflict
type RepositorySyncTargetRequirementState string

const (
	RepositorySyncTargetAuthorized            RepositorySyncTargetRequirementState = "Authorized"
	RepositorySyncTargetAwaitingAuthorization RepositorySyncTargetRequirementState = "AwaitingAuthorization"
	RepositorySyncTargetUnavailable           RepositorySyncTargetRequirementState = "Unavailable"
	RepositorySyncTargetConflict              RepositorySyncTargetRequirementState = "Conflict"
)

type RepositorySyncTargetClaim struct {
	// +optional
	Group    string `json:"group,omitempty"`
	Resource string `json:"resource"`
	// +optional
	Verbs []string `json:"verbs,omitempty"`
}

// RepositorySyncTargetRequirement reports the workspace capability needed by
// one desired object. It describes apply authorization only; it deliberately
// does not project the target provider's runtime readiness.
type RepositorySyncTargetRequirement struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Resource   string `json:"resource"`
	// +optional
	Namespace string                               `json:"namespace,omitempty"`
	State     RepositorySyncTargetRequirementState `json:"state"`
	// +optional
	Message string `json:"message,omitempty"`
	// +optional
	Claim *RepositorySyncTargetClaim `json:"claim,omitempty"`
}

type RepositorySyncStatus struct {
	ObservedGeneration int64                             `json:"observedGeneration,omitempty"`
	Phase              RepositorySyncPhase               `json:"phase,omitempty"`
	ObservedRevision   string                            `json:"observedRevision,omitempty"`
	AppliedRevision    string                            `json:"appliedRevision,omitempty"`
	Inventory          []RepositorySyncInventoryItem     `json:"inventory,omitempty"`
	TargetRequirements []RepositorySyncTargetRequirement `json:"targetRequirements,omitempty"`
	Conditions         []metav1.Condition                `json:"conditions,omitempty"`
}
