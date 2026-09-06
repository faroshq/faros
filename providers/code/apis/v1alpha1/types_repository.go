/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Repository is the desired state for one git repository on the host the
// referenced Connection authenticates. The RepositoryController creates (and,
// on delete, removes) the repo via the sub-provider backend and records its
// URLs + host-side id in status.
//
// +crd
// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories=faros,shortName=grepo
// +kubebuilder:printcolumn:name="Connection",type=string,JSONPath=`.spec.connectionRef`
// +kubebuilder:printcolumn:name="Repo",type=string,JSONPath=`.spec.name`
// +kubebuilder:printcolumn:name="Visibility",type=string,JSONPath=`.spec.visibility`
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.status.htmlURL`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type Repository struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RepositorySpec   `json:"spec"`
	Status RepositoryStatus `json:"status,omitempty"`
}

// RepositoryList is the standard k8s list wrapper.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type RepositoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Repository `json:"items"`
}

// RepositoryVisibility controls who can see the repository.
//
// +kubebuilder:validation:Enum=private;public;internal
type RepositoryVisibility string

const (
	VisibilityPrivate  RepositoryVisibility = "private"
	VisibilityPublic   RepositoryVisibility = "public"
	VisibilityInternal RepositoryVisibility = "internal"
)

// RepositorySpec is the desired state.
//
// The object-level rule keeps optional owner presence immutable as well as its
// value. A field-level oldSelf rule is not evaluated for an omitted optional
// field by every Kubernetes version, so it cannot reliably reject an
// absent-to-present transition after handoff.
// +kubebuilder:validation:XValidation:rule="has(self.owner) == has(oldSelf.owner) && (!has(self.owner) || self.owner == oldSelf.owner)",message="spec.owner is immutable"
type RepositorySpec struct {
	// ConnectionRef names the Connection (same workspace) whose credential and
	// owner this repository is created under. After handoff it may be changed
	// only to rotate credentials for the same provider, endpoint, and effective
	// owner; retargeting a repository requires delete and recreate.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	ConnectionRef string `json:"connectionRef"`

	// Name is the repository name on the git host (the path segment after
	// the owner). DNS-ish; the host enforces its own rules.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec.name is immutable"
	Name string `json:"name"`

	// Owner overrides the Connection's owner for this single repository
	// (e.g. to create under a different org the same credential controls).
	// Empty inherits Connection.spec.owner.
	// +optional
	// +kubebuilder:validation:MaxLength=100
	Owner string `json:"owner,omitempty"`

	// Visibility defaults to private when empty.
	// +optional
	Visibility RepositoryVisibility `json:"visibility,omitempty"`

	// Description is set as the repository description on the host.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	Description string `json:"description,omitempty"`

	// DefaultBranch names the initial default branch. Empty uses the host
	// default (e.g. "main").
	// +optional
	// +kubebuilder:validation:MaxLength=255
	DefaultBranch string `json:"defaultBranch,omitempty"`

	// AutoInit creates an initial commit (README) so the repository has a
	// default branch immediately. Required if DeployKeys/clones must
	// succeed right after creation.
	// +optional
	AutoInit bool `json:"autoInit,omitempty"`
}

// RepositoryStatus is the observed state.
type RepositoryStatus struct {
	// ObservedGeneration mirrors metadata.generation last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Identity records the external repository identity that was handed to the
	// backend. It is populated before the first ensure call so a later mutable
	// update cannot redirect this object to another owner, host, or repository,
	// and deletion can continue to target the original object.
	// +optional
	Identity *RepositoryIdentity `json:"identity,omitempty"`

	// RepoID is the host-side numeric/opaque id of the repository.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	RepoID string `json:"repoID,omitempty"`

	// HTMLURL is the browser URL of the repository.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	HTMLURL string `json:"htmlURL,omitempty"`

	// CloneURL is the HTTPS clone URL.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	CloneURL string `json:"cloneURL,omitempty"`

	// SSHURL is the SSH clone URL (used together with a DeployKey).
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	SSHURL string `json:"sshURL,omitempty"`

	// Conditions follows the standard Kubernetes conditions pattern.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// RepositoryIdentity is the stable external identity of a managed
// repository. Owner is the effective owner after applying spec.owner over the
// referenced Connection's owner. Provider and BaseURL identify the remote
// host; ConnectionRef identifies the credential anchor used to operate it.
type RepositoryIdentity struct {
	// ConnectionRef is the Connection currently supplying the credential. It may
	// rotate to another connection only when the controller proves the external
	// provider, endpoint, and effective owner are unchanged.
	ConnectionRef string `json:"connectionRef"`
	// Provider is the registered git backend used at handoff.
	Provider GitProvider `json:"provider"`
	// BaseURL identifies a GitHub Enterprise or other self-hosted endpoint.
	// Empty means the provider's public endpoint.
	// +optional
	BaseURL string `json:"baseURL,omitempty"`
	// Owner is the effective account or organization containing the repository.
	Owner string `json:"owner"`
	// SpecOwner preserves the raw spec.owner value so an older API server or a
	// fake client cannot turn an inherited owner into a different explicit
	// ownership contract without being rejected by the controller.
	// +optional
	SpecOwner string `json:"specOwner,omitempty"`
	// Name is the repository name on the host.
	Name string `json:"name"`
}

// FinalizerRepository is added by the RepositoryController so the host-side
// repository is deleted before the CR disappears.
const FinalizerRepository = "repositories.code.faros.sh/finalizer"
