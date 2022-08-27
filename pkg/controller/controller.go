package controller

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/InVisionApp/go-health/v2"
	"github.com/sirupsen/logrus"

	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/service"
	"github.com/faroshq/faros/pkg/session"
	"github.com/faroshq/faros/pkg/store"
	"github.com/faroshq/faros/pkg/util/recover"
)

var _ Interface = &Controller{}

type Interface interface {
	Run(context.Context, <-chan struct{}, chan<- struct{})
}

type Controller struct {
	log      *logrus.Entry
	service  service.Interface
	sessions session.Interface
}

func New(ctx context.Context, log *logrus.Entry, config *config.Config, pgStore store.Store, health *health.Health) (*Controller, error) {
	svc, err := service.New(ctx, log, config, pgStore, health)
	if err != nil {
		return nil, err
	}

	sess, err := session.New(log, config, pgStore)
	if err != nil {
		return nil, err
	}

	return &Controller{
		log:      log,
		service:  svc,
		sessions: sess,
	}, nil
}

func (c *Controller) Run(ctx context.Context, stop <-chan struct{}, done chan<- struct{}) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	wg := &sync.WaitGroup{}
	fm := []func(context.Context, *sync.WaitGroup){
		c.runService,
		c.runSessions,
	}

	if stop != nil {
		go func() {
			defer recover.Panic(c.log)

			<-stop
			c.log.Info("stopping controller")
			cancel()
		}()
	}

	for _, f := range fm {
		wg.Add(1)
		go f(ctx, wg)
	}

	wg.Wait()
	close(done)
}

func (c *Controller) runService(ctx context.Context, wg *sync.WaitGroup) {
	defer recover.Panic(c.log)

	defer wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		err := c.service.Run(ctx)
		if err != nil {
			if strings.Contains(err.Error(), "http: Server closed") {
				return
			}
			c.log.Fatalf("controller failed to start: %s", err)
		}

		select {
		case <-ctx.Done():
			c.log.Info("stopped service")
			return
		case <-ticker.C:
		}

	}
}

func (c *Controller) runSessions(ctx context.Context, wg *sync.WaitGroup) {
	defer recover.Panic(c.log)

	defer wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		err := c.sessions.Run(ctx)
		if err != nil {
			if strings.Contains(err.Error(), "http: Server closed") {
				return
			}
			c.log.Fatalf("sessions failed to start: %s", err)
		}

		select {
		case <-ctx.Done():
			c.log.Info("stopped sessions")
			return
		case <-ticker.C:
		}

	}
}
