package recover

// Copyright (c) Faros.sh
// Licensed under the Apache License 2.0.

import (
	"fmt"
	"os"
	"runtime/debug"

	"go.uber.org/zap"
)

// Panic recovers a panic
// TODO: Add tests
func Panic(log *zap.Logger) {
	if e := recover(); e != nil {
		if log != nil {
			log.Sugar().Error(e)
			log.Sugar().Info(string(debug.Stack()))

		} else {
			fmt.Fprintln(os.Stderr, e)
			debug.PrintStack()
		}
	}
}
