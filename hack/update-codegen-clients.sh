#!/usr/bin/env bash

# Copyright 2017 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -o errexit
set -o nounset
set -o pipefail
set -o xtrace

export GOPATH=$(go env GOPATH)

SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
pushd "${SCRIPT_ROOT}"
BOILERPLATE_HEADER="$( pwd )/hack/boilerplate/boilerplate.go.txt"
popd
CODEGEN_PKG=${CODEGEN_PKG:-$(cd "${SCRIPT_ROOT}"; go list -f '{{.Dir}}' -m k8s.io/code-generator)}
echo "output: ${SCRIPT_ROOT}"

# generate the code with:
# --output-base    because this script should also be able to run inside the vendor dir of
#                  k8s.io/kubernetes. The output-base is needed for the generators to output into the vendor dir
#                  instead of the $GOPATH directly. For normal projects this can be dropped.
bash "${CODEGEN_PKG}"/generate-groups.sh "deepcopy,client,informers,listers" \
  github.com/faroshq/faros/pkg/client github.com/faroshq/faros/pkg/apis \
  "tenancy:v1alpha1" \
  --output-base "${SCRIPT_ROOT}" \
  --go-header-file "${BOILERPLATE_HEADER}" \
  --trim-path-prefix github.com/faroshq/faros

pushd ./pkg/apis
${CODE_GENERATOR} \
  "client:outputPackagePath=github.com/faroshq/faros/pkg/client,apiPackagePath=github.com/faroshq/faros/pkg/apis,singleClusterClientPackagePath=github.com/faroshq/faros/pkg/client/clientset/versioned,headerFile=${BOILERPLATE_HEADER}" \
  "lister:apiPackagePath=github.com/faroshq/faros/pkg/apis,headerFile=${BOILERPLATE_HEADER}" \
  "informer:outputPackagePath=github.com/faroshq/faros/pkg/client,singleClusterClientPackagePath=github.com/faroshq/faros/pkg/client/clientset/versioned,apiPackagePath=github.com/faroshq/faros/pkg/apis,headerFile=${BOILERPLATE_HEADER}" \
  "paths=./..." \
  "output:dir=./../client"
popd


go install "${CODEGEN_PKG}"/cmd/openapi-gen

"$GOPATH"/bin/openapi-gen --input-dirs github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1 \
--input-dirs github.com/kcp-dev/kcp/pkg/apis/third_party/conditions/apis/conditions/v1alpha1 \
--input-dirs k8s.io/apimachinery/pkg/apis/meta/v1,k8s.io/apimachinery/pkg/runtime,k8s.io/apimachinery/pkg/version \
--output-package github.com/faroshq/faros/pkg/openapi -O zz_generated.openapi \
--go-header-file ./hack/../hack/boilerplate/boilerplate.generatego.txt \
--output-base "${SCRIPT_ROOT}" \
--trim-path-prefix github.com/faroshq/faros
