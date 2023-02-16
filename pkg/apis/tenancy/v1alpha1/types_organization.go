package v1alpha1

import (
	conditionsv1alpha1 "github.com/kcp-dev/kcp/pkg/apis/third_party/conditions/apis/conditions/v1alpha1"
	"github.com/kcp-dev/kcp/pkg/apis/third_party/conditions/util/conditions"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +crd
// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=organizations,scope=Cluster
// +kubebuilder:object:root=true

// Organization is the Schema for the Organization API
type Organization struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OrganizationSpec   `json:"spec,omitempty"`
	Status OrganizationStatus `json:"status,omitempty"`
}

// OrganizationSpec defines the desired state of organization
type OrganizationSpec struct {
	// Description is a user readable description of the workspace
	Description string `json:"description,omitempty"`
}

// OrganizationStatus defines the observed state of organization
type OrganizationStatus struct {
	// Current processing state of the Organization.
	// +optional
	Conditions conditionsv1alpha1.Conditions `json:"conditions,omitempty"`

	// WorkspaceURL is the URL of the workspace
	WorkspaceURL string `json:"workspaceURL,omitempty"`
}

func (in *Organization) SetConditions(c conditionsv1alpha1.Conditions) {
	in.Status.Conditions = c
}

func (in *Organization) GetConditions() conditionsv1alpha1.Conditions {
	return in.Status.Conditions
}

var _ conditions.Getter = &Organization{}
var _ conditions.Setter = &Organization{}

// OrganizationList contains a list of Organizations
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type OrganizationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Organization `json:"items"`
}
