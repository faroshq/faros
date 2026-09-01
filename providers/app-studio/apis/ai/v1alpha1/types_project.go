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

	// Template names the infrastructure Template whose instance backs this
	// Project's development environment (docs/app-studio-template-sandboxes.md).
	// When set, the development binding is generated from the Template's
	// instanceCRD with farosMode: development, and file sync routes per the
	// Template's declared development components. Empty means the project has
	// no development environment yet — one must be selected before any
	// development runtime surface (sync, preview, logs) works.
	// +optional
	Template *ProjectTemplateSpec `json:"template,omitempty"`

	// Components are the stable source and build units that make up this
	// Project. A component can be mapped to a different provider Template in
	// each environment without changing its source identity.
	// +optional
	// +kubebuilder:validation:MaxItems=64
	// +listType=map
	// +listMapKey=name
	Components []ProjectComponentSpec `json:"components,omitempty"`

	// Build identifies the repository-owned CI workflow for a Project whose
	// development environment is not backed by an infrastructure Template.
	// Template-backed Projects continue to resolve this contract from their
	// selected Template's development.build block.
	// +optional
	Build *ProjectBuildSpec `json:"build,omitempty"`

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

// ProjectBuildSpec identifies the repository-owned workflow App Studio
// observes and dispatches to produce immutable component images. App Studio
// never creates or edits this workflow.
type ProjectBuildSpec struct {
	// WorkflowPath is the repository-relative GitHub Actions workflow file.
	// It must be directly under .github/workflows so Code can dispatch the
	// corresponding workflow by filename without allowing path traversal.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^\.github/workflows/[^/]+\.ya?ml$`
	// +kubebuilder:validation:MaxLength=256
	WorkflowPath string `json:"workflowPath"`
}

// ProjectComponentKind identifies the runtime role of a Project component.
type ProjectComponentKind string

const (
	// ProjectComponentKindService is a long-running networked component.
	ProjectComponentKindService ProjectComponentKind = "Service"
	// ProjectComponentKindWorker is a long-running component without a public
	// network endpoint.
	ProjectComponentKindWorker ProjectComponentKind = "Worker"
)

// ProjectComponentProtocol identifies the network protocol a production
// component port speaks. HTTP and HTTPS are routable through the platform;
// TCP is retained for provider-specific internal services.
type ProjectComponentProtocol string

const (
	ProjectComponentProtocolHTTP  ProjectComponentProtocol = "HTTP"
	ProjectComponentProtocolHTTPS ProjectComponentProtocol = "HTTPS"
	ProjectComponentProtocolTCP   ProjectComponentProtocol = "TCP"
)

// ProjectComponentSpec is the Project-owned logical identity of one source
// and build unit. Providers consume this contract through environment binding
// mappings; they do not own the source paths.
type ProjectComponentSpec struct {
	// Name is a stable component identifier such as web, api, or worker.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Name string `json:"name"`

	// Kind determines whether this component is expected to serve traffic.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Service;Worker
	Kind ProjectComponentKind `json:"kind"`

	// SourcePath is the component's directory relative to the repository root.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	SourcePath string `json:"sourcePath"`

	// Build describes the source context used to produce an immutable image.
	// +optional
	Build *ProjectComponentBuildSpec `json:"build,omitempty"`

	// Ports describes the production network contract for this component.
	// Development listener ports are configured independently by the
	// DevelopmentService resource.
	// +optional
	// +kubebuilder:validation:MaxItems=32
	// +listType=map
	// +listMapKey=name
	Ports []ProjectComponentPortSpec `json:"ports,omitempty"`
}

// ProjectComponentBuildSpec identifies a Docker-compatible build context.
type ProjectComponentBuildSpec struct {
	// ContextPath is relative to the repository root.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	ContextPath string `json:"contextPath"`

	// DockerfilePath is relative to the repository root.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	DockerfilePath string `json:"dockerfilePath"`
}

