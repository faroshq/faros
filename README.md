# Faros - lightweight Kuberentes cluster monitoring Operator


## Quickstarts

```
export KUBECONFIG=mycluster.kubeconfig
./faros deploy

# To check
kubectl get cluster.operator.faros.sh -o yaml
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

* Operator exposes basic cluster health as status on the `cluster.operator.faros.sh`
CRD.

* (future) Monitoring hub will read specific CRD and will query all its cluster
to aggregate metrics. Metrics will be enriched by cluster metadata and exposed
via statsd or prometheus format.

* (future) Operator will be able to send metrics via `Push` method to central portal
and from there customer can either read metrics or act based on them


## Useful links

* TBC
