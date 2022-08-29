package log

import (
	"flag"
	"fmt"
	"runtime"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/faroshq/faros/pkg/models"
	"github.com/faroshq/faros/pkg/util/stringutils"
)

var (
	_, thisfile, _, _ = runtime.Caller(0)
	repopath          = strings.Replace(thisfile, "pkg/util/log/log.go", "", -1)

	loglevel = flag.String("loglevel", "info", "{panic,fatal,error,warning,info,debug,trace}")
)

func getBaseLogger() *logrus.Logger {
	logger := logrus.New()

	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})

	return logger
}

// GetLogger returns a consistently configured log entry
func GetLogger() *logrus.Entry {
	logger := getBaseLogger()

	logger.SetReportCaller(true)
	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:    true,
		CallerPrettyfier: relativeFilePathPrettier,
	})

	log := logrus.NewEntry(logger)

	l, err := logrus.ParseLevel(*loglevel)
	if err == nil {
		log.Logger.SetLevel(l)
	} else {
		log.Warn(err)
	}

	return log
}

func relativeFilePathPrettier(f *runtime.Frame) (string, string) {
	file := strings.TrimPrefix(f.File, repopath)
	function := stringutils.LastTokenByte(f.Function, '/')
	return fmt.Sprintf("%s()", function), fmt.Sprintf("%s:%d", file, f.Line)
}

// EnrichWithCorrelationData sets log fields based on an optional
// correlationData struct
func EnrichWithCorrelationData(rlog *logrus.Entry, correlationData *models.CorrelationData) *logrus.Entry {
	if correlationData == nil {
		return rlog
	}

	return rlog.WithFields(
		logrus.Fields{
			"client_request_id": correlationData.ClientRequestID,
			"request_id":        correlationData.RequestID,
		},
	)
}
