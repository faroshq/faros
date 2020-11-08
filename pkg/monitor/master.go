package monitor

// Copyright (c) Faros.sh
// Licensed under the Apache License 2.0.

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/klog"
)

const masterAnnotationKey = "hub.faros.sh/master"
const heartBeatAnnotationKey = "hub.faros.sh/lastHeartBeat"

// master updates the monitor document with the list of buckets balanced between
// registered monitors
func (mon *monitor) master(ctx context.Context) error {
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      "faros-hub",
			Namespace: "faros-hub",
		},
		Client: mon.kubernetescli.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: mon.podName,
		},
	}

	// start the leader election code loop
	go leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   60 * time.Second,
		RenewDeadline:   15 * time.Second,
		RetryPeriod:     5 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				// we're notified when we start - this is where you would
				// usually put your code
				mon.isMaster = true
				//run(ctx)

			},
			OnStoppedLeading: func() {
				// we can do cleanup here
				klog.Infof("leader lost: %s", mon.podName)
				mon.isMaster = false
				return // we should exit but we guard code with isMaster
			},
			OnNewLeader: func(identity string) {
				// we're notified when new leader elected
				if identity == mon.podName {
					// I just got the lock
					return
				}
				klog.Infof("new leader elected: %s", identity)
			},
		},
	})
	return nil
}
