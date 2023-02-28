# Faros - Kubernetes like control planes

Faros allows you to self-provision [KCP](https://github.com/kcp-dev/kcp)-based 'Kubernetes
like' control planes and use them for development, testing, and demos, or run
your multi-tenant Kubernetes as a service.

## What is KCP?

KCP gives you the ability to have Kubernetes-like control planes, but without the
nodes, compute resources, and operational overhead. It is a set of Kubernetes
resources like `Namespace`, `ClusterRole`, `ClusterRoleBinding`, `ServiceAccount`, `CustomResourceDefinitions`
allowing you to create your own operators and controllers and use faros as a backend.

In addition, KCP allows you to attach your own compute layer to it, and use it as a regular Kubernetes cluster.
See more about it [here](https://docs.kcp.io/kcp/v0.11/en/).

This allows you to use any Kubernetes cluster as a backend for your KCP-based control planes for compute.
And this can be any Kubernetes cluster, including K3S, EKS, GKE, AKS, etc. Compute attachment is done via
Reverse tunnels, so no need to expose any ports on your compute cluster.

## How it works

1. Download faros `kubectl-faros` plugin from [releases](https://github.com/faroshq/faros/releases) and install it.

```bash
https://github.com/faroshq/faros/releases/latest

tar -xvf kubectl-faros-v*.tar.gz
mv kubectl-faros /usr/local/bin/kubectl-faros
chmod +x /usr/local/bin/kubectl-faros
rm kubectl-faros-v*.tar.gz
```

2. Login to Faros

```bash
kubectl faros login
```

3. Create an organization

```bash
kubectl faros org create my-org
kubectl faros org use my-org
```

4. Create a workspace

```bash
kubectl faros workspace create my-workspace
kubectl faros workspace use my-workspace
```

Once you have created a workspace, you can use it as a regular KCP/Kubernetes cluster.

## How to use Faros

See available APIS:

```bash
kubectl api-resources
```
# Roadmap/TODO

1. Add basic quotas for orgs and workspaces
2. Move to APIExportEndpointSlice.status.endpoints
3. Add Certificate to kubeconfig
4. Add refresh for kubeconfig token
5. Make orgs `rootless` LogicalClusters. It will require running shard-api in the same cluster as shard itself, and doing some loadbalancing. Replicate front-proxy.
6. Write faros auth plugin for kubectl
