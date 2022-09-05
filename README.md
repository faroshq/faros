# faros - Kubernetes Proxy

Faros - Central Kubernetes API Proxy enabling you to mint short-lived (TTL) Kubeconfig
to any k8s clusters. It will proxy requests to the k8s clusters without exposing original credentials.

This enables users to access cluster for deployment, break-glass, or operational
work without any additional changes on cluster side.


## Roadmap

1. AWS, Azure, Google Cloud support for accessing cluster without need to provide kubeconfig
2. Make faros CLI self-update once new release is pushed
3. Add caching for Database queries to increase performance of the API
4. Add metrics for tracing
5. Add basic audit capability to provide events on actions executed.
# Installation

## CLI

Install from release:
```bash
curl https://downloads.faros.sh/install-cli.sh | bash
```

Build:
```
make cli
```

## API control plane

API control plane will proxy all requests to managed clusters

### Kubernetes

```bash
TBC
```
### Docker-compose

```bash
TBC
```

### Docker

```bash
TBC
```


# Development

Run:
```
make run
```

Interact with it:
```
make build-cli
./faros --help
```

Folder structure:
```
cmd - entrypoint for all commands
pkg/cli - CLI code
pkg/client - go typed client
pkg/config - configuration package for everything
pkg/models - api structured, data models
pkg/service - API/Proxy service
pkg/session - session TTL manager
pkg/store - storage interface and implementation
pkg/controller - utility to run multiple services
```

## Bootstrap for development

```
make build-cli
./faros create namespace test
./clr create cluster test -k $KUBECONFIG
```


## Future state

1. Agent based without kubectl (fake/TTL -> server <= agent)2
2. ClusterAccessSessions defines connections to clusters
3. 'Reflector' reflects objects into K8S layer if provided and back. (separate component)

Cluster Access modes:
1. Proxy - use provided kubeconfig
2. Proxy Cloud - use cloud credentials to read kubeconfig from cloud and use those to proxy
3. Agent - use server to initiate connection. Server side dictates permissions
