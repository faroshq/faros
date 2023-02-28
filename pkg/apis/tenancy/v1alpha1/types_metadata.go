package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +crd
// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=metadata,scope=Cluster
// +kubebuilder:object:root=true

// Metadata is the Schema for the Metadata API
type Metadata struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec MetadataSpec `json:"spec,omitempty"`
}

// MetadataSpec defines the desired state of Metadata
type MetadataSpec struct {
	AccessToken         string `json:"accessToken,omitempty"`
	RefreshToken        string `json:"refreshToken,omitempty"`
	CurrentOrganization string `json:"currentOrganization,omitempty"`
	ExpiresAt           int    `json:"expiresAt,omitempty"`
}

// MetadataList contains a list of Metadata
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type MetadataList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Metadata `json:"items"`
}
