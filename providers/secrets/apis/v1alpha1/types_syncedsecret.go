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

// SyncedSecret declaratively projects secret material from a SecretStore into
// a workspace Secret, with ExternalSecret-style semantics: a refresh interval,
// key remapping, and last-sync/version status. It is what infrastructure
// templates and app-studio consume instead of hand-placed Secrets.
//
// The projected Secret is written in the SyncedSecret's own namespace, carries
// an ownerReference back to it, and is labeled as provider-managed — the
// controller refuses to overwrite a Secret it does not own.
//
// +crd
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=faros,shortName=ssync
// +kubebuilder:printcolumn:name="Store",type=string,JSONPath=`.spec.storeRef.name`
// +kubebuilder:printcolumn:name="Refresh",type=string,JSONPath=`.spec.refreshInterval`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Last Sync",type=date,JSONPath=`.status.lastSyncTime`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type SyncedSecret struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SyncedSecretSpec   `json:"spec"`
	Status SyncedSecretStatus `json:"status,omitempty"`
}

// SyncedSecretList is the standard k8s list wrapper.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type SyncedSecretList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SyncedSecret `json:"items"`
}

// SyncedSecretSpec is the desired state.
type SyncedSecretSpec struct {
	// StoreRef names the (cluster-scoped) SecretStore to read from.
	// +required
	StoreRef StoreReference `json:"storeRef"`

	// RefreshInterval is how often the projection is re-read from the
	// external store, picking up rotated values. Defaults to 1h.
	// +optional
	// +kubebuilder:default="1h"
	RefreshInterval *metav1.Duration `json:"refreshInterval,omitempty"`

	// Target configures the projected Secret. Empty projects into a Secret
	// named after the SyncedSecret in its own namespace.
	// +optional
	Target *SyncedSecretTarget `json:"target,omitempty"`

	// Data maps individual remote properties onto Secret keys. Use for key
	// remapping or cherry-picking; use DataFrom to pull a whole path.
	// At least one of data / dataFrom must be set.
	// +optional
	// +kubebuilder:validation:MaxItems=64
	Data []SyncedSecretData `json:"data,omitempty"`

	// DataFrom pulls every property at each remote path into the projected
	// Secret verbatim. Later entries win on key collisions; explicit Data
	// mappings win over DataFrom.
	// +optional
	// +kubebuilder:validation:MaxItems=16
	DataFrom []RemoteReference `json:"dataFrom,omitempty"`
}

// StoreReference names a SecretStore in the same workspace.
type StoreReference struct {
	// Name of the SecretStore.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
}

// SyncedSecretTarget configures the projected Secret.
type SyncedSecretTarget struct {
	// Name of the projected Secret. Empty defaults to the SyncedSecret's
	// own name.
	// +optional
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name,omitempty"`
}

// SyncedSecretData maps one remote property onto one Secret key.
type SyncedSecretData struct {
	// SecretKey is the key in the projected Secret's data.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	SecretKey string `json:"secretKey"`

	// RemoteRef addresses the remote value.
	// +required
	RemoteRef RemoteReference `json:"remoteRef"`
}

// RemoteReference addresses secret material in the external store.
type RemoteReference struct {
	// Path is the location within the store (for Vault KV v2, the path
	// under the configured mount, without the /data/ infix).
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Path string `json:"path"`

	// Property selects one key of the value at Path. Empty in a DataFrom
	// entry pulls all properties; required semantics in a Data entry:
	// empty means the property named after SecretKey.
	// +optional
	// +kubebuilder:validation:MaxLength=253
	Property string `json:"property,omitempty"`
}

// SyncedSecretStatus is the observed state.
type SyncedSecretStatus struct {
	// ObservedGeneration mirrors metadata.generation last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// SecretName is the projected Secret's name once written.
	// +optional
	// +kubebuilder:validation:MaxLength=253
	SecretName string `json:"secretName,omitempty"`

	// LastSyncTime is when the projection last succeeded.
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// SyncedVersion is a content hash of the projected data (sha256 over
	// sorted key/value pairs, hex, prefixed "sha256:"). Changes exactly when
	// the projected material changes, so consumers can watch for rotation
	// without reading the Secret.
	// +optional
	// +kubebuilder:validation:MaxLength=100
	SyncedVersion string `json:"syncedVersion,omitempty"`

	// SyncedKeys is the number of keys in the projected Secret.
	// +optional
	SyncedKeys int32 `json:"syncedKeys,omitempty"`

	// Conditions follows the standard Kubernetes conditions pattern.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// SyncedSecret condition reasons (beyond the shared SecretStore ones, which
// the sync controller re-uses for backend failures).
const (
	// ReasonStoreNotFound — spec.storeRef names a SecretStore that does not
	// exist in this workspace.
	ReasonStoreNotFound = "StoreNotFound"
	// ReasonTargetConflict — the target Secret exists but is not managed by
	// this SyncedSecret; the controller refuses to overwrite it.
	ReasonTargetConflict = "TargetConflict"
	// ReasonSyncFailed — reading from the external store failed.
	ReasonSyncFailed = "SyncFailed"
	// ReasonInvalidSpec — the spec addresses nothing (no data / dataFrom).
	ReasonInvalidSpec = "InvalidSpec"
)

// FinalizerSyncedSecret is added by the SyncedSecretController; it deletes the
// projected Secret on SyncedSecret deletion (the ownerReference would GC it
// too, but be deterministic about it).
const FinalizerSyncedSecret = "syncedsecrets.secrets.faros.sh/finalizer"

// Labels stamped on projected Secrets. ManagedByLabel gates overwrites: the
// controller only writes Secrets carrying it (the A.1 "provider-owned material
// only" discipline), and its value is the owning SyncedSecret's name.
const (
	ManagedByLabel      = "secrets.faros.sh/managed-by"
	ManagedByLabelValue = "syncedsecret"
	OwnerNameLabel      = "secrets.faros.sh/synced-secret"
)
