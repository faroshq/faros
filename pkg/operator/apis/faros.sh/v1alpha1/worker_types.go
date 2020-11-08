package v1alpha1

// Copyright (c) Faros.sh.
// Licensed under the Apache License 2.0.

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/faroshq/faros/pkg/util/status"
)

// WorkerSpec defines the desired state of the Config
type WorkerSpec struct{}

// WorkerStatus defines the observed state of Faros Operator
type WorkerStatus struct {
	Clusters          []types.UID       `json:"clusters,omitempty"`
	LastHeartbeatTime *metav1.Time      `json:"lastHeartbeatTime"`
	Conditions        status.Conditions `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +genclient
// +genclient:nonNamespaced
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// Config is the Schema for the config API
type Worker struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   WorkerSpec   `json:"spec,omitempty"`
	Status WorkerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WorkerList contains a list of Workers
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type WorkerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Worker `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Worker{}, &WorkerList{})
}
