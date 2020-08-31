package heartbeat

// Copyright (c) Faros.sh
// Licensed under the Apache License 2.0.

import (
	"time"

	"go.uber.org/zap"

	"github.com/faroshq/faros/pkg/util/recover"
)

// EmitHeartbeat sends a heartbeat metric (if healthy), starting immediately and
// subsequently every 60 seconds
func EmitHeartbeat(log *zap.Logger, stop <-chan struct{}, checkFunc func() bool) {
	defer recover.Panic(log)

	t := time.NewTicker(time.Minute)
	defer t.Stop()

	log.Sugar().Info("starting heartbeat")

	for {
		if checkFunc() {
			//TODO: Emit heartbeat
		}

		select {
		case <-t.C:
		case <-stop:
			return
		}
	}
}
