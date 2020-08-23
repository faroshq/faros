package v1alpha1

// Copyright (c) Faros.sh.
// Licensed under the Apache License 2.0.

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/faroshq/faros/pkg/util/status"
)

const (
	SingletonObjectName string               = "cluster"
	Healthy             status.ConditionType = "Healthy"
)

func AllConditionTypes() []status.ConditionType {
	return []status.ConditionType{Healthy}
}

// ConfigSpec defines the desired state of the Config
type ConfigSpec struct {
	// Name is the  name of the cluster
	Name     string `json:"name,omitempty"`
	Location string `json:"location,omitempty"`
}

// ConfigStatus defines the observed state of Faros Operator
type ConfigStatus struct {
	OperatorVersion string            `json:"operatorVersion,omitempty"`
	Conditions      status.Conditions `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +genclient
// +genclient:nonNamespaced
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// Config is the Schema for the config API
type Config struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ConfigSpec   `json:"spec,omitempty"`
	Status ConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ConfigList contains a list of Configs
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Config `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Config{}, &ConfigList{})
}
