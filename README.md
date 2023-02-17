# Faros hub - KCP based self-managed control plane

Faros hub allows is control plane for Kubernetes clusters. It is based on KCP (Kubernetes Control Plane) project.
It allows to manage multiple Kubernetes clusters from a single control plane.

KCP allows users to create organizations, and workspaces inside organizations.



# TODO

1. Make getting org and workspace based on user bindings
2. Add bindings for membership and basic roles
3. Add workspace creation
4. Add basic quotas for orgs and workspaces
5. Add an ability to add other members/groups to orgs and workspaces
6. Move to APIExportEndpointSlice.status.endpoints
