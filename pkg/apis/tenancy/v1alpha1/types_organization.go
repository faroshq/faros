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
	// OwnersRef is the reference to the user that owns the organization
	OwnersRef []ObjectReference `json:"ownersRef,omitempty"`
}

type ObjectReference struct {
	// Kind of the referent.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#types-kinds
	// +optional
	Kind string `json:"kind,omitempty" protobuf:"bytes,1,opt,name=kind"`
	// Name of the referent.
	// More info: https://kubernetes.io/docs/concepts/overview/working-with-objects/names/#names
	// +optional
	Name string `json:"name,omitempty" protobuf:"bytes,3,opt,name=name"`
	// API version of the referent.
	// +optional
	APIVersion string `json:"apiVersion,omitempty" protobuf:"bytes,5,opt,name=apiVersion"`
	// Email if the email of the user
	// +optional
	Email string `json:"email,omitempty" protobuf:"bytes,5,opt,name=email"`
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

// IsOwner returns true if the user is an owner of the organization
func (o *Organization) IsOwner(user *User) bool {
	for _, owner := range o.Spec.OwnersRef {
		if owner.Name == user.Name {
			return true
		}
	}
	return false
}
