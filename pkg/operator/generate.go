package operator

// Copyright (c) Faros.sh.
// Licensed under the Apache License 2.0.

// build the operator's CRD (based on the apis)
// for deployment
//go:generate go run ../../vendor/sigs.k8s.io/controller-tools/cmd/controller-gen "crd:trivialVersions=true" paths="./apis/..." output:crd:dir=deploy/staticresources

// bindata for the above yaml files
//go:generate go run ../../vendor/github.com/go-bindata/go-bindata/go-bindata -nometadata -pkg deploy -prefix deploy/staticresources/ -o deploy/bindata.go deploy/staticresources/...
//go:generate gofmt -s -l -w deploy/bindata.go

//go:generate gofmt -s -w ./clientset
//go:generate go run ../../vendor/golang.org/x/tools/cmd/goimports -w -local=github.com/faroshq/faros ./clientset
