# Faros - KCP-based self-managed control planes

Faros allows to self-provision [KCP](https://github.com/kcp-dev/kcp) workspaces and use all the benefits of KCP.

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
