package main

import (
	"context"
	"flag"
	"os"

	"k8s.io/klog/v2"

	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/server"
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

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	server, err := server.New(ctx, cfg)
	if err != nil {
		return err
	}

	go server.Run(ctx)

	<-ctx.Done()
	return nil
}
