package agent

import (
	"context"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/service/kubeconfig"
	"github.com/faroshq/faros/pkg/util/dialer"
	faroshttputil "github.com/faroshq/faros/pkg/util/httputil"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
	"k8s.io/utils/clock"
)

type Agent interface {
	Run(context.Context) error
}

type agent struct {
	log            *logrus.Entry
	config         *config.AgentConfig
	downstreamRest *rest.Config
}

func New(log *logrus.Entry, config *config.AgentConfig, downstream *rest.Config) Agent {
	return &agent{
		log:            log.WithField("component", "agent"),
		config:         config,
		downstreamRest: downstream,
	}
}

func (a *agent) Run(ctx context.Context) error {
	return a.connect(ctx)
}

func (a *agent) connect(ctx context.Context) error {
	var (
		initBackoff   = 5 * time.Second
		maxBackoff    = 5 * time.Minute
		resetDuration = 1 * time.Minute
		backoffFactor = 2.0
		jitter        = 1.0
		clock         = &clock.RealClock{}
		sliding       = true
	)

	backoffMgr := wait.NewExponentialBackoffManager(initBackoff, maxBackoff, resetDuration, backoffFactor, jitter, clock)

	wait.BackoffUntil(func() {
		klog.V(5).Infof("Starting tunnel for Faros")
		err := a.startTunnel(ctx)
		if err != nil {
			klog.Errorf("Failed to create tunnel: %v", err)
		}
	}, backoffMgr, sliding, ctx.Done())
	return nil
}

func (a *agent) startTunnel(ctx context.Context) error {

	// agent --> faros
	httpClient := faroshttputil.DefaultInsecureClient

	// agent --> local apiserver
	cfg := *a.downstreamRest
	// use http/1.1 to allow SPDY tunneling: pod exec, port-forward, ...
	cfg.NextProtos = []string{"http/1.1"}
	url, err := url.Parse(cfg.Host)
	if err != nil {
		return err
	}

	proxy := httputil.NewSingleHostReverseProxy(url)
	if err != nil {
		return err
	}

	clientDownstream, err := rest.HTTPClientFor(&cfg)
	if err != nil {
		return err
	}
	proxy.Transport = clientDownstream.Transport

	// create the reverse connection
	// virtual workspaces
	// agent --> faros
	u, err := url.Parse(a.config.ServerURI)
	if err != nil {
		return err
	}
	// strip the path
	u.Path = ""
	dst := kubeconfig.GetClusterConnectURL(u.String(), a.config.NamespaceID, a.config.ClusterID, a.config.AccessID)
	if err != nil {
		return err
	}
	klog.Infof("connecting to %s/%s at %s", a.config.NamespaceID, a.config.ClusterID, dst)

	// add authorization header to the req
	header := http.Header{}
	var bearer = "Bearer " + a.config.AccessKey
	header.Add("Authorization", bearer)

	header.Set("Bearer", a.config.AccessKey)
	l, err := dialer.NewListener(a.log, httpClient, dst, header)
	if err != nil {
		return err
	}
	defer l.Close()

	// reverse proxy the request coming from the reverse connection to the p-cluster apiserver
	server := &http.Server{Handler: proxy}
	defer server.Close()

	klog.V(2).Infof("Serving on reverse connection")
	errCh := make(chan error)
	go func() {
		errCh <- server.Serve(l)
	}()

	select {
	case err = <-errCh:
	case <-ctx.Done():
		err = server.Close()
	}
	klog.V(2).Infof("Stop serving on reverse connection")
	return err
}
