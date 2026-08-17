/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +crd
// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories=faros,shortName=gsync
// +kubebuilder:printcolumn:name="Repository",type=string,JSONPath=`.spec.repositoryRef`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Revision",type=string,JSONPath=`.status.appliedRevision`
type RepositorySync struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              RepositorySyncSpec   `json:"spec"`
	Status            RepositorySyncStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type RepositorySyncList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RepositorySync `json:"items"`
}

type RepositorySyncSpec struct {
	// +required
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

// +kubebuilder:validation:Enum=Pending;Reconciling;Ready;Failed
type RepositorySyncPhase string

const (
	RepositorySyncPhasePending     RepositorySyncPhase = "Pending"
	RepositorySyncPhaseReconciling RepositorySyncPhase = "Reconciling"
	RepositorySyncPhaseReady       RepositorySyncPhase = "Ready"
	RepositorySyncPhaseFailed      RepositorySyncPhase = "Failed"
)

type RepositorySyncInventoryItem struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Resource   string `json:"resource"`
	Name       string `json:"name"`
	UID        string `json:"uid,omitempty"`
}

type RepositorySyncStatus struct {
	ObservedGeneration int64                         `json:"observedGeneration,omitempty"`
	Phase              RepositorySyncPhase           `json:"phase,omitempty"`
	ObservedRevision   string                        `json:"observedRevision,omitempty"`
	AppliedRevision    string                        `json:"appliedRevision,omitempty"`
	Inventory          []RepositorySyncInventoryItem `json:"inventory,omitempty"`
	Conditions         []metav1.Condition            `json:"conditions,omitempty"`
}
