//+build e2e

package e2e

// Copyright (c) Faros.sh.
// Licensed under the Apache License 2.0.

import (
	"flag"
	"math/rand"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"github.com/onsi/gomega/format"
	"go.uber.org/zap"

	"github.com/faroshq/faros/pkg/util/logger"
	"github.com/faroshq/faros/pkg/util/version"
)

func TestE2E(t *testing.T) {
	rand.Seed(time.Now().UnixNano())

	flag.Parse()
	log := logger.GetZapLoggerInstance("", zap.InfoLevel)
	log.Sugar().Infof("e2e tests starting, git commit %s\n", version.GitCommit)
	RegisterFailHandler(Fail)
	format.TruncatedDiff = false
	RunSpecs(t, "e2e tests")
}
