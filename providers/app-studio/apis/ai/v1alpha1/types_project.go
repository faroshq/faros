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
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	// ProjectPhaseReady marks a Project that is ready for portal use.
	ProjectPhaseReady = "Ready"

	// ProjectMessageRoleUser is a message authored by the user.
	ProjectMessageRoleUser = "user"
	// ProjectMessageRoleAssistant is a message authored by the assistant.
	ProjectMessageRoleAssistant = "assistant"
)

// +genclient
// +genclient:nonNamespaced
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=projects,singular=project,scope=Cluster,shortName=proj
// +kubebuilder:printcolumn:name="DisplayName",type=string,JSONPath=".spec.displayName"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Updated",type=date,JSONPath=".status.updatedAt"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Project is a persistent AI workspace scoped to a Faros child workspace.
type Project struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProjectSpec   `json:"spec,omitempty"`
	Status ProjectStatus `json:"status,omitempty"`
}

// ProjectSpec defines user-authored Project state.
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.delivery) ? (!has(self.delivery) || (self.delivery.development.mode == 'Direct' && self.delivery.production.mode == 'Direct')) : self.delivery == oldSelf.delivery",message="delivery policy is immutable; use a delivery migration workflow"
type ProjectSpec struct {
	// DisplayName is the human-readable project title.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	DisplayName string `json:"displayName"`

	// Description is a short project summary.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	Description string `json:"description,omitempty"`

	// Repository records the Code provider repository backing this Project.
	// +optional
	Repository *ProjectRepositoryBinding `json:"repository,omitempty"`

	// Delivery declares the desired-state writer independently for development
	// and production. Empty means Direct for both environments for projects
	// created before this field existed. The policy is selected at creation time
	// and is immutable; moving either environment between writers requires a
	// future migration workflow that can transfer ownership without racing
	// reconciliation.
	// +optional
	Delivery *ProjectDeliverySpec `json:"delivery,omitempty"`

	// Template names the infrastructure Template whose instance backs this
	// Project's development environment (docs/app-studio-template-sandboxes.md).
	// When set, the development binding is generated from the Template's
	// instanceCRD with farosMode: development, and file sync routes per the
	// Template's declared development components. Empty means the project has
	// no development environment yet — one must be selected before any
	// development runtime surface (sync, preview, logs) works.
	// +optional
	Template *ProjectTemplateSpec `json:"template,omitempty"`

	// Memory stores durable context the AI should consider for this
	// project. It is edited explicitly through the API in the MVP.
	// +optional
	Memory ProjectMemory `json:"memory,omitempty"`

	// Sharing captures App Studio access policy intent for previews and
	// published apps. Empty policies are interpreted as private.
	// +optional
	Sharing ProjectSharingSpec `json:"sharing,omitempty"`

	// Environments describe provider-backed runtime capabilities for this
	// Project. App Studio owns the binding contract; providers own runtime
	// implementation details.
	// +optional
	Environments []ProjectEnvironmentSpec `json:"environments,omitempty"`
}

type ProjectDeliveryMode string

const (
	// ProjectDeliveryModeDirect makes App Studio the desired-state writer.
	ProjectDeliveryModeDirect ProjectDeliveryMode = "Direct"
	// ProjectDeliveryModeGitOps makes the configured Git branch authoritative;
	// App Studio proposes changes and RepositorySync applies merged state.
	ProjectDeliveryModeGitOps ProjectDeliveryMode = "GitOps"
)

type ProjectGitOpsChangePolicy string

const (
	ProjectGitOpsChangePolicyPullRequest ProjectGitOpsChangePolicy = "PullRequest"
)

// ProjectEnvironmentDeliverySpec selects one environment's desired-state writer.
type ProjectEnvironmentDeliverySpec struct {
	// Mode selects exactly one desired-state writer.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Direct;GitOps
	Mode ProjectDeliveryMode `json:"mode"`
}

// ProjectDeliverySpec selects the Project's environment delivery contracts.
type ProjectDeliverySpec struct {
	// Development selects the writer for the mutable development sandbox.
	// +kubebuilder:validation:Required
	Development ProjectEnvironmentDeliverySpec `json:"development"`

	// Production selects the writer for promoted production deployments.
	// +kubebuilder:validation:Required
	Production ProjectEnvironmentDeliverySpec `json:"production"`

	// GitOps configures the shared repository location and review gate used by
	// every GitOps environment. repositoryRef is inherited from spec.repository
	// rather than copied.
	// +optional
	GitOps *ProjectGitOpsDeliverySpec `json:"gitOps,omitempty"`
}

