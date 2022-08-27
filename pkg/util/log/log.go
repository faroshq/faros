package log

// Copyright (c) Microsoft Corporation.
// Licensed under the Apache License 2.0.

import (
	"flag"
	"fmt"
	"runtime"
	"strings"

	"github.com/coreos/go-systemd/v22/journal"
	"github.com/sirupsen/logrus"

	"github.com/faroshq/faros/pkg/models"
	"github.com/faroshq/faros/pkg/util/stringutils"
)

var (
	_, thisfile, _, _ = runtime.Caller(0)
	repopath          = strings.Replace(thisfile, "pkg/util/log/log.go", "", -1)

	loglevel = flag.String("loglevel", "info", "{panic,fatal,error,warning,info,debug,trace}")
)

type ResultType string

const (
	SuccessResultType     ResultType = "Success"
	UserErrorResultType   ResultType = "UserError"
	ServerErrorResultType ResultType = "InternalServerError"
)

func getBaseLogger() *logrus.Logger {
	logger := logrus.New()

	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp: true,
	})

	return logger
}

// Important: Logger hooks loading order is important as they are executed in
// such order (https://github.com/sirupsen/logrus/blob/master/hooks.go#L26-L34).
//  This means we need to load all populating hooks (logrHook, auditHook)
// first, and emitting hooks (journald) last. Otherwise enriched data is lost.
// In addition to that we need to understand that in situations like this log output
// in CMD might be not the same as in journald, because we emit directly to journald.
// If to remove this, we would need additional layer for log parsing.

// GetLogger returns a consistently configured log entry
func GetLogger() *logrus.Entry {
	logger := getBaseLogger()

	logger.SetReportCaller(true)
	logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:    true,
		CallerPrettyfier: relativeFilePathPrettier,
	})

	if journal.Enabled() {
		logger.AddHook(&journaldHook{})
	}

	log := logrus.NewEntry(logger)

	l, err := logrus.ParseLevel(*loglevel)
	if err == nil {
		logrus.SetLevel(l)
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
