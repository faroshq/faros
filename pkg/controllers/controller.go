package controllers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	kcptenancyv1alpha1 "github.com/kcp-dev/kcp/pkg/apis/tenancy/v1alpha1"
	"golang.org/x/sync/errgroup"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	"github.com/faroshq/faros/pkg/config"
	utilhttp "github.com/faroshq/faros/pkg/util/http"
)

var (
	scheme = runtime.NewScheme()
)

const resyncPeriod = 10 * time.Hour

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kcptenancyv1alpha1.AddToScheme(scheme))
	utilruntime.Must(tenancyv1alpha1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
}

type Controllers interface {
	WaitForAPIReady(ctx context.Context) error
	Run(ctx context.Context) error
}

type controllerManager struct {
	config *config.Config
}

func New(c *config.Config) (Controllers, error) {
	if c.FarosKCPConfig.ControllersRestConfig == nil {
		return nil, fmt.Errorf("controllers rest config is nil")
	}

	return &controllerManager{
		config: c,
	}, nil
}

func (c *controllerManager) WaitForAPIReady(ctx context.Context) error {
	// Wait for API server to report healthy
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	ticker := time.NewTicker(time.Second * 2)
	defer ticker.Stop()

	for {
		h := utilhttp.GetInsecureClient()
		res, err := h.Get(c.config.FarosKCPConfig.KCPClusterRestConfig.Host + "/healthz")
		switch {
		case err != nil:
			klog.Infof("Waiting for API server to report healthy: %v", err)
		case res.StatusCode != http.StatusOK:
			klog.Infof("Waiting for API server to report healthy: %v", res.Status)
		case res.StatusCode == http.StatusOK:
			klog.Infof("API server is healthy")
			return nil
		}

		select {
		case <-ctx.Done():
			klog.Infof("stopped waiting for API server to report healthy: %v", ctx.Err())
			return nil
		case <-ticker.C:
		}
	}
}

func (c *controllerManager) Run(ctx context.Context) error {
	klog.V(2).Info("starting controllers")

	eg := errgroup.Group{}

	eg.Go(func() error {
		return c.runTenancyControllers(ctx)
	})

	//eg.Go(func() error {
	//	return c.runWorkloadsControllers(ctx)
	//})

	return eg.Wait()
}
