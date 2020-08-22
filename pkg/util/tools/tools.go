// +build tools

// tools is a dummy package that will be ignored for builds, but included for dependencies
package tools

import (
	_ "github.com/go-bindata/go-bindata/go-bindata"
	_ "github.com/onsi/ginkgo"
	_ "github.com/onsi/gomega"
	_ "golang.org/x/tools/cmd/goimports"
	_ "k8s.io/code-generator/cmd/client-gen"
	_ "sigs.k8s.io/controller-tools/cmd/controller-gen"
)