// ProjectGitOpsDeliverySpec configures Git-owned environment manifests.
type ProjectGitOpsDeliverySpec struct {
	// Ref is the branch RepositorySync observes and ChangeRequests target.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Ref string `json:"ref,omitempty"`

	// Path is the repository-relative root containing Faros manifests.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	Path string `json:"path,omitempty"`

	// ChangePolicy controls how App Studio proposes configuration changes.
	// +optional
	// +kubebuilder:validation:Enum=PullRequest
	ChangePolicy ProjectGitOpsChangePolicy `json:"changePolicy,omitempty"`

	// RequiredApprovals is the minimum approval count before a proposed change
	// may merge. The Git host remains authoritative for branch protection.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	RequiredApprovals int32 `json:"requiredApprovals,omitempty"`
}

type ProjectSharingMode string

const (
	ProjectSharingModePrivate ProjectSharingMode = "private"
	ProjectSharingModeShared  ProjectSharingMode = "shared"
	ProjectSharingModePublic  ProjectSharingMode = "public"
)

type ProjectSharingSpec struct {
	// Preview controls who may access the mutable development preview. Private
	// requires platform sign-in and workspace access; public allows anyone with
	// the URL. The legacy shared value is normalized to private by App Studio.
	// +optional
	Preview ProjectPreviewSharingPolicy `json:"preview,omitempty"`

	// Publishing controls who may access published app instances once the
	// publishing runtime exists.
	// +optional
	Publishing ProjectSharingPolicy `json:"publishing,omitempty"`
}

type ProjectPreviewSharingPolicy struct {
	// Mode is the requested preview visibility. Empty means private.
	// +optional
	// +kubebuilder:validation:Enum=private;public
	Mode ProjectSharingMode `json:"mode,omitempty"`
}

type ProjectSharingPolicy struct {
	// Mode is the requested visibility for this channel. Empty means private.
	// +optional
	// +kubebuilder:validation:Enum=private;shared;public
	Mode ProjectSharingMode `json:"mode,omitempty"`
}

// ProjectTemplateSpec names the infrastructure Template backing the Project's
// development environment. Everything else — the instance kind to bind, the
// component/workspacePath map file sync routes by — is read live from the
// Template CR in the tenant workspace catalog, so template updates apply
// without a Project write.
type ProjectTemplateSpec struct {
	// Name is the Template's catalog name (e.g. "application").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
}

// ProjectRepositoryBinding identifies the Code provider Repository created for
// a Project.
type ProjectRepositoryBinding struct {
	// RepositoryRef names the Repository resource in the same workspace.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	RepositoryRef string `json:"repositoryRef"`

	// Name is the repository name on the git host.
	// +optional
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name,omitempty"`

	// ConnectionRef names the Code provider Connection used by the Repository.
	// +optional
	// +kubebuilder:validation:MaxLength=253
	ConnectionRef string `json:"connectionRef,omitempty"`

	// Adopted marks a binding built from an EXISTING Repository CR
	// (repository import). The Project reconciler creates the Repository CR
	// for non-adopted bindings only — an adopted repository is never
	// (re)created on the project's behalf.
	// +optional
	Adopted bool `json:"adopted,omitempty"`
}

// ProjectMemory is the MVP project memory document.
type ProjectMemory struct {
	// +optional
	Goals []string `json:"goals"`
	// +optional
	Requirements []string `json:"requirements"`
	// +optional
	Constraints []string `json:"constraints"`
}

type ProjectEnvironmentMode string

const (
	ProjectEnvironmentModeArtifact ProjectEnvironmentMode = "artifact"
	ProjectEnvironmentModeLive     ProjectEnvironmentMode = "live"
)

type ProjectPromotion string

const (
	ProjectPromotionManual ProjectPromotion = "manual"
	ProjectPromotionAuto   ProjectPromotion = "auto"
)

type ProjectBindingKind string

const (
	ProjectBindingKindProviderResource ProjectBindingKind = "providerResource"
	// ProjectBindingKindProviderReference is a non-owning reference to a
	// provider resource that already exists in the tenant workspace. App
	// Studio may observe the resource and use explicitly declared actions, but
	// must never create, update, or delete it.
	ProjectBindingKindProviderReference ProjectBindingKind = "providerReference"
)

// ProjectProviderActionSpec declares one versioned provider action that a
// generated application may invoke through the App Studio integration
// gateway. The gateway treats the declaration as an allow-list: an omitted
// action, an unknown version, or a revoked declaration is rejected before any
// provider tool is called.
type ProjectProviderActionSpec struct {
	// Name is the provider-neutral action name (for example query_table).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Name string `json:"name"`

	// Version identifies the versioned action contract (for example v1).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=32
	Version string `json:"version"`

	// SchemaDigest identifies the exact provider action schema that the caller
	// reviewed and consented to. Digests are immutable action-catalog content
	// addresses; digest-less grants are never accepted.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=71
	// +kubebuilder:validation:MaxLength=71
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	SchemaDigest string `json:"schemaDigest"`

	// GrantedBy is the authenticated caller recorded by the server when the
	// action grant is verified against the provider catalog. Clients cannot
	// set or replace this audit value.
	// +optional
	GrantedBy string `json:"grantedBy,omitempty"`

	// GrantedAt is the server time at which the catalog-backed grant was
	// verified. Clients cannot set or replace this audit value.
	// +optional
	GrantedAt *metav1.Time `json:"grantedAt,omitempty"`

	// Revoked disables this action without removing the integration binding,
	// allowing an operator to retain the audit history while closing access.
	// +optional
	Revoked bool `json:"revoked,omitempty"`

	// RevokedBy is the authenticated caller recorded by the server when an
	// active action grant is revoked. Clients cannot set or replace this audit
	// value.
	// +optional
	RevokedBy string `json:"revokedBy,omitempty"`

	// RevokedAt is the server time at which the active action grant was
	// revoked. Clients cannot set or replace this audit value.
	// +optional
	RevokedAt *metav1.Time `json:"revokedAt,omitempty"`
}

