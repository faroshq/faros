package internetchecker

// Copyright (c) Faros.sh.
// Licensed under the Apache License 2.0.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	farosv1alpha1 "github.com/faroshq/faros/pkg/operator/apis/operator.faros.sh/v1alpha1"
	farosclient "github.com/faroshq/faros/pkg/operator/clientset/versioned/typed/operator.faros.sh/v1alpha1"
	"github.com/faroshq/faros/pkg/operator/controllers"
	"github.com/faroshq/faros/pkg/util/status"
)

// InternetChecker reconciles a Cluster object
type InternetChecker struct {
	kubernetescli kubernetes.Interface
	faroscli      farosclient.OperatorV1alpha1Interface
	log           logr.Logger
	role          string
}

func NewReconciler(log logr.Logger, kubernetescli kubernetes.Interface, faroscli farosclient.OperatorV1alpha1Interface) *InternetChecker {
	return &InternetChecker{
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
// +kubebuilder:rbac:groups=operator.faros.sh,resources=clusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=operator.faros.sh,resources=clusters/status,verbs=get;update;patch

// Reconcile will keep checking that the cluster can connect to essential services.
func (r *InternetChecker) Reconcile(request ctrl.Request) (ctrl.Result, error) {
	if request.Name != farosv1alpha1.SingletonClusterName {
		return reconcile.Result{}, nil
	}

	instance, err := r.faroscli.Clusters().Get(context.TODO(), request.Name, metav1.GetOptions{})
	if err != nil {
		return reconcile.Result{}, err
	}

	var condition status.ConditionType
	condition = farosv1alpha1.InternetReachable

	urlErrors := map[string]string{}
	for _, url := range instance.Spec.InternetChecker.URLs {
		err = r.check(&http.Client{}, url)
		if err != nil {
			urlErrors[url] = err.Error()
		}
	}

	if len(urlErrors) > 0 {
		sb := &strings.Builder{}
		for url, err := range urlErrors {
			fmt.Fprintf(sb, "%s: %s\n", url, err)
		}
		err = controllers.SetCondition(context.TODO(), r.faroscli, &status.Condition{
			Type:    condition,
			Status:  corev1.ConditionFalse,
			Message: sb.String(),
			Reason:  "CheckFailed",
		})
	} else {
		err = controllers.SetCondition(context.TODO(), r.faroscli, &status.Condition{
			Type:    condition,
			Status:  corev1.ConditionTrue,
			Message: "Outgoing connection successful.",
			Reason:  "CheckDone",
		})
	}
	if err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{RequeueAfter: time.Minute, Requeue: true}, nil
}

func (r *InternetChecker) check(client simpleHTTPClient, url string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

// SetupWithManager setup our mananger
func (r *InternetChecker) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&farosv1alpha1.Cluster{}).
		Named(controllers.InternetCheckerControllerName).
		Complete(r)
}
