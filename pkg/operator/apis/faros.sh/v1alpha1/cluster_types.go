package v1alpha1

// Copyright (c) Faros.sh.
// Licensed under the Apache License 2.0.

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/faroshq/faros/pkg/util/status"
)

// ClusterSpec defines the desired state of the Config
type ClusterSpec struct {
	// Name is the  name of the cluster
	Name             string                 `json:"name,omitempty"`
	Location         string                 `json:"location,omitempty"`
	KubeConfigSecret corev1.SecretReference `json:"kubeConfigSecret,omitempty"`
}

// ClusterStatus defines the observed state of Faros Operator
type ClusterStatus struct {
	WorkerUID       types.UID         `json:"workerUID,omitempty"`
	OperatorVersion string            `json:"operatorVersion,omitempty"`
	Conditions      status.Conditions `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +genclient
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// Config is the Schema for the config API
type Cluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterSpec   `json:"spec,omitempty"`
	Status ClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterList contains a list of Clusters
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Cluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Cluster{}, &ClusterList{})
}