// ProjectComponentPortSpec declares a named production port.
type ProjectComponentPortSpec struct {
	// Name is the stable port name used by Template component mappings and
	// application routes.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Name string `json:"name"`

	// Protocol is the protocol spoken by the port.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=HTTP;HTTPS;TCP
	Protocol ProjectComponentProtocol `json:"protocol"`

	// ContainerPort is the port exposed by the component workload.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	ContainerPort int32 `json:"containerPort"`
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

	// Preview selects the primary DevelopmentService URL shown by App Studio
	// for this environment. Other services remain addressable individually.
	// +optional
	Preview *ProjectEnvironmentPreviewSpec `json:"preview,omitempty"`

	// Bindings connect this environment to provider capabilities.
	// +optional
	Bindings []ProjectProviderBindingSpec `json:"bindings,omitempty"`

	// Connections bind provider-resource outputs to a consuming binding or
	// DevelopmentService in this environment. They are logical, secret-free
	// intent: the Project controller resolves exact object identities and owns
	// the derived Infrastructure Connection resources.
	// +optional
	// +kubebuilder:validation:MaxItems=32
	// +listType=map
	// +listMapKey=name
	Connections []ProjectEnvironmentConnectionSpec `json:"connections,omitempty"`
}

// ProjectEnvironmentPreviewSpec selects the environment's primary preview.
type ProjectEnvironmentPreviewSpec struct {
	// PrimaryServiceRef is the name of a DevelopmentService in this Project
	// environment.
	// +optional
	// +kubebuilder:validation:MaxLength=63
	PrimaryServiceRef string `json:"primaryServiceRef,omitempty"`
}

// ProjectConnectionReferenceKind identifies a logical endpoint within a
// Project environment. Source references are always bindings; targets may be
// bindings or logical DevelopmentServices.
type ProjectConnectionReferenceKind string

const (
	ProjectConnectionReferenceBinding            ProjectConnectionReferenceKind = "binding"
	ProjectConnectionReferenceDevelopmentService ProjectConnectionReferenceKind = "developmentService"
)

// ProjectConnectionEndpointReference names a logical Project-owned endpoint.
// Physical object names and UIDs are deliberately controller-owned.
type ProjectConnectionEndpointReference struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=binding;developmentService
	Kind ProjectConnectionReferenceKind `json:"kind"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Name string `json:"name"`
}

// ProjectConnectionMappingSpec narrows one provider-declared connection
// mapping. Empty mappings select the target interface's provider-owned
// defaults.
type ProjectConnectionMappingSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9._-]+$`
	SourceKey string `json:"sourceKey"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[A-Za-z_][A-Za-z0-9_]*$`
	TargetKey string `json:"targetKey"`
}

// ProjectEnvironmentConnectionSpec is generic environment-scoped dependency
// wiring. It contains no credentials or provider runtime identity.
type ProjectEnvironmentConnectionSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	Name string `json:"name"`

	// +kubebuilder:validation:Required
	SourceRef ProjectConnectionEndpointReference `json:"sourceRef"`

	// +kubebuilder:validation:Required
	TargetRef ProjectConnectionEndpointReference `json:"targetRef"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9-]*$`
	SourceInterface string `json:"sourceInterface"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9-]*$`
	TargetInterface string `json:"targetInterface"`

	// +optional
	// +kubebuilder:validation:MaxItems=32
	// +listType=map
	// +listMapKey=targetKey
	Mappings []ProjectConnectionMappingSpec `json:"mappings,omitempty"`
}

// ProjectBindingDeletionPolicy controls what happens to a provider resource
// when its Project binding is deleted.
type ProjectBindingDeletionPolicy string

const (
	// ProjectBindingDeletionPolicyDelete removes the provider resource with its
	// Project binding. This is the compatibility default.
	ProjectBindingDeletionPolicyDelete ProjectBindingDeletionPolicy = "Delete"
	// ProjectBindingDeletionPolicyRetain detaches the provider resource so it
	// survives Project or environment deletion.
	ProjectBindingDeletionPolicyRetain ProjectBindingDeletionPolicy = "Retain"
)

