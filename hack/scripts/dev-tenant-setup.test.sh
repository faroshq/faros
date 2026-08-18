#!/usr/bin/env bash
# Static regression checks for the local tenant bootstrap boundary. These
# checks deliberately do not contact a hub or require kubectl credentials.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCRIPT="${ROOT_DIR}/hack/scripts/dev-tenant-setup.sh"
ENV_EXAMPLE="${ROOT_DIR}/hack/dev/tenant-bootstrap.env.example"
TILTFILE="${ROOT_DIR}/Tiltfile.cluster"

bash -n "${SCRIPT}"

if grep -Eq 'curl[^\n]*[[:space:]](-k|--insecure)' "${SCRIPT}"; then
  echo "tenant setup must not invoke curl with unconditional insecure TLS" >&2
  exit 1
fi
if grep -Fq -- '--from-literal' "${SCRIPT}"; then
  echo "tenant setup must not put credential values in kubectl arguments" >&2
  exit 1
fi
if grep -Eq 'curl[^\n]*(STATIC_TOKEN|FAROS_BOOTSTRAP_(GITHUB|LLM|DATABRICKS).*TOKEN)' "${SCRIPT}"; then
  echo "tenant setup must not pass a credential-bearing argument to curl" >&2
  exit 1
fi

grep -Fq -- '--header @-' "${SCRIPT}"
grep -Fq -- 'chmod 600' "${SCRIPT}"
grep -Fq -- 'FAROS_BOOTSTRAP_CA_CERT' "${SCRIPT}"
grep -Fq -- 'FAROS_BOOTSTRAP_HUB_URL must be an HTTPS URL' "${SCRIPT}"
grep -Fq -- 'wait_for_provider_binding' "${SCRIPT}"
grep -Fq -- 'FAROS_BOOTSTRAP_INSECURE_TLS=true' "${ENV_EXAMPLE}"
grep -Fq -- 'FAROS_BOOTSTRAP_CA_CERT=' "${ENV_EXAMPLE}"
grep -Fq -- "resource_deps=['faros-hub', 'code-init', 'databricks-init']" "${TILTFILE}"
grep -Fq -- "resource_deps=['faros-hub', 'edges-init']" "${TILTFILE}"
grep -Fq -- "resource_deps=['faros-hub', 'agents-db', 'agents-init']" "${TILTFILE}"
grep -Fq -- "'.kcp/edges-runtime.kubeconfig'" "${TILTFILE}"
grep -Fq -- "'.kcp/agents-provider.kubeconfig'" "${TILTFILE}"
grep -Fq -- "http_get=http_get_action(port=8088, path='/readyz')" "${TILTFILE}"
grep -Fq -- "http_get=http_get_action(port=8087, path='/readyz')" "${TILTFILE}"
grep -Fq -- 'FAROS_PROVIDER_KUBECONFIG=$${FAROS_PROVIDER_KUBECONFIG:-$(AGENTS_PROVIDER_KUBECONFIG)}' "${ROOT_DIR}/Makefile"

echo "dev tenant setup static checks passed"
