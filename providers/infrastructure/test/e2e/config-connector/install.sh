#!/usr/bin/env bash

# Copyright 2026 The Faros Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0

set -euo pipefail

readonly KCC_VERSION="1.153.0"
readonly KCC_BUNDLE_URL="https://storage.googleapis.com/configconnector-operator/${KCC_VERSION}/release-bundle.tar.gz"
readonly KCC_BUNDLE_SHA256="e2e46bb51638a39dbbf6e28f35260890d8bc32c4fccc455a02b34c947221e3f2"
readonly KCC_NAMESPACE="cnrm-system"
readonly KCC_SECRET="gsa-key"
readonly KCC_NAME="configconnector.core.cnrm.cloud.google.com"
readonly PUBSUB_CRD="pubsubtopics.pubsub.cnrm.cloud.google.com"

: "${FAROS_E2E_TILT_RUNTIME_KUBECONFIG:?FAROS_E2E_TILT_RUNTIME_KUBECONFIG is required}"
: "${FAROS_E2E_GCP_CREDENTIALS_FILE:?FAROS_E2E_GCP_CREDENTIALS_FILE is required}"

if [[ ! -f "${FAROS_E2E_TILT_RUNTIME_KUBECONFIG}" ]]; then
  echo "runtime kubeconfig does not exist" >&2
  exit 1
fi
if [[ ! -f "${FAROS_E2E_GCP_CREDENTIALS_FILE}" ]]; then
  echo "GCP credentials file does not exist" >&2
  exit 1
fi

for command in curl kubectl sha256sum tar; do
  command -v "${command}" >/dev/null || {
    echo "${command} is required" >&2
    exit 1
  }
done

task_tmp="$(mktemp -d)"
trap 'rm -rf "${task_tmp}"' EXIT
bundle="${task_tmp}/release-bundle.tar.gz"

echo ">>> downloading pinned Config Connector ${KCC_VERSION} bundle"
curl -fsSL "${KCC_BUNDLE_URL}" -o "${bundle}"
echo "${KCC_BUNDLE_SHA256}  ${bundle}" | sha256sum -c -
tar -xzf "${bundle}" -C "${task_tmp}"

kc=(kubectl --kubeconfig "${FAROS_E2E_TILT_RUNTIME_KUBECONFIG}")

echo ">>> applying Config Connector operator ${KCC_VERSION}"
"${kc[@]}" apply -f "${task_tmp}/operator-system/configconnector-operator.yaml"
"${kc[@]}" wait --for=condition=Established \
  crd/configconnectors.core.cnrm.cloud.google.com --timeout=2m
"${kc[@]}" -n configconnector-operator-system rollout status \
  statefulset/configconnector-operator --timeout=5m

echo ">>> importing the test service-account key into ${KCC_NAMESPACE}/${KCC_SECRET}"
"${kc[@]}" create namespace "${KCC_NAMESPACE}" --dry-run=client -o yaml | "${kc[@]}" apply -f -
"${kc[@]}" -n "${KCC_NAMESPACE}" create secret generic "${KCC_SECRET}" \
  --from-file="key.json=${FAROS_E2E_GCP_CREDENTIALS_FILE}" \
  --dry-run=client -o yaml | "${kc[@]}" apply -f -

echo ">>> configuring Config Connector cluster mode"
"${kc[@]}" apply -f - <<EOF
apiVersion: core.cnrm.cloud.google.com/v1beta1
kind: ConfigConnector
metadata:
  name: ${KCC_NAME}
spec:
  mode: cluster
  credentialSecretName: ${KCC_SECRET}
  stateIntoSpec: Absent
EOF

"${kc[@]}" wait --for=jsonpath='{.status.healthy}'=true \
  "configconnector/${KCC_NAME}" --timeout=10m

echo ">>> waiting for Config Connector controllers and PubSubTopic CRD"
for _ in $(seq 1 120); do
  if "${kc[@]}" -n "${KCC_NAMESPACE}" get pods -o name 2>/dev/null | grep -q .; then
    break
  fi
  sleep 5
done
if ! "${kc[@]}" -n "${KCC_NAMESPACE}" get pods -o name 2>/dev/null | grep -q .; then
  echo "Config Connector controller pods were not created" >&2
  exit 1
fi
"${kc[@]}" -n "${KCC_NAMESPACE}" wait --for=condition=Ready pod --all --timeout=10m

for _ in $(seq 1 120); do
  if "${kc[@]}" get "crd/${PUBSUB_CRD}" >/dev/null 2>&1; then
    break
  fi
  sleep 5
done
"${kc[@]}" wait --for=condition=Established "crd/${PUBSUB_CRD}" --timeout=2m

echo ">>> Config Connector ${KCC_VERSION} is ready for the Pub/Sub E2E"
