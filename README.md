# Faros Kubernetes cluster manager

Enables easy access of remote Kubernetes clusters anywhere in the world.


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
pkg/service - API/Proxy service
pkg/store - storage interface and implementation
pkg/controller - utility to run multiple services
pkg/cli - command line interface
pkg/config - configuration package for everything
pkg/client - rest client (manual typed for now)
```

## Bootstrap for development

```
make build-cli
./faros create namespace test
./clr create cluster test -k $KUBECONFIG
```


## Design aspirations

1. Use of kubectl for all commands as extension
2. Proxy with provided kubeconfig (fake/TTL -> kubeconfig)
3. Agent based without kubectl (fake/TTL -> server <= agent)
4. ClusterAccessSessions defines connections to clusters
5. 'Reflector' reflects objects into K8S layer if provided and back. (separate component)

Cluster Access modes:
1. Proxy - use provided kubeconfig
2. Proxy Cloud - use cloud credentials to read kubeconfig from cloud
3. Agent - use server to initiate connection. Server side dictates permissions
4.
