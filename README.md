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
