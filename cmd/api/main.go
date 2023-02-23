package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"k8s.io/klog/v2"

	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/server"
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
		return fmt.Errorf("failed to load config: %w", err)
	}

	server, err := server.New(ctx, cfg)
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	go server.Run(ctx)

	<-ctx.Done()
	return nil
}
