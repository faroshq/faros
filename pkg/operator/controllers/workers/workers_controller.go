package cluster

// Copyright (c) Faros.sh.
// Licensed under the Apache License 2.0.

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	farosv1alpha1 "github.com/faroshq/faros/pkg/operator/apis/faros.sh/v1alpha1"
	farosclient "github.com/faroshq/faros/pkg/operator/clientset/faros.sh/v1alpha1/versioned/typed/faros.sh/v1alpha1"
	"github.com/faroshq/faros/pkg/util/recover"
	"github.com/faroshq/faros/pkg/util/status"
)

const (
	ControllerName = "Workers"
)

// Worker reconciles a Worker object
type Worker struct {
	kubernetescli kubernetes.Interface
	faroscli      farosclient.FarosV1alpha1Interface
	log           logr.Logger
	name          string
}

func NewReconciler(ctx context.Context, log logr.Logger, name string, kubernetescli kubernetes.Interface, faroscli farosclient.FarosV1alpha1Interface) (*Worker, error) {
	w := &Worker{
		kubernetescli: kubernetescli,
		faroscli:      faroscli,
		log:           log,
		name:          name,
	}
	return w, w.registerWorker(ctx, name)
}

// registerWorker registers worker into API
func (w *Worker) registerWorker(ctx context.Context, name string) error {
	w.log.Info("registering worker", "name", name)
	worker := &farosv1alpha1.Worker{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		_, err := w.faroscli.Workers().Create(ctx, worker, metav1.CreateOptions{})
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		w.log.Error(err, "error while registering worker")
		return err
	}

	go w.ping(ctx, name)

	return nil
}

// Reconcile will keep checking that the cluster can connect to essential services.
func (w *Worker) Reconcile(request ctrl.Request) (ctrl.Result, error) {
	return w.reconcile(request)
}

func (w *Worker) reconcile(request ctrl.Request) (ctrl.Result, error) {
	_, err := w.faroscli.Workers().Get(context.TODO(), request.Name, metav1.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			err = nil
		}
		return reconcile.Result{}, err
	}

	// TODO: All these secConditions are asking for refactor together with other
	// status fields
	err = setCondition(context.TODO(), w.faroscli, request, &status.Condition{
		Type:    farosv1alpha1.Healthy,
		Status:  corev1.ConditionTrue,
		Message: "Worker accepted",
		Reason:  "Verified",
	})
	if err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{RequeueAfter: 2 * time.Minute, Requeue: true}, nil
}

// SetupWithManager setup our mananger
func (w *Worker) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&farosv1alpha1.Worker{}).
		Named(ControllerName).
		Complete(w)
}

// ping updated health status of only 1 worker
func (w *Worker) ping(ctx context.Context, name string) error {
	defer recover.PanicLogr(w.log)

	t := time.NewTicker(2 * time.Minute)
	defer t.Stop()

	for {
		worker, err := w.faroscli.Workers().Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			w.log.Error(err, "error healthStatus get")
		}
		wCopy := worker.DeepCopy()

		wCopy.Status.LastHeartbeatTime = &metav1.Time{
			Time: time.Now(),
		}

		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			_, err := w.faroscli.Workers().UpdateStatus(ctx, wCopy, metav1.UpdateOptions{})
			if err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			w.log.Error(err, "error healthStatus update")
		}
		w.log.Info("ping", "name", name)

		select {
		case <-ctx.Done():
			w.log.Info("exiting healthStatus")
			return nil
		case <-t.C:
		}

	}
}
