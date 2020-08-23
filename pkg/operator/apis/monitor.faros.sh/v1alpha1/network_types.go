package v1alpha1

// Copyright (c) Faros.sh.
// Licensed under the Apache License 2.0.

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/faroshq/faros/pkg/util/status"
)

const (
	SingletonObjectName string               = "cluster"
	InternetReachable   status.ConditionType = "InternetReachable"
)

func AllConditionTypes() []status.ConditionType {
	return []status.ConditionType{InternetReachable}
}

type InternetCheckerSpec struct {
	URLs []string `json:"urls,omitempty"`
}

// NetworkSpec defines the desired state of the Network
type NetworkSpec struct {
	InternetChecker InternetCheckerSpec `json:"internetChecker,omitempty"`
}

// ClusterStatus defines the observed state of Cluster
type NetworkStatus struct {
	Conditions status.Conditions `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +genclient
// +genclient:nonNamespaced
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// Network is the Schema for the Network API
type Network struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NetworkSpec   `json:"spec,omitempty"`
	Status NetworkStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NetworkList contains a list of Networks
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type NetworkList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Network `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Network{}, &NetworkList{})
}
