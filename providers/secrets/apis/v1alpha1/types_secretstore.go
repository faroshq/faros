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

// SecretStore binds a tenant to one external secret backend. It is the
// credential anchor SyncedSecret references: every secret read runs against
// the backend this store authenticates to.
//
// The credential itself never lives on the CR — spec.secretRef points at a
// Secret in the tenant workspace holding the token. The SecretStoreController
// resolves that Secret, calls the backend to verify it, and records the
// outcome on the Validated condition. External stores remain the source of
// truth; faros never copies material out of them except through SyncedSecret
// projections the tenant declares.
//
// +crd
// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,categories=faros,shortName=sstore
// +kubebuilder:printcolumn:name="Backend",type=string,JSONPath=`.spec.backend`
// +kubebuilder:printcolumn:name="Validated",type=string,JSONPath=`.status.conditions[?(@.type=="Validated")].status`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type SecretStore struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SecretStoreSpec   `json:"spec"`
	Status SecretStoreStatus `json:"status,omitempty"`
}

// SecretStoreList is the standard k8s list wrapper.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type SecretStoreList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SecretStore `json:"items"`
}

// SecretStoreSpec is the desired state.
type SecretStoreSpec struct {
	// Backend names the external secret backend. v1: vault.
	// +required
	Backend StoreBackendType `json:"backend"`

	// Vault configures the Vault backend. Required when backend=vault.
	// +optional
	Vault *VaultStoreSpec `json:"vault,omitempty"`

	// SecretRef points at the Secret in the tenant workspace holding the
	// credential that authenticates to the backend. For backend=vault the
	// default key is "token".
	// +required
	SecretRef LocalSecretReference `json:"secretRef"`
}

// VaultStoreSpec configures a Vault KV v2 backend.
type VaultStoreSpec struct {
	// Address is the Vault server base URL.
	// +required
	// +kubebuilder:validation:Pattern=`^https?://[A-Za-z0-9.-]+(:[0-9]+)?/?$`
	// +kubebuilder:validation:MaxLength=2048
	Address string `json:"address"`

	// Mount is the KV v2 secrets-engine mount path. Defaults to "secret",
	// the engine Vault enables out of the box.
	// +optional
	// +kubebuilder:default=secret
	// +kubebuilder:validation:MaxLength=253
	Mount string `json:"mount,omitempty"`

	// Namespace is the Vault Enterprise namespace to target (sent as the
	// X-Vault-Namespace header). Empty for OSS Vault.
	// +optional
	// +kubebuilder:validation:MaxLength=253
	Namespace string `json:"namespace,omitempty"`
}

// SecretStoreStatus is the observed state.
type SecretStoreStatus struct {
	// ObservedGeneration mirrors metadata.generation last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// BackendVersion is the backend server version discovered during
	// validation (e.g. the Vault version). Empty until first successful
	// validation.
	// +optional
	// +kubebuilder:validation:MaxLength=100
	BackendVersion string `json:"backendVersion,omitempty"`

	// Conditions follows the standard Kubernetes conditions pattern. The
	// Validated condition is True once the credential authenticates.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// SecretStore condition types.
const (
	// ConditionValidated flips True once the credential authenticates
	// against the external backend.
	ConditionValidated = "Validated"
)

// SecretStore condition reasons, stable across reconciles so the portal can
// key UX off them.
const (
	// ReasonCredentialUnavailable — the referenced credential Secret is
	// missing, unreadable, or has no non-empty token key.
	ReasonCredentialUnavailable = "CredentialUnavailable"
	// ReasonBackendNotConfigured — spec.backend names a backend whose
	// configuration block (e.g. spec.vault) is absent, or no backend with
	// that name is registered.
	ReasonBackendNotConfigured = "BackendNotConfigured"
	// ReasonStoreUnavailable — the backend could not be reached or answered
	// with a server error; retried automatically.
	ReasonStoreUnavailable = "StoreUnavailable"
	// ReasonAccessDenied — the backend rejected the credential.
	ReasonAccessDenied = "AccessDenied"
	// ReasonPathNotFound — the addressed mount or path does not exist.
	ReasonPathNotFound = "PathNotFound"
	// ReasonValidationFailed — any other validation failure.
	ReasonValidationFailed = "ValidationFailed"
)

// FinalizerSecretStore is added by the SecretStoreController.
const FinalizerSecretStore = "secretstores.secrets.faros.sh/finalizer"
