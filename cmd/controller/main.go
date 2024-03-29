package main

import (
	"context"
	"flag"
	"os"

	"k8s.io/klog/v2"

	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/controllers"
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

	controller, err := controllers.New(cfg)
	if err != nil {
		return err
	}

	klog.Info("Starting controller")
	go controller.Run(ctx)

	<-ctx.Done()
	return nil
}
