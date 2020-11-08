package monitor

// Copyright (c) Faros.sh
// Licensed under the Apache License 2.0.

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coreos/etcd/store"
	"go.uber.org/zap"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"

	farosv1alpha1 "github.com/faroshq/faros/pkg/operator/apis/faros.sh/v1alpha1"
	farosclient "github.com/faroshq/faros/pkg/operator/clientset/faros.sh/v1alpha1/versioned/typed/faros.sh/v1alpha1"
	"github.com/faroshq/faros/pkg/util/bucket"
	"github.com/faroshq/faros/pkg/util/logger"
	"github.com/faroshq/faros/pkg/util/recover"
)

type monitor struct {
	log      *zap.Logger
	store    *store.Store
	mu       sync.RWMutex
	clusters *farosv1alpha1.ClusterList

	kubernetescli kubernetes.Interface
	faroscli      farosclient.FarosV1alpha1Interface

	isMaster    bool
	podName     string
	bucketCount int
	buckets     map[int]struct{}

	lastBucketlist atomic.Value //time.Time
	lastChangefeed atomic.Value //time.Time
	startTime      time.Time
}

type Runnable interface {
	Run(context.Context, chan<- struct{}) error
}

func NewMonitor(log *zap.Logger, name string, kubernetescli kubernetes.Interface, faroscli farosclient.FarosV1alpha1Interface) (Runnable, error) {
	rlog := logger.NewLogRLogger(log)
	ctrl.SetLogger(rlog)

	return &monitor{
		log: log,

		kubernetescli: kubernetescli,
		faroscli:      faroscli,

		podName: name,

		bucketCount: bucket.Buckets,
		buckets:     map[int]struct{}{},

		startTime: time.Now(),
	}, nil
}

// Run runs:
// * worker process to register as worker and keep updating the object.
// * master process
//    - read workers and distribute work for them.
//    - reads workers on periodic basis and decides if worker is still alive
func (mon *monitor) Run(ctx context.Context, done chan<- struct{}) error {
	defer recover.Panic(mon.log)

	err := mon.master(ctx)
	if err != nil {
		return err
	}

	return mon.runWorker(ctx, done)
}
