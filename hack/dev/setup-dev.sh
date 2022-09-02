#!/bin/bash


if [ ! -f "/usr/local/bin/kind" ]; then
 echo "Installing KIND"
 curl -Lo ./kind https://kind.sigs.k8s.io/dl/v0.14.0/kind-linux-amd64
 chmod +x ./kind
 sudo mv ./kind /usr/local/bin/kind
else
    echo "KIND already installed"
fi

kind create cluster --name cluster1 --kubeconfig ./dev/cluster1
kind create cluster --name cluster2 --kubeconfig ./dev/cluster2


echo "Configure faros dev"

./faros configure  --insecure-skip-tls-verify --api-endpoint https://localhost:8443/api/v1 --namespace test
./faros create namespace test || true
./faros create namespace test1 || true
./faros create namespace test2 || true

./faros create cluster cluster1 --kubeconfig ./dev/cluster1 --namespace test1
./faros create cluster cluster2 --kubeconfig ./dev/cluster2 --namespace test2

./faros create access cluster1-access -c cluster1 --namespace test1
./faros create access cluster2-access -c cluster2 --namespace test2
