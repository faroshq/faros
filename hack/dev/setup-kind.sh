#!/bin/bash

source .env

if [ ! -f "/usr/local/bin/kind" ]; then
 echo "Installing KIND"
 curl -Lo ./kind https://kind.sigs.k8s.io/dl/v0.14.0/kind-linux-amd64
 chmod +x ./kind
 sudo mv ./kind /usr/local/bin/kind
else
    echo "KIND already installed"
fi

CLUSTER_NAME=kcp

if ! kind get clusters | grep -w -q "${CLUSTER_NAME}"; then
kind create cluster --name kcp \
     --kubeconfig ./cluster.kubeconfig \
     --config ./hack/dev/kind/config.yaml
else
    echo "Cluster already exists"
fi

export KUBECONFIG=./cluster.kubeconfig

echo "Installing ingress"

kubectl apply -f https://gist.githubusercontent.com/mjudeikis/dd91434af0049378b4a24d021cceef38/raw/413600fe604bea2ccf4dcc2bd52375ebf863f35b/deploy
kubectl label nodes faros-control-plane node-role.kubernetes.io/control-plane-

echo "Waiting for the ingress controller to become ready..."
kubectl --context "${KUBECTL_CONTEXT}" -n ingress-nginx wait --for=condition=Ready pod -l app.kubernetes.io/component=controller --timeout=5m


echo "Installing cert-manager"

helm repo add jetstack https://charts.jetstack.io
helm repo update

kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.9.1/cert-manager.crds.yaml
helm install \
  --wait \
  cert-manager jetstack/cert-manager \
  --namespace cert-manager \
  --create-namespace \
  --version v1.9.1


echo "Install dex"

[ ! -d "./dev/dex-chart" ] && git clone https://github.com/faroshq/dex-helm-charts -b master ./dev/dex-chart

while ! helm upgrade -i dex ./dev/dex-chart/charts/dex \
     --values ./hack/dev/dex/values.yaml \
     --create-namespace \
     --namespace kcp \
     --wait \
     --set config.connectors[0].config.clientSecret=$GITHUB_CLIENT_SECRET \
     --set config.connectors[0].config.clientID=$GITHUB_CLIENT_ID
# we fail with network flakes, so lets retry. Once they goes through, it will be ok for the rest of calls
do
  echo "Try again"
  sleep 5
done

echo "Waiting for the ingress controller to become ready..."
kubectl --context "${KUBECTL_CONTEXT}" -n kcp wait --for=condition=Ready pod -l  app.kubernetes.io/name=dex --timeout=5m

echo "Install KCP"

mkdir -p ./dev
[ ! -d "./dev/kcp-chart" ] && git clone https://github.com/mjudeikis/helm-charts.git -b local.dev ./dev/kcp-chart

helm upgrade -i kcp ./dev/kcp-chart/charts/kcp \
     --values ./hack/dev/kcp/values.yaml \
     --set kcp.hostAliases.values[0].ip=$(kubectl get svc dex -n kcp -o json  | jq -r .spec.clusterIP) \
     --set kcpFrontProxy.hostAliases.values[0].ip=$(kubectl get svc dex -n kcp -o json  | jq -r .spec.clusterIP) \
     --set kcp.hostAliases.values[1].ip=$(kubectl get svc kcp-internal -n kcp -o json  | jq -r .spec.clusterIP) \
     --set kcpFrontProxy.hostAliases.values[1].ip=$(kubectl get svc kcp-internal -n kcp -o json  | jq -r .spec.clusterIP) \
     --namespace kcp \
     --create-namespace

echo "Generate KCP admin kubeconfig"
./hack/dev/generate-admin-kubeconfig.sh

echo "Check /etc/hosts for kcp.dev.faros.sh"
if ! grep -q kcp.dev.faros.sh /etc/hosts; then
    echo "127.0.0.1 kcp.dev.faros.sh" | sudo tee -a /etc/hosts
else
    echo "kcp.dev.faros.sh already exists in /etc/hosts"
fi

echo "Install Faros"

helm upgrade -i faros ./charts/faros-dev \
     --values ./hack/dev/faros/values.yaml \
     --namespace kcp


