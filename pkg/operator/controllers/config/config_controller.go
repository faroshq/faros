package config

// Copyright (c) Faros.sh.
// Licensed under the Apache License 2.0.

import (
	"context"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	farosv1alpha1 "github.com/faroshq/faros/pkg/operator/apis/faros.sh/v1alpha1"
	farosclient "github.com/faroshq/faros/pkg/operator/clientset/faros.sh/v1alpha1/versioned/typed/faros.sh/v1alpha1"
	"github.com/faroshq/faros/pkg/util/status"
)

const (
	ControllerName = "Config"
)

// Config reconciles a Config object
type Config struct {
	kubernetescli kubernetes.Interface
	faroscli      farosclient.FarosV1alpha1Interface
	log           logr.Logger
}

func NewReconciler(log logr.Logger, kubernetescli kubernetes.Interface, faroscli farosclient.FarosV1alpha1Interface) *Config {
	return &Config{
		kubernetescli: kubernetescli,
		faroscli:      faroscli,
		log:           log,
	}
}

type simpleHTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// This is the permissions that this controller needs to work.
// "make generate" will run kubebuilder and cause operator/deploy/staticresources/*/role.yaml to be updated
// from the annotation below.
// +kubebuilder:rbac:groups=faros.sh,resources=networks,verbs=get;list;watch
// +kubebuilder:rbac:groups=faros.sh,resources=networks/status,verbs=get;update;patch

// Reconcile will keep checking that the cluster can connect to essential services.
func (c *Config) Reconcile(request ctrl.Request) (ctrl.Result, error) {
	switch request.Name {
	case farosv1alpha1.SingletonClusterConfigObjectName, farosv1alpha1.SingletonHubConfigObjectName:
		return c.reconcileConfig(request)
	default:
		return reconcile.Result{}, nil
	}
}

func (c *Config) reconcileConfig(request ctrl.Request) (ctrl.Result, error) {
	_, err := c.faroscli.Configs().Get(context.TODO(), request.Name, metav1.GetOptions{})
	if err != nil {
		return reconcile.Result{}, err
	}

	var condition status.ConditionType
	condition = farosv1alpha1.Healthy

	err = setCondition(context.TODO(), c.faroscli, &status.Condition{
		Type:    condition,
		Status:  corev1.ConditionTrue,
		Message: "Config controller healthy",
		Reason:  "Started",
	})
	if err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{RequeueAfter: time.Minute, Requeue: true}, nil
}

// SetupWithManager setup our mananger
func (c *Config) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&farosv1alpha1.Config{}).
		Named(ControllerName).
		Complete(c)
}
