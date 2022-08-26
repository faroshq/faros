package service

import (
	"fmt"
)

var (
	stringErrorFailure                      = "failure"
	stringErrorNamespaceNotFound            = "namespace not found"
	stringErrorNamespaceAlreadyExists       = "namespace already exists"
	stringErrorClusterNotFound              = "cluster not found"
	stringErrorClusterAlreadyExists         = "cluster already exists"
	stringErrorClusterAccessSessionNotFound = "cluster access session not found"
)

var (
	errorIDFormatInvalid = fmt.Errorf("invalid id format")
)
