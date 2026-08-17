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
// +kubebuilder:resource:scope=Cluster,categories=faros,shortName=gcr
// +kubebuilder:printcolumn:name="Repository",type=string,JSONPath=`.spec.repositoryRef`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.status.url`
type ChangeRequest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ChangeRequestSpec   `json:"spec"`
	Status            ChangeRequestStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ChangeRequestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ChangeRequest `json:"items"`
}

type ChangeRequestSpec struct {
	// +required
	// +kubebuilder:validation:MinLength=1
	RepositoryRef string `json:"repositoryRef"`
	// +required
	// +kubebuilder:validation:MinLength=1
	BaseBranch string `json:"baseBranch"`
	// +required
	// +kubebuilder:validation:MinLength=1
	HeadBranch string `json:"headBranch"`
	// +required
	// +kubebuilder:validation:MinLength=1
	Title string `json:"title"`
	// +optional
	Body string `json:"body,omitempty"`
	// +optional
	MergePolicy ChangeRequestMergePolicy `json:"mergePolicy,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=0
	RequiredApprovals int32 `json:"requiredApprovals,omitempty"`
}

// +kubebuilder:validation:Enum=Manual;AfterApproval
type ChangeRequestMergePolicy string

const (
	ChangeRequestMergePolicyManual        ChangeRequestMergePolicy = "Manual"
	ChangeRequestMergePolicyAfterApproval ChangeRequestMergePolicy = "AfterApproval"
)

// +kubebuilder:validation:Enum=Pending;Open;Approved;Merged;Closed;Failed
type ChangeRequestPhase string

const (
	ChangeRequestPhasePending  ChangeRequestPhase = "Pending"
	ChangeRequestPhaseOpen     ChangeRequestPhase = "Open"
	ChangeRequestPhaseApproved ChangeRequestPhase = "Approved"
	ChangeRequestPhaseMerged   ChangeRequestPhase = "Merged"
	ChangeRequestPhaseClosed   ChangeRequestPhase = "Closed"
	ChangeRequestPhaseFailed   ChangeRequestPhase = "Failed"
)

type ChangeRequestStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Phase              ChangeRequestPhase `json:"phase,omitempty"`
	Number             int64              `json:"number,omitempty"`
	URL                string             `json:"url,omitempty"`
	HeadSHA            string             `json:"headSHA,omitempty"`
	Approvals          int32              `json:"approvals,omitempty"`
	MergeSHA           string             `json:"mergeSHA,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
}
