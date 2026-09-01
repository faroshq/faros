/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// Connection binds one provided Instance interface to one consuming Instance
// or DevelopmentService. It is a KRM intent object: the controller copies only
// allowlisted runtime Secret keys and status never contains credential values.
//
// +crd
// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories=faros,shortName=conn
// +kubebuilder:printcolumn:name="Source",type=string,JSONPath=`.spec.source.instanceRef.name`
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.spec.target.name`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type Connection struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ConnectionSpec   `json:"spec"`
	Status ConnectionStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ConnectionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Connection `json:"items"`
}

// ConnectionSpec is immutable because changing either identity or mapping in
// place can momentarily deliver credentials to the wrong workload. Replace the
// object to change a binding.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="connection spec is immutable"
type ConnectionSpec struct {
	// +required
	Source ConnectionSource `json:"source"`
	// +required
	Target ConnectionTarget `json:"target"`
	// Empty selects all mappings declared by the target interface.
	// +optional
	// +kubebuilder:validation:MaxItems=32
	Mappings []ConnectionMapping `json:"mappings,omitempty"`
}

type ConnectionSource struct {
	// +required
	InstanceRef ConnectionObjectReference `json:"instanceRef"`
	// +required
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9-]*$`
	Interface string `json:"interface"`
}

type ConnectionTarget struct {
	// +required
	// +kubebuilder:validation:Enum=Instance;DevelopmentService
	Kind string `json:"kind"`
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Name string `json:"name"`
	// UID is mandatory: a same-name recreation must never inherit credentials.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	UID string `json:"uid"`
	// +required
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9-]*$`
	Interface string `json:"interface"`
}

type ConnectionObjectReference struct {
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Name string `json:"name"`
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	UID string `json:"uid"`
}

type ConnectionMapping struct {
	// +required
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9._-]+$`
	SourceKey string `json:"sourceKey"`
	// +required
	// +kubebuilder:validation:Pattern=`^[A-Za-z_][A-Za-z0-9_]*$`
	TargetKey string `json:"targetKey"`
}

type ConnectionStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// Revision is derived from non-secret object versions and mapping metadata;
	// it is safe to expose and changes whenever delivered credentials rotate.
	// +optional
	Revision string `json:"revision,omitempty"`
	// +optional
	ManagedSecretRef *ConnectionManagedSecretReference `json:"managedSecretRef,omitempty"`
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

type ConnectionManagedSecretReference struct {
	Name                  string `json:"name"`
	Namespace             string `json:"namespace"`
	TargetRuntimeIdentity string `json:"targetRuntimeIdentity"`
}

const (
	ConnectionFinalizer      = "infrastructure.faros.sh/connection"
	ConnectionConditionReady = "Ready"
	ConnectionTargetInstance = "Instance"
	ConnectionTargetService  = "DevelopmentService"
)
