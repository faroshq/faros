package deploy

// Copyright (c) Faros.sh.
// Licensed under the Apache License 2.0.

import (
	"context"
	"sort"
	"time"

	"go.uber.org/zap"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	extv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	extensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"

	pkgoperator "github.com/faroshq/faros/pkg/operator"
	farosv1alpha1 "github.com/faroshq/faros/pkg/operator/apis/faros.sh/v1alpha1"
	farosclient "github.com/faroshq/faros/pkg/operator/clientset/faros.sh/v1alpha1/versioned/typed/faros.sh/v1alpha1"
	"github.com/faroshq/faros/pkg/util/dynamichelper"
	"github.com/faroshq/faros/pkg/util/ready"
)

type Operator interface {
	CreateOrUpdate(ctx context.Context) error
	IsReady(ctx context.Context) (bool, error)
}

type operator struct {
	log *zap.Logger

	dh       dynamichelper.DynamicHelper
	cli      kubernetes.Interface
	extcli   extensionsclient.Interface
	faroscli farosclient.FarosV1alpha1Interface
}

func New(log *zap.Logger, cli kubernetes.Interface, extcli extensionsclient.Interface, faroscli farosclient.FarosV1alpha1Interface) (Operator, error) {
	restConfig, err := ctrl.GetConfig()
	if err != nil {
		return nil, err
	}
	dh, err := dynamichelper.New(log, restConfig)
	if err != nil {
		return nil, err
	}

	return &operator{
		log: log,

		dh:       dh,
		cli:      cli,
		extcli:   extcli,
		faroscli: faroscli,
	}, nil
}

func (o *operator) resources() ([]runtime.Object, error) {
	// first static resources from Assets
	results := []runtime.Object{}
	for _, assetName := range AssetNames() {
		b, err := Asset(assetName)
		if err != nil {
			return nil, err
		}

		obj, _, err := scheme.Codecs.UniversalDeserializer().Decode(b, nil, nil)
		if err != nil {
			return nil, err
		}

		// set the image for the deployments
		if d, ok := obj.(*appsv1.Deployment); ok {
			for i := range d.Spec.Template.Spec.Containers {
				// TODO: Move to future config package
				d.Spec.Template.Spec.Containers[i].Image = "quay.io/faroshq/faros-operator:latest"
			}
		}

		results = append(results, obj)
	}

	// create a config object to star the operator.
	return append(results,
		&farosv1alpha1.Config{
			ObjectMeta: metav1.ObjectMeta{
				Name: farosv1alpha1.SingletonObjectName,
			},
			Spec: farosv1alpha1.ConfigSpec{
				// TODO: Move to future config package
				Name:     "test",
				Location: "location",
			},
		},
	), nil
}

func (o *operator) CreateOrUpdate(ctx context.Context) error {
	resources, err := o.resources()
	if err != nil {
		return err
	}

	uns := make([]*unstructured.Unstructured, 0, len(resources))
	for _, res := range resources {
		un := &unstructured.Unstructured{}
		err = scheme.Scheme.Convert(res, un, nil)
		if err != nil {
			return err
		}
		uns = append(uns, un)
	}

	sort.Slice(uns, func(i, j int) bool {
		return dynamichelper.CreateOrder(uns[i], uns[j])
	})

	for _, un := range uns {
		err = o.dh.Ensure(ctx, un)

		switch un.GroupVersionKind().GroupKind().String() {
		case "CustomResourceDefinition.apiextensions.k8s.io":
			err = wait.PollImmediate(time.Second, time.Minute, func() (bool, error) {
				crd, err := o.extcli.ApiextensionsV1beta1().CustomResourceDefinitions().Get(ctx, un.GetName(), metav1.GetOptions{})
				if err != nil {
					return false, err
				}

				return isCRDEstablished(crd), nil
			})
			if err != nil {
				return err
			}

			err = o.dh.RefreshAPIResources()
			if err != nil {
				return err
			}

		case "Cluster.config.faros.sh":
			// add an owner reference onto our configuration secret.  This is
			// can only be done once we've got the cluster UID.  It is needed to
			// ensure that secret updates trigger updates of the appropriate
			// controllers

			// TODO: Boilerplate for faros secret for push model
			//err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			//	cluster, err := o.faroscli.Clusters().Get(ctx, farosv1alpha1.SingletonClusterName, metav1.GetOptions{})
			//	if err != nil {
			//		return err
			//	}
			//
			//	// TODO: add secret ref reading
			//	s, err := o.cli.CoreV1().Secrets(pkgoperator.Namespace).Get(ctx, pkgoperator.SecretName, metav1.GetOptions{})
			//	if err != nil {
			//		return err
			//	}
			//
			//	err = controllerutil.SetControllerReference(cluster, s, scheme.Scheme)
			//	if err != nil {
			//		return err
			//	}
			//
			//	_, err = o.cli.CoreV1().Secrets(pkgoperator.Namespace).Update(ctx, s, metav1.UpdateOptions{})
			//	return err
			//})
			//if err != nil {
			//	return err
			//}
		}
	}
	return nil
}

func (o *operator) IsReady(ctx context.Context) (bool, error) {
	ok, err := ready.CheckDeploymentIsReady(ctx, o.cli.AppsV1().Deployments(pkgoperator.Namespace), "faros-operator")()
	if !ok || err != nil {
		return ok, err
	}

	// wait for conditions to appear
	cluster, err := o.faroscli.Configs().Get(ctx, farosv1alpha1.SingletonObjectName, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	for _, ct := range farosv1alpha1.AllConditionTypes() {
		cond := cluster.Status.Conditions.GetCondition(ct)
		if cond == nil {
			return false, nil
		}
		if cond.Status != corev1.ConditionTrue {
			return false, nil
		}
	}
	return true, nil
}

func isCRDEstablished(crd *extv1beta1.CustomResourceDefinition) bool {
	m := make(map[extv1beta1.CustomResourceDefinitionConditionType]extv1beta1.ConditionStatus, len(crd.Status.Conditions))
	for _, cond := range crd.Status.Conditions {
		m[cond.Type] = cond.Status
	}
	return m[extv1beta1.Established] == extv1beta1.ConditionTrue &&
		m[extv1beta1.NamesAccepted] == extv1beta1.ConditionTrue
}
