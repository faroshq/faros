package recover

import (
	"fmt"
	"runtime/debug"

	"github.com/sirupsen/logrus"
)

// Panic recovers a panic
func Panic(log *logrus.Entry) {
	if e := recover(); e != nil {
		log.Error(fmt.Sprint("%w", e))
		log.Info(string(debug.Stack()))

	}
}
