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

// +k8s:deepcopy-gen=package,register
// +groupName=secrets.faros.sh

// Package v1alpha1 contains the tenant-facing API for the secrets provider —
// declarative projection of externally-stored secrets into tenant workspaces.
//
// Two kinds ship in v1:
//
//   - SecretStore anchors a connection to an external secret backend (Vault
//     KV v2 today). The credential for the store itself never lives on the CR:
//     spec.secretRef points at a Secret in the tenant workspace, mirroring the
//     code provider's Connection. The controller validates the credential and
//     records Validated/Ready conditions.
//   - SyncedSecret declaratively projects secret material from a SecretStore
//     path into a workspace Secret with ExternalSecret-style semantics:
//     refresh interval, key remapping, and last-sync/version status.
//
// faros stores no master secrets: external stores remain the source of truth,
// and the provider only holds tenant Secret refs (the A.2 non-goal in
// docs/plan-secrets-mcp-governance-edge-autonomy.md).
package v1alpha1
