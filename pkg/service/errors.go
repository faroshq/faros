package service

import (
	"fmt"
)

var (
	stringErrorFailure                      = "failure"
	stringErrorNamespaceNotFound            = "namespace not found"
	stringErrorClusterNotFound              = "cluster not found"
	stringErrorClusterAccessSessionNotFound = "cluster access session not found"
)

var (
	errorIDFormatInvalid = fmt.Errorf("invalid id format")
)
