#!/usr/bin/env bash
# Copyright 2026 The Faros Authors.
# SPDX-License-Identifier: Apache-2.0
set -euo pipefail

# Dedicated CI/local gate: optional unit-test skips must never turn this job green.
: "${AGENTS_TEST_POSTGRES_DSN:?set a disposable Agents PostgreSQL test DSN}"
: "${APP_STUDIO_TEST_POSTGRES_DSN:?set a disposable App Studio PostgreSQL test DSN}"
: "${KUERY_TEST_POSTGRES_DSN:?set a disposable Kuery PostgreSQL test DSN}"

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
artifact_dir=${FAROS_TEST_ARTIFACT_DIR:-$(mktemp -d "${TMPDIR:-/var/tmp}/faros-postgres-contracts.XXXXXX")}
mkdir -p "$artifact_dir"
artifact_dir=$(cd "$artifact_dir" && pwd)

for provider in agents app-studio kuery; do
  package=./store
  if [ "$provider" = kuery ]; then
    package=./tenantindex
  fi
  result_file="$artifact_dir/$provider.jsonl"
  echo "Running $provider PostgreSQL contracts"
  if ! (cd "$repo_root/providers/$provider" && go test -race -count=1 -json "$package" -run '^TestPostgres') >"$result_file"; then
    cat "$result_file"
    exit 1
  fi
  python3 "$repo_root/hack/verify-required-go-tests.py" "$result_file"
done
