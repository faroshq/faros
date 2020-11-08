package cluster

// Copyright (c) Faros.sh.
// Licensed under the Apache License 2.0.

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	farosv1alpha1 "github.com/faroshq/faros/pkg/operator/apis/faros.sh/v1alpha1"
	farosclient "github.com/faroshq/faros/pkg/operator/clientset/faros.sh/v1alpha1/versioned/typed/faros.sh/v1alpha1"
	"github.com/faroshq/faros/pkg/util/status"
)

const (
	ControllerName = "Clusters"

	kubeConfigSecretKey = "kubeconfig"
)

// Cluster reconciles a Cluster object
type Cluster struct {
	kubernetescli kubernetes.Interface
	faroscli      farosclient.FarosV1alpha1Interface
	log           logr.Logger
}

func NewReconciler(log logr.Logger, kubernetescli kubernetes.Interface, faroscli farosclient.FarosV1alpha1Interface) (*Cluster, error) {
	return &Cluster{
		kubernetescli: kubernetescli,
		faroscli:      faroscli,
		log:           log,
	}, nil
}

// Reconcile will keep checking that the cluster can connect to essential services.
func (c *Cluster) Reconcile(request ctrl.Request) (ctrl.Result, error) {
	return c.reconcileConfig(request)
}

func (c *Cluster) reconcileConfig(request ctrl.Request) (ctrl.Result, error) {
	cluster, err := c.faroscli.Clusters(request.Namespace).Get(context.TODO(), request.Name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			err = nil
		}
		return reconcile.Result{}, err
	}
	if cluster.Spec.KubeConfigSecret.Name == "" ||
		cluster.Spec.KubeConfigSecret.Namespace == "" {
		return reconcile.Result{}, fmt.Errorf("cluster.Spec.KubeConfigSecret fields are invalid")
	}

	var condition status.ConditionType
	condition = farosv1alpha1.Healthy

	// Cluster object reconcile flow:
	// 1. Check if kubeconfig secret exists, if so app owner metadata
	// 2. Check if kubeconfig is valid and we can reach cluster
	err = c.checkKubeConfigSecret(cluster.Spec.KubeConfigSecret)
	if err != nil {
		err = setCondition(context.TODO(), c.faroscli, request, &status.Condition{
			Type:    condition,
			Status:  corev1.ConditionFalse,
			Message: err.Error(),
			Reason:  "Cluster access failed",
		})
		return reconcile.Result{Requeue: true}, err
	}

	// TODO: All these secConditions are asking for refactor together with other
	// status fields
	err = setCondition(context.TODO(), c.faroscli, request, &status.Condition{
		Type:    condition,
		Status:  corev1.ConditionTrue,
		Message: "Monitor can access the cluster",
		Reason:  "Verified",
	})
	if err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{RequeueAfter: time.Minute, Requeue: true}, nil
}

func (c *Cluster) checkKubeConfigSecret(secretRef corev1.SecretReference) error {
	secret, err := c.kubernetescli.CoreV1().Secrets(secretRef.Namespace).Get(context.TODO(), secretRef.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if val, ok := secret.Data[kubeConfigSecretKey]; ok {
		restConfig, err := clientcmd.RESTConfigFromKubeConfig(val)
		if err != nil {
			return err
		}

		return verifyRestConfig(restConfig)
	}

	return fmt.Errorf("key %s not found in secret %s/%s", kubeConfigSecretKey, secretRef.Namespace, secretRef.Name)
}

func verifyRestConfig(restConfig *restclient.Config) error {
	kubernetescli, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return err
	}
	_, err = kubernetescli.CoreV1().Namespaces().Get(context.TODO(), "kube-system", metav1.GetOptions{})
	if err != nil {
		return err
	}

	return nil
}

// SetupWithManager setup our mananger
func (c *Cluster) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&farosv1alpha1.Cluster{}).
		Named(ControllerName).
		Complete(c)
}
