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
// +groupName=infrastructure.faros.sh

// Package v1alpha1 contains the platform-facing API for the
// infrastructure provider — a backend-neutral catalog system that publishes
// Templates read-only and exposes one stable Instance kind to tenant
// workspaces. Instance.spec.template selects the product without exposing
// which backend (kro today; terraform/cloud later) materializes it.
//
// The provider's authored API surface is Template and Instance. Per-template
// runtime kinds exist only on the backend cluster and are not APIExport
// resources.
//
// See docs/infrastructure-architecture.md for the full design.
package v1alpha1
