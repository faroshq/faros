# Faros - lightweight Kuberentes cluster monitoring Operator


## Quickstarts

```
export KUBECONFIG=mycluster.kubeconfig
./faros deploy

# To check default config for Faros
kubectl get config.faros.sh -o yaml

# Create Network monitor and check status
kubectl create -f pkg/operator/deploy/staticresources/network_example.yaml
kubectl get Network.monitor.faros.sh/cluster


```
## Contributing

This project welcomes contributions and suggestions.

## Repository map

* .pipelines: CI workflows using Azure pipelines.

* cmd/faros: entrypoints.

  * deploy: Deploys Faros operator to the cluster
  * operator: Runs Faros operator
  * monitor: Runs monitoring hub (Roadmap)

* docs: Documentation.

* hack: Build scripts and utilities.

* pkg: RP source code:

  * pkg/operator: Operator codebase

  * pkg/monitor: Monitoring hub (Roadmap)

  * pkg/util: Utility libraries.

* vendor: vendored Go libraries.


## Basic architecture

* Operator exposes basic cluster health as status on the `object.monitor.faros.sh`
CRD.

* (future) Monitoring hub will read specific CRD and will query all its cluster
to aggregate metrics. Metrics will be enriched by cluster metadata and exposed
via statsd or prometheus format.

* (future) Operator will be able to send metrics via `Push` method to central portal
and from there customer can either read metrics or act based on them


## Useful links

* TBC
