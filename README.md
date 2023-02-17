# Faros hub - KCP-based self-managed control plane

Faros hub allows is control plane for Kubernetes clusters. It is based on KCP (Kubernetes Control Plane) project.
It allows the management of multiple Kubernetes clusters from a single control plane.

KCP allows users to create organizations and workspaces inside organizations.

# TODO

1. Make getting org and workspace based on user bindings
2. Add basic quotas for orgs and workspaces
3. Add an ability to add other members/groups to orgs and workspaces
4. Move to APIExportEndpointSlice.status.endpoints
5. Make kubeconfig "double" so API works even when cluster context is set
6. Add Certificate to kubeconfig
7. Add refresh for kubeconfig token
