package main

// Copyright (c) Faros.sh.
// Licensed under the Apache License 2.0.

import (
	"io"
	"net"
	"os"
	"os/signal"

	"go.uber.org/zap"

	"github.com/faroshq/faros/pkg/util/logger"
)

func run(log *zap.Logger) error {
	os.Remove("mdm_statsd.socket")

	sigint := make(chan os.Signal, 1)
	signal.Notify(sigint, os.Interrupt)

	l, err := net.Listen("unix", "mdm_statsd.socket")
	if err != nil {
		return err
	}

	log.Sugar().Info("listening")

	go func() error {
		for {
			c, err := l.Accept()
			if err != nil {
				return err
			}

			go io.Copy(os.Stdout, c)
		}
	}()

	<-sigint

	return l.Close()
}

func main() {
	log := logger.GetZapLoggerInstance("", zap.InfoLevel)

	err := run(log)
	if err != nil {
		panic(err)
	}
}
