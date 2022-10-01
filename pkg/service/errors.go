package service

import (
	"fmt"
)

var (
	stringErrorFailure                               = "failure"
	stringErrorNamespaceNotFound                     = "namespace not found"
	stringErrorClusterNotFound                       = "cluster not found"
	stringErrorClusterAccessSessionNotFound          = "cluster access session not found"
	stringErrorClusterRegistrationTokenNotFound      = "cluster registration token not found"
	stringErrorClusterRegistrationAlreadyExistsFound = "cluster registration token for this cluster already exists, please delete and try again"
)

var (
	errorIDFormatInvalid = fmt.Errorf("invalid id format")
)
