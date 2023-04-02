package main

import (
	"context"
	"flag"
	"os"
	"time"

	"k8s.io/klog/v2"

	"github.com/faroshq/faros/pkg/bootstrap"
	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/util/version"
)

func main() {

	klog.InitFlags(flag.CommandLine)
	flag.Parse()
	ctx := klog.NewContext(context.Background(), klog.NewKlogr())

	err := run(ctx)
	if err != nil {
		klog.Error(err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	klog.Info("Version: ", version.GetVersion().Version)

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	bootstrap, err := bootstrap.New(&cfg.FarosKCPConfig)
	if err != nil {
		return err
	}

	ctxT, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	klog.Info("Starting bootstrapper")
	return bootstrap.Bootstrap(ctxT)
}
