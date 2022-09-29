package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/faroshq/faros/pkg/agent"
	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/util/log"

	ctrl "sigs.k8s.io/controller-runtime"
)

func main() {
	ctx := context.Background()
	flag.Parse()
	if err := run(ctx); err != nil {
		fmt.Printf("error starting agent: %v", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	c, err := config.LoadAgent()
	if err != nil {
		return err
	}

	restConfig := ctrl.GetConfigOrDie()

	log := log.GetLogger()
	log.Info("starting faros agent")

	agent := agent.New(log, c, restConfig)
	return agent.Run(ctx)
}
