// Copyright 2026 The Faros Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package scheme

import (
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	apiskcpv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
	apiskcpv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha2"
	corev1alpha1 "github.com/kcp-dev/sdk/apis/core/v1alpha1"

	deploymentsv1alpha1 "github.com/faroshq/provider-deployments/apis/v1alpha1"
)

func New() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(corev1alpha1.AddToScheme(s))
	utilruntime.Must(apiskcpv1alpha1.AddToScheme(s))
	utilruntime.Must(apiskcpv1alpha2.AddToScheme(s))
	utilruntime.Must(deploymentsv1alpha1.AddToScheme(s))
	return s
}