type ProjectEnvironmentSpec struct {
	// Name is a stable environment identifier such as development or test.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Name string `json:"name"`

	// Mode distinguishes artifact-based environments from live development
	// runtimes. Empty means artifact for backward compatibility.
	// +optional
	Mode ProjectEnvironmentMode `json:"mode,omitempty"`

	// AutoDeploy marks artifact environments that should deploy automatically.
	// +optional
	AutoDeploy bool `json:"autoDeploy,omitempty"`

	// Promotion controls how changes move into this environment.
	// +optional
	Promotion ProjectPromotion `json:"promotion,omitempty"`

	// Bindings connect this environment to provider capabilities.
	// +optional
	Bindings []ProjectProviderBindingSpec `json:"bindings,omitempty"`
}

type ProjectProviderBindingSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Name string `json:"name"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Provider string `json:"provider"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=providerResource;providerReference
	Kind ProjectBindingKind `json:"kind"`

	// +optional
	ResourceRef *ProjectProviderResourceReference `json:"resourceRef,omitempty"`

	// Values is provider-owned configuration. App Studio treats it as an
	// opaque contract payload.
	// +optional
	Values runtime.RawExtension `json:"values,omitempty"`

	// AllowedActions is the versioned allow-list for non-owning provider
	// references. Owning providerResource bindings may leave this empty; the
	// integration gateway requires an explicitly declared action before it
	// forwards a call.
	// +optional
	AllowedActions []ProjectProviderActionSpec `json:"allowedActions,omitempty"`
}

type ProjectProviderResourceReference struct {
	Name       string `json:"name,omitempty"`
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Resource   string `json:"resource,omitempty"`
}

// ProjectMessage is a single chat message in a Project.
type ProjectMessage struct {
	// ID is a server-assigned stable message identifier.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	ID string `json:"id"`

	// ProjectID is the name of the Project this message belongs to.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	ProjectID string `json:"projectID"`

	// Role is the message author.
	// +kubebuilder:validation:Enum=user;assistant
	Role string `json:"role"`

	// Content is the message body.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=32768
	Content string `json:"content"`

	// ContentEncrypted marks whether Content is encrypted at rest.
	// +optional
	ContentEncrypted bool `json:"contentEncrypted,omitempty"`

	// ContentKeyID identifies the key used to encrypt Content.
	// +optional
	// +kubebuilder:validation:MaxLength=128
	ContentKeyID string `json:"contentKeyID,omitempty"`

	// Metadata carries additional message annotations such as retry or
	// provider-specific envelope data.
	// +optional
	Metadata map[string]runtime.RawExtension `json:"metadata,omitempty"`

	// CreatedAt is the server timestamp for this message.
	CreatedAt metav1.Time `json:"createdAt"`
}

// ProjectStatus defines the observed Project state.
type ProjectStatus struct {
	// Phase is Ready for MVP-created Projects.
	// +optional
	Phase string `json:"phase,omitempty"`

	// UpdatedAt reflects the latest API mutation affecting metadata or memory.
	// +optional
	UpdatedAt *metav1.Time `json:"updatedAt,omitempty"`

	// Environments reports provider-observed environment state.
	// +optional
	Environments []ProjectEnvironmentStatus `json:"environments,omitempty"`
}

type ProjectEnvironmentStatus struct {
	Name     string                         `json:"name,omitempty"`
	Mode     ProjectEnvironmentMode         `json:"mode,omitempty"`
	Phase    string                         `json:"phase,omitempty"`
	Bindings []ProjectProviderBindingStatus `json:"bindings,omitempty"`
}

type ProjectProviderBindingStatus struct {
	Name       string            `json:"name,omitempty"`
	Provider   string            `json:"provider,omitempty"`
	Phase      string            `json:"phase,omitempty"`
	URL        string            `json:"url,omitempty"`
	PreviewURL string            `json:"previewURL,omitempty"`
	Outputs    map[string]string `json:"outputs,omitempty"`
}

// +kubebuilder:object:root=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ProjectList contains a list of Projects.
type ProjectList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Project `json:"items"`
}