// ProjectBindingLifecycleSpec contains lifecycle policy for one provider
// resource binding.
type ProjectBindingLifecycleSpec struct {
	// DeletionPolicy defaults to Delete when omitted. Stateful providers can be
	// retained explicitly by setting Retain.
	// +optional
	// +kubebuilder:validation:Enum=Delete;Retain
	DeletionPolicy ProjectBindingDeletionPolicy `json:"deletionPolicy,omitempty"`
}

// ProjectComponentMappingSpec maps a Project component to a named component
// in the selected provider Template.
type ProjectComponentMappingSpec struct {
	// ComponentRef names the Project component.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	ComponentRef string `json:"componentRef"`

	// TargetComponent names the component in the selected Template.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	TargetComponent string `json:"targetComponent"`
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

	// TemplateRef selects the provider Template for this binding. It is the
	// canonical source for managed provider resources. Project.spec.template
	// remains a legacy/simple-flow fallback for bindings that omit this field.
	// +optional
	TemplateRef *ProjectTemplateSpec `json:"templateRef,omitempty"`

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

	// ComponentMappings map Project-owned source components to components in
	// the selected Template. They are primarily used by artifact/production
	// bindings and are ignored by provider references.
	// +optional
	// +kubebuilder:validation:MaxItems=64
	// +listType=map
	// +listMapKey=targetComponent
	ComponentMappings []ProjectComponentMappingSpec `json:"componentMappings,omitempty"`

	// Lifecycle controls deletion behavior for the bound provider resource.
	// +optional
	Lifecycle *ProjectBindingLifecycleSpec `json:"lifecycle,omitempty"`
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

	// BindingInventory records the last provider-resource identities owned by
	// this Project. The controller uses it to clean up a binding removed from
	// spec, when the removed binding's dynamic resource kind is no longer
	// available in the current Project spec.
	// +optional
	// +kubebuilder:validation:MaxItems=128
	BindingInventory []ProjectBindingInventoryStatus `json:"bindingInventory,omitempty"`
}

type ProjectEnvironmentStatus struct {
	Name        string                               `json:"name,omitempty"`
	Mode        ProjectEnvironmentMode               `json:"mode,omitempty"`
	Phase       string                               `json:"phase,omitempty"`
	Bindings    []ProjectProviderBindingStatus       `json:"bindings,omitempty"`
	Connections []ProjectEnvironmentConnectionStatus `json:"connections,omitempty"`
}

// ProjectEnvironmentConnectionStatus mirrors only secret-free Infrastructure
// Connection state. Secret references, paths, and values never cross into the
// Project API.
type ProjectEnvironmentConnectionStatus struct {
	Name     string `json:"name,omitempty"`
	Phase    string `json:"phase,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Message  string `json:"message,omitempty"`
	Revision string `json:"revision,omitempty"`
}

type ProjectProviderBindingStatus struct {
	Name       string            `json:"name,omitempty"`
	Provider   string            `json:"provider,omitempty"`
	Phase      string            `json:"phase,omitempty"`
	URL        string            `json:"url,omitempty"`
	PreviewURL string            `json:"previewURL,omitempty"`
	Outputs    map[string]string `json:"outputs,omitempty"`
}

// ProjectBindingInventoryStatus preserves enough information to lifecycle a
// provider resource after its binding has been removed from spec. It is
// controller-maintained status, not user-authored desired state.
type ProjectBindingInventoryStatus struct {
	// Environment identifies the environment that previously contained the
	// binding.
	Environment string `json:"environment,omitempty"`

	// Binding identifies the removed binding.
	Binding string `json:"binding,omitempty"`

	// Provider identifies the provider that owns the resource.
	Provider string `json:"provider,omitempty"`

	// ResourceRef is the complete dynamic resource identity required to address
	// the provider resource after the binding is removed.
	ResourceRef *ProjectProviderResourceReference `json:"resourceRef,omitempty"`

	// DeletionPolicy is the policy captured when the binding was last observed.
	DeletionPolicy ProjectBindingDeletionPolicy `json:"deletionPolicy,omitempty"`
}

// +kubebuilder:object:root=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ProjectList contains a list of Projects.
type ProjectList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Project `json:"items"`
}
