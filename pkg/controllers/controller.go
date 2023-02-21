package controllers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	kcptenancyv1alpha1 "github.com/kcp-dev/kcp/pkg/apis/tenancy/v1alpha1"
	kcpclientset "github.com/kcp-dev/kcp/pkg/client/clientset/versioned/cluster"
	"golang.org/x/sync/errgroup"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	"github.com/faroshq/faros/pkg/bootstrap"
	"github.com/faroshq/faros/pkg/config"
	utilhttp "github.com/faroshq/faros/pkg/util/http"
	utilkubernetes "github.com/faroshq/faros/pkg/util/kubernetes"
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
	config        *config.Config
	clientFactory utilkubernetes.ClientFactory
	bootstraper   bootstrap.Bootstraper
	kcpClientSet  kcpclientset.ClusterInterface
}

func New(c *config.Config) (Controllers, error) {
	b, err := bootstrap.New(&c.FarosKCPConfig)
	if err != nil {
		return nil, err
	}

	cf, err := utilkubernetes.NewClientFactory(c.FarosKCPConfig.KCPClusterRestConfig)
	if err != nil {
		return nil, err
	}

	kcpClientSet, err := kcpclientset.NewForConfig(c.FarosKCPConfig.KCPClusterRestConfig)
	if err != nil {
		return nil, err
	}

	return &controllerManager{
		kcpClientSet:  kcpClientSet,
		config:        c,
		clientFactory: cf,
		bootstraper:   b,
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
	// bootstrap will set missing ctrlRestConfig and deploy kcp wide resources
	ctxT, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	err := c.bootstrap(ctxT)
	if err != nil {
		return fmt.Errorf("bootstrap failed: %w", err)
	}

	eg := errgroup.Group{}

	eg.Go(func() error {
		return c.runTenancyControllers(ctx)
	})

	return eg.Wait()
}

func (c *controllerManager) bootstrap(ctx context.Context) error {
	// create controllers workspace
	for _, w := range []string{
		c.config.FarosKCPConfig.ControllersTenantWorkspace,
		c.config.FarosKCPConfig.ControllersWorkspace,
	} {
		if err := c.bootstraper.CreateWorkspace(ctx, w); err != nil {
			return err
		}
	}
	// create assets for controller workspace being able to access all "workspaces"
	// and implement their requests
	if err := c.bootstraper.DeployKustomizeAssetsCRD(ctx); err != nil {
		return err
	}
	if err := c.bootstraper.DeployKustomizeAssetsKCP(ctx); err != nil {
		return err
	}

	// create assets for controller tenant workspace being able to access use apis
	if err := c.bootstraper.BootstrapServiceTenantAssets(ctx); err != nil {
		return err
	}

	return nil
}
