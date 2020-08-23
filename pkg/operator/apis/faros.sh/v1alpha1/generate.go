package v1alpha1

// Copyright (c) Faros.sh.
// Licensed under the Apache License 2.0.

// build the kubernetes client
//go:generate go run ../../../../../vendor/sigs.k8s.io/controller-tools/cmd/controller-gen object paths=.
//go:generate sh -c "cd $GOPATH/src && go run github.com/faroshq/faros/vendor/k8s.io/code-generator/cmd/client-gen --clientset-name versioned --input-base github.com/faroshq/faros/pkg/operator/apis --input faros.sh/v1alpha1 --output-package github.com/faroshq/faros/pkg/operator/clientset/faros.sh/v1alpha1 --go-header-file github.com/faroshq/faros/hack/licenses/boilerplate.go.txt"
