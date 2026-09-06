#!/usr/bin/env bash
# Copyright 2026 The Faros Authors.
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail

: "${EDGES_REPLICA_TEST_KUBECONFIG:?set an explicit local Kubernetes or KCP kubeconfig}"
EDGES_REPLICA_TEST_KUBECONFIG=$(realpath "$EDGES_REPLICA_TEST_KUBECONFIG")
export EDGES_REPLICA_TEST_KUBECONFIG
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
artifact_dir=${FAROS_TEST_ARTIFACT_DIR:-$(mktemp -d "${TMPDIR:-/var/tmp}/faros-replica-contract.XXXXXX")}
mkdir -p "$artifact_dir"
artifact_dir=$(cd "$artifact_dir" && pwd)
result_file="$artifact_dir/edges-replica.jsonl"

if ! (cd "$repo_root/providers/edges" && go test -race -count=1 -json ./internal/tunnel -run '^TestRegistryReplicaConvergenceLive$') >"$result_file"; then
  cat "$result_file"
  exit 1
fi
python3 "$repo_root/hack/verify-required-go-tests.py" "$result_file"
