package monitor

// Copyright (c) Faros.sh
// Licensed under the Apache License 2.0.

import (
	"context"
	"sort"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/faroshq/faros/pkg/util/recover"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"

	farosv1alpha1 "github.com/faroshq/faros/pkg/operator/apis/faros.sh/v1alpha1"
	"github.com/faroshq/faros/pkg/util/status"
)

type worker struct {
	log *zap.Logger

	kubernetescli kubernetes.Interface

	podName string
}

func (mon *monitor) runWorker(ctx context.Context, done chan<- struct{}) error {
	defer recover.Panic(mon.log)

	go func() {
		defer recover.Panic(mon.log)

		<-ctx.Done()

		err := mon.deregisterWorker(context.Background(), mon.podName)
		if err != nil {
			mon.log.Sugar().Error("error while de-registering worker", zap.Error(err))
		}

		// Allows for better controll of exiting if we want in the future
		mon.log.Sugar().Info("marking not ready and waiting 10 seconds")
		time.Sleep(10 * time.Second)

		mon.log.Sugar().Info("exiting")
		close(done)
	}()

	t := time.NewTicker(30 * time.Second)
	defer t.Stop()

	for {
		if mon.isMaster {
			mon.log.Debug("worker job executed")
			err := mon.checkIdleWorkers(ctx)
			if err != nil {
				mon.log.Sugar().Error(zap.Error(err))
			}
			err = mon.balance(ctx)
			if err != nil {
				mon.log.Sugar().Error(zap.Error(err))
			}
		}
		<-t.C
	}
}

func (mon *monitor) deregisterWorker(ctx context.Context, name string) error {
	return mon.faroscli.Workers().Delete(ctx, name, metav1.DeleteOptions{})
}

// checkIdleWorkers checks if any worker didn't called in in 10 minutes and deletes it
func (mon *monitor) checkIdleWorkers(ctx context.Context) error {
	defer recover.Panic(mon.log)

	workers, err := mon.faroscli.Workers().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	ttl := time.Now().Add(-5 * time.Minute)
	for _, w := range workers.Items {
		if w.Status.LastHeartbeatTime.Before(&metav1.Time{Time: ttl}) {
			mon.log.Error("dead worker detected. Cleaning", zap.String("name", w.Name))
			err := mon.faroscli.Workers().Delete(ctx, w.Name, metav1.DeleteOptions{})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

type workerRef struct {
	w farosv1alpha1.Worker
	c int
}

type workerList []workerRef

func (w workerList) Len() int           { return len(w) }
func (w workerList) Less(i, j int) bool { return w[i].c < w[j].c }
func (w workerList) Swap(i, j int)      { w[i], w[j] = w[j], w[i] }

type clusterRef struct {
	c  farosv1alpha1.Cluster
	cc int
}

type clusterList []clusterRef

func (c clusterList) Len() int           { return len(c) }
func (c clusterList) Less(i, j int) bool { return c[i].cc < c[j].cc }
func (c clusterList) Swap(i, j int)      { c[i], c[j] = c[j], c[i] }

func (mon *monitor) balance(ctx context.Context) error {
	workers, err := mon.faroscli.Workers().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	clusters, err := mon.faroscli.Clusters("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	err = mon.balanceWorkers(ctx, workers, clusters)
	if err != nil {
		return err
	}

	return mon.balanceClusters(ctx, workers, clusters)
}

func (mon *monitor) balanceClusters(ctx context.Context, workers *farosv1alpha1.WorkerList, clusters *farosv1alpha1.ClusterList) error {
	// construct workers list
	workerList := make(workerList, len(workers.Items))
	for i, w := range workers.Items {
		workerList[i] = workerRef{
			w: w,
			c: 0,
		}
	}

	var staleClusters []farosv1alpha1.Cluster
	for _, c := range clusters.Items {
		var active bool
		// cluster is healthy and registered
		if isHealthy(c.Status.Conditions) {
			// if worker are still present during time of checking, we do not
			// need to balance this cluster
			if c.Status.WorkerUID != "" {
				for i, worker := range workerList {
					if worker.w.UID == c.Status.WorkerUID {
						workerList[i].c++
						active = true
						continue
					}
				}
			}
			// cluster object is stale and not distributed
			if !active {
				staleClusters = append(staleClusters, c)
			}
		}
	}

	if len(staleClusters) > 0 {
		for _, c := range staleClusters {
			sort.Sort(workerList)
			worker, err := mon.faroscli.Workers().Get(ctx, workerList[0].w.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}

			worker.Status.Clusters = append(worker.Status.Clusters, c.UID)
			err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
				// update worker status with new assigned cluster
				_, err := mon.faroscli.Workers().UpdateStatus(ctx, worker, metav1.UpdateOptions{})
				if err != nil {
					return err
				}
				c.Status.WorkerUID = worker.UID
				_, err = mon.faroscli.Clusters(c.Namespace).UpdateStatus(ctx, &c, metav1.UpdateOptions{})
				if err != nil {
					return err
				}
				return nil
			})
			if err != nil {
				mon.log.Error("error while updating balancing",
					zap.Error(err),
					zap.String("worker", worker.Name),
					zap.String("cluster", c.Name))
				return err
			}
			workerList[0].c++
		}

		for _, w := range workerList {
			mon.log.Info(w.w.Name, zap.Int("count", w.c))
		}
	}

	return nil
}

func (mon *monitor) balanceWorkers(ctx context.Context, workers *farosv1alpha1.WorkerList, clusters *farosv1alpha1.ClusterList) error {
	for _, w := range workers.Items {
		// for each worker clusters it is responsible for
		for _, c := range w.Status.Clusters {
			var exists bool
			for _, cc := range clusters.Items {
				if cc.UID == c {
					exists = true
				}
			}
			if !exists {
				err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
					worker, err := mon.faroscli.Workers().Get(ctx, w.Name, metav1.GetOptions{})
					if err != nil {
						return err
					}
					wCopy := worker.DeepCopy()
					wCopy.Status.Clusters = removeUID(wCopy.Status.Clusters, c)
					spew.Dump(wCopy.Status.Clusters)
					_, err = mon.faroscli.Workers().UpdateStatus(ctx, wCopy, metav1.UpdateOptions{})
					if err != nil {
						return err
					}
					return nil
				})
				if err != nil {
					mon.log.Error("error while balancing clusters",
						zap.Error(err),
						zap.String("worker", w.Name))
					return err
				}
			}
		}
	}

	return nil
}

func isHealthy(statuses []status.Condition) bool {
	for _, s := range statuses {
		if s.Type == farosv1alpha1.Healthy && s.IsTrue() {
			return true
		}
	}
	return false
}

func removeUID(slice []types.UID, id types.UID) []types.UID {
	var result []types.UID
	for _, t := range slice {
		if t == id {
			continue
		}
		result = append(result, t)
	}
	return result
}

func unique(slice []types.UID) []types.UID {
	keys := make(map[types.UID]bool)
	list := []types.UID{}
	for _, entry := range slice {
		if _, value := keys[entry]; !value {
			keys[entry] = true
			list = append(list, entry)
		}
	}
	return list
}
