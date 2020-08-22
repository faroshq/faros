package main

// Copyright (c) Faros.sh.
// Licensed under the Apache License 2.0.

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"

	"github.com/faroshq/faros/pkg/util/logger"
	_ "github.com/faroshq/faros/pkg/util/scheme"
	"github.com/faroshq/faros/pkg/util/version"
)

func usage() {
	fmt.Fprint(flag.CommandLine.Output(), "usage:\n")
	fmt.Fprintf(flag.CommandLine.Output(), "  %s monitor\n", os.Args[0])
	fmt.Fprintf(flag.CommandLine.Output(), "  %s operator\n", os.Args[0])
	flag.PrintDefaults()
}

func main() {
	log := logger.GetZapLoggerInstance("", zap.InfoLevel)
	log.Sugar().Infof("starting, git commit %s", version.GitCommit)

	flag.Usage = usage
	flag.Parse()

	ctx := context.Background()

	var err error
	switch strings.ToLower(flag.Arg(0)) {
	case "operator":
		checkArgs(1)
		err = operator(ctx, log)
	case "deploy":
		checkArgs(1)
		err = deploy(ctx, log)
	default:
		usage()
		os.Exit(2)
	}

	if err != nil {
		log.Sugar().Error(err)
	}
}

func checkArgs(required int) {
	if len(flag.Args()) != required {
		usage()
		os.Exit(2)
	}
}
