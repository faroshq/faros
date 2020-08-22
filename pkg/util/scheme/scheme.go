package scheme

// Copyright (c) Faros.sh.
// Licensed under the Apache License 2.0.

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"

	farosv1alpha1 "github.com/faroshq/faros/pkg/operator/apis/operator.faros.sh/v1alpha1"
)

func init() {
	runtime.Must(apiextensionsv1beta1.AddToScheme(scheme.Scheme))
	runtime.Must(apiextensionsv1.AddToScheme(scheme.Scheme))
	runtime.Must(farosv1alpha1.AddToScheme(scheme.Scheme))
}
