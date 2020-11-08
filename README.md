# Faros - native lightweight Kubernetes cluster monitoring Hub & Operator

## Overview

Agent works in hub and monitor/operator mode.
Operator enhances data to be monitored. But it is not mandatory.
Hub reads Cluster objects and distributed them to be monitored over to "hub replicas".

### Architecture

* Hub runs in master/slave configuration. And all cluster to be monitored are
divided equally to slaves.
* Each slave register itself as `worker.faros.sh` and master will divide work
based on availability. If worker goes "dark", his monitoring workload is re-distributed.
* Operator exposes basic cluster health as status on the `object.monitor.faros.sh`
CRD.
* (future) Monitoring hub will read specific CRD and will query all its cluster
to aggregate metrics. Metrics will be enriched by cluster metadata and exposed
via statsd or prometheus format.
* (future) Operator will be able to send metrics via `Push` method to central portal
and from there customer can either read metrics or act based on them
* (future) When scaled up workers will be redistributed. Currently only on scale
down actions re-distribution happens.

*** Under development ***

Faros enabled you to convert Kubernetes cluster into monitoring hub to monitor
other clusters.

Once cluster object is created on Hub cluster, external cluster will be monitored
and metrics exposed via metrics interface.

For more complicated monitoring scenarios Faros has operator running on monitored
cluster, where it can check more complicated edge-cases.

## Quickstarts

### Monitored cluster (optional)

Deploy faros operator on cluster, which required more advance monitoring
```
export KUBECONFIG=mycluster.kubeconfig
./faros deploy operator

# To check config for Faros cluster object
kubectl get configs.faros.sh/cluster -o yaml

# Create Network monitor and check status
kubectl create -f pkg/operator/deploy/examples/network_example.yaml
kubectl get Network.monitor.faros.sh/cluster
```

### Hub cluster

Deploy Faros hub into the cluster and configure it to monitor external clusters

```bash
export KUBECONFIG=mycluster.kubeconfig
./faros deploy hub

# To check config for Faros Hub object
kubectl get configs.faros.sh/hub -o yaml

# Add Hub cluster to monitor itself by creating Secret containing KUBECONFIG
# and creating cluster object with reference to the secret

kubectl create secret generic hub-kubeconfig -n faros-operator --from-file=kubeconfig=$KUBECONFIG
kubectl create -f pkg/operator/deploy/examples/cluster_example.yaml

kubectl get cluster.faros.sh/hub -n faros-operator -o yaml
```

## Development

1. Create a cluster where you gonna do all the development
2. Run `go run ./cmd/faros deploy hub`
3. Run `go run ./cmd/faros deploy operator`
4. Run hub: `go run ./cmd/faros hub`
5. Run hub replica in second terminal `go run ./cmd/faros hub`

## Contributing

This project welcomes contributions and suggestions.

## Repository map

* cmd/faros: entrypoints.

  * deploy: Deploys Faros operator to the cluster
  * operator: Runs Faros operator
  * hub: Runs monitoring hub (WIP)

* docs: Documentation.

* hack: Build scripts and utilities.

* pkg: RP source code:

  * pkg/operator: Operator codebase

  * pkg/deploy: Deployment codebase

  * pkg/hub: Monitoring hub (WIP)

  * pkg/util: Utility libraries.

* vendor: vendored Go libraries.


## Basic architecture


## Useful links

* TBC
