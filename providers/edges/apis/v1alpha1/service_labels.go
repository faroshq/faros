// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package v1alpha1

import (
	"crypto/sha256"
	"encoding/hex"

	"k8s.io/apimachinery/pkg/util/validation"
)

// ServiceEdgeLabelValue preserves the existing label for ordinary edge names,
// and represents longer names within Kubernetes' 63-character label limit.
// The spec remains authoritative; readers also compare spec.edgeRef.name.
func ServiceEdgeLabelValue(edgeName string) string {
	if len(validation.IsValidLabelValue(edgeName)) == 0 {
		return edgeName
	}
	digest := sha256.Sum256([]byte(edgeName))
	return "sha256-" + hex.EncodeToString(digest[:])[:56]
}
