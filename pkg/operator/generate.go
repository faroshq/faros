package operator

// Copyright (c) Faros.sh.
// Licensed under the Apache License 2.0.

// build the kubenetes client
//go:generate go run ../../vendor/sigs.k8s.io/controller-tools/cmd/controller-gen object paths=./apis/operator.faros.sh/v1alpha1
//go:generate sh -c "cd ../../../../.. && go run github.com/faroshq/faros/vendor/k8s.io/code-generator/cmd/client-gen --clientset-name versioned --input-base github.com/faroshq/faros/pkg/operator/apis --input operator.faros.sh/v1alpha1 --output-package github.com/faroshq/faros/pkg/operator/clientset --go-header-file github.com/faroshq/faros/hack/licenses/boilerplate.go.txt"
//go:generate gofmt -s -w ./clientset
//go:generate go run ../../vendor/golang.org/x/tools/cmd/goimports -w -local=github.com/faroshq/faros ./clientset

// build the operator's CRD (based on the apis)
// for deployment
//go:generate go run ../../vendor/sigs.k8s.io/controller-tools/cmd/controller-gen "crd:trivialVersions=true" paths="./apis/..." output:crd:dir=deploy/staticresources

// bindata for the above yaml files
//go:generate go run ../../vendor/github.com/go-bindata/go-bindata/go-bindata -nometadata -pkg deploy -prefix deploy/staticresources/ -o deploy/bindata.go deploy/staticresources/...
//go:generate gofmt -s -l -w deploy/bindata.go
