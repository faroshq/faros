package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	health "github.com/InVisionApp/go-health/v2"
	_ "github.com/go-sql-driver/mysql"

	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/controller"
	sqlstore "github.com/faroshq/faros/pkg/store/sql"
	"github.com/faroshq/faros/pkg/util/log"
)

func main() {
	ctx := context.Background()
	flag.Parse()
	if err := run(ctx); err != nil {
		fmt.Printf("error starting controller: %v", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	c, err := config.Load(true, true)
	if err != nil {
		return err
	}

	log := log.GetLogger()

	log.Info("starting faros controller")

	// Create a new health instance
	h := health.New()
	defer h.Stop()

	if os.Getenv("PORT") != "" {
		// Overriding given port
		c.API.URI = ":" + os.Getenv("PORT")
	}

	sqlStore, err := sqlstore.NewStore(log, c)
	if err != nil {
		return err
	}

	h.AddCheck(&health.Config{
		Name:     "database",
		Interval: time.Second * 5,
		Checker:  sqlStore,
		Fatal:    true,
	})

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	stop := make(chan struct{})
	done := make(chan struct{})

	ctrl, err := controller.New(ctx, log, c, sqlStore, h)
	if err != nil {
		return err
	}

	go ctrl.Run(ctx, stop, done)
	select {
	case <-signals:
		// shutdown
	case <-ctx.Done():
		// ctx termination
	}
	// we catch both sigterm (used by systemd) and Interupt (ctr+c) for development. Later is not really needed
	log.Info("received Sigterm/Int")
	close(stop)

	shutdownCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	select {
	case <-shutdownCtx.Done():
		log.Warn("controller didn't shutdown in time, force exit")
	case <-done:
		// OK
	}

	err = sqlStore.Close()
	if err != nil {
		log.Errorf("error while closing SQL store: %s", err)
	}

	return nil
}
