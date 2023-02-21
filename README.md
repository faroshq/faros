# Faros hub - KCP-based self-managed control plane

Faros hub allows is control plane for Kubernetes clusters. It is based on KCP (Kubernetes Control Plane) project.
It allows the management of multiple Kubernetes clusters from a single control plane.

KCP allows users to create organizations and workspaces inside organizations.

# TODO

1. Make getting org and workspace based on user bindings
2. Add basic quotas for orgs and workspaces
3. Move to APIExportEndpointSlice.status.endpoints
4. Add Certificate to kubeconfig
5. Add refresh for kubeconfig token
6. Make orgs `rootless` LogicalClusters. It will require running shard-api in the same cluster as shard itself, and doing some loadbalancing. Replicate front-proxy.
