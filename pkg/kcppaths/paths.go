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

// Package kcppaths is the single source of truth for the faros kcp workspace
// topology. It has no internal dependencies so every layer (bootstrap,
// provisioner, admin, controllers, proxy) can import it without cycles.
//
// Topology:
//
//	root:faros
//	  providers:<provider>        standalone provider APIs + CatalogEntry
//	  tenants:<uuid>:<ws>:<edge>  tenant fleet (org/team/edge workspaces)
//	  system
//	    controllers               platform APIExports + APIResourceSchemas
//	    providers                 admin Provider + builtin CatalogEntry objects
//	    tenants                   User/Organization/Membership CR OBJECTS
//
// Standalone provider init owns its APIExport, schemas, and CatalogEntry in its
// provider sub-workspace. The system workspaces contain only platform-owned API
// contracts and administration objects.
package kcppaths

const (
	// Root is the faros root workspace.
	Root = "root:faros"

	// ProvidersParent is the parent of per-provider sub-workspaces. Objects live
	// in the named children, not in this parent workspace.
	ProvidersParent = Root + ":providers"

	// TenantsParent is the parent of per-tenant (organization) workspaces. The
	// tenant *fleet* (org/team/edge workspaces) lives here.
	TenantsParent = Root + ":tenants"

	// System groups the platform-internal workspaces.
	System = Root + ":system"

	// SystemControllers holds platform APIExports + APIResourceSchemas
	// (core / faros / tenancy / providers / admin .faros.sh). Every
	// consumer binds the exports from here.
	SystemControllers = System + ":controllers"

	// SystemProviders holds admin Provider objects and builtin CatalogEntries.
	// Standalone CatalogEntries live in their provider sub-workspaces.
	SystemProviders = System + ":providers"

	// SystemTenants holds the User / Organization / Membership CR OBJECTS
	// (replaces the former root:faros:users). NOT the tenant workspaces.
	SystemTenants = System + ":tenants"
)

// ProviderPath returns the sub-workspace path for a provider by name:
// root:faros:providers:<name>.
func ProviderPath(name string) string { return ProvidersParent + ":" + name }

// OrgPath returns the tenant workspace path for an org by UUID:
// root:faros:tenants:<uuid>.
func OrgPath(orgUUID string) string { return TenantsParent + ":" + orgUUID }

// WorkspacePath returns the team workspace path within a tenant org:
// root:faros:tenants:<uuid>:<ws>.
func WorkspacePath(orgUUID, wsUUID string) string { return OrgPath(orgUUID) + ":" + wsUUID }
