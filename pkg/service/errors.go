package service

import (
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// Errors abstracts default k8s error types with local ones for better readability.
// github.com/kcp-dev/kubernetes/staging/src/k8s.io/apiserver/pkg/endpoints/handlers/responsewriters/status.go

func errInternalServerError(message string) error {
	return apierrors.NewInternalError(fmt.Errorf(message))
}

func errBadRequest(message string) error {
	return apierrors.NewBadRequest(message)
}

func errNotFound(message string) error {
	return apierrors.NewNotFound(schema.GroupResource{}, message)
}

func errForbidden(message string) error {
	return apierrors.NewForbidden(schema.GroupResource{}, message, nil)
}

func errConflict(message string) error {
	return apierrors.NewConflict(schema.GroupResource{}, message, nil)
}
