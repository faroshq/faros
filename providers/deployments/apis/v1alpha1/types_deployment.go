// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const DeploymentClassKRODirect = "kro-direct"

type DeploymentMode string

const (
	DeploymentModeDevelopment DeploymentMode = "development"
	DeploymentModeProduction  DeploymentMode = "production"
)

type DeploymentDeletionPolicy string

const (
	DeploymentDeletionPolicyRetain DeploymentDeletionPolicy = "Retain"
	DeploymentDeletionPolicyDelete DeploymentDeletionPolicy = "Delete"
)

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=dep,categories=faros
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Release",type=string,JSONPath=`.status.activeReleaseRef`
type Deployment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              DeploymentSpec   `json:"spec"`
	Status            DeploymentStatus `json:"status,omitempty"`
}

type DeploymentSpec struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	ReleaseRef string `json:"releaseRef"`
	// +kubebuilder:default=kro-direct
	// +kubebuilder:validation:Enum=kro-direct
	ClassName string `json:"className,omitempty"`
	// +kubebuilder:default=production
	// +kubebuilder:validation:Enum=development;production
	Mode DeploymentMode `json:"mode,omitempty"`
	// +kubebuilder:default=Retain
	// +kubebuilder:validation:Enum=Retain;Delete
	DeletionPolicy DeploymentDeletionPolicy `json:"deletionPolicy,omitempty"`
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:XPreserveUnknownFields
	Configuration *runtime.RawExtension `json:"configuration,omitempty"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	RolloutID string `json:"rolloutID"`
}

type DeploymentStatus struct {
	ObservedGeneration       int64              `json:"observedGeneration,omitempty"`
	Phase                    string             `json:"phase,omitempty"`
	Conditions               []metav1.Condition `json:"conditions,omitempty"`
	ActiveReleaseRef         string             `json:"activeReleaseRef,omitempty"`
	LastSuccessfulReleaseRef string             `json:"lastSuccessfulReleaseRef,omitempty"`
	ObservedRolloutID        string             `json:"observedRolloutID,omitempty"`
	URL                      string             `json:"url,omitempty"`
	Outputs                  map[string]string  `json:"outputs,omitempty"`
	BackendRef               *BackendReference  `json:"backendRef,omitempty"`
}

type BackendReference struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Resource   string `json:"resource"`
	Name       string `json:"name"`
	UID        string `json:"uid,omitempty"`
}

// +kubebuilder:object:root=true
type DeploymentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Deployment `json:"items"`
}
