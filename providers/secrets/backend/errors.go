/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package backend

import (
	"context"
	"errors"
	"fmt"
	"net"

	secretsv1alpha1 "github.com/faroshq/provider-secrets/apis/v1alpha1"
)

// StatusError is the error backends return for HTTP-shaped failures. Message
// must already be safe to surface on a CR status: bounded and body-free
// (response bodies can echo secret material or internal detail).
type StatusError struct {
	// Code is the HTTP status code, 0 when the failure never got a response.
	Code int
	// Message is the safe, human-readable summary.
	Message string
}

func (e *StatusError) Error() string {
	if e.Code == 0 {
		return e.Message
	}
	return fmt.Sprintf("%s (HTTP %d)", e.Message, e.Code)
}

// HTTPStatusCode implements the classification seam ClassifyError keys off —
// the same httpStatusCoder convention the databricks provider uses.
func (e *StatusError) HTTPStatusCode() int { return e.Code }

type httpStatusCoder interface{ HTTPStatusCode() int }

// ClassifyError maps a backend error onto the stable condition reasons
// declared in the API package, so portals can key UX off status.reason
// regardless of which backend produced the failure.
func ClassifyError(err error) string {
	var coder httpStatusCoder
	if errors.As(err, &coder) {
		switch code := coder.HTTPStatusCode(); {
		case code == 401 || code == 403:
			return secretsv1alpha1.ReasonAccessDenied
		case code == 404:
			return secretsv1alpha1.ReasonPathNotFound
		case code == 408 || code == 429 || code >= 500:
			return secretsv1alpha1.ReasonStoreUnavailable
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) || errors.Is(err, context.DeadlineExceeded) {
		return secretsv1alpha1.ReasonStoreUnavailable
	}
	return secretsv1alpha1.ReasonValidationFailed
}
