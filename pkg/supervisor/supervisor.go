package supervisor

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/InVisionApp/go-health/v2"
	"github.com/sirupsen/logrus"

	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/controller"
	"github.com/faroshq/faros/pkg/service"
	"github.com/faroshq/faros/pkg/store"
	"github.com/faroshq/faros/pkg/util/recover"
)

var _ Interface = &Supervisor{}

type Interface interface {
	Run(context.Context, <-chan struct{}, chan<- struct{})
}

type Supervisor struct {
	log        *logrus.Entry
	service    service.Interface
	controller controller.Controller
}

func New(ctx context.Context, log *logrus.Entry, config *config.ServerConfig, sqlStore store.Store, health *health.Health) (*Supervisor, error) {
	ctrl, err := controller.New(log, config, sqlStore)
	if err != nil {
		return nil, err
	}

	svc, err := service.New(ctx, log, config, ctrl, health)
	if err != nil {
		return nil, err
	}

	return &Supervisor{
		log:        log.WithField("component", "supervisor"),
		service:    svc,
		controller: ctrl,
	}, nil
}

func (c *Supervisor) Run(ctx context.Context, stop <-chan struct{}, done chan<- struct{}) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	wg := &sync.WaitGroup{}
	fm := []func(context.Context, *sync.WaitGroup){
		c.runService,
		c.runController,
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

func (c *Supervisor) runService(ctx context.Context, wg *sync.WaitGroup) {
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

func (c *Supervisor) runController(ctx context.Context, wg *sync.WaitGroup) {
	defer recover.Panic(c.log)

	defer wg.Done()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		err := c.controller.Run(ctx)
		if err != nil {
			c.log.Fatalf("controller failed to start: %s", err)
		}

		select {
		case <-ctx.Done():
			c.log.Info("stopped controller")
			return
		case <-ticker.C:
		}

	}
}
