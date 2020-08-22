package v1alpha1

// Copyright (c) Faros.sh.
// Licensed under the Apache License 2.0.

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/faroshq/faros/pkg/util/status"
)

const (
	SingletonClusterName                      = "cluster"
	InternetReachable    status.ConditionType = "InternetReachable"
)

func AllConditionTypes() []status.ConditionType {
	return []status.ConditionType{InternetReachable}
}

type InternetCheckerSpec struct {
	URLs []string `json:"urls,omitempty"`
}

// ClusterSpec defines the desired state of the Cluster
type ClusterSpec struct {
	// Name is the  name of the cluster
	Name            string              `json:"name,omitempty"`
	Location        string              `json:"location,omitempty"`
	InternetChecker InternetCheckerSpec `json:"internetChecker,omitempty"`
}

// ClusterStatus defines the observed state of Cluster
type ClusterStatus struct {
	OperatorVersion string            `json:"operatorVersion,omitempty"`
	Conditions      status.Conditions `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +genclient
// +genclient:nonNamespaced
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// Cluster is the Schema for the clusters API
type Cluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterSpec   `json:"spec,omitempty"`
	Status ClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterList contains a list of Cluster
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Cluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Cluster{}, &ClusterList{})
}
