package kubeconfig

import (
	"log"
	"net/http/httputil"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/controller"
	"github.com/faroshq/faros/pkg/service/authentication/kubectl"
	"github.com/faroshq/faros/pkg/util/clientcache"
	"github.com/faroshq/faros/pkg/util/roundtripper"
)

type kubeconfig struct {
	log         *logrus.Entry
	controller  controller.Controller
	config      *config.Config
	clientCache clientcache.ClientCache
}

func New(
	logger *logrus.Entry,
	config *config.Config,
	controller controller.Controller,
	router *mux.Router,
) error {
	k := &kubeconfig{
		log:         logger,
		config:      config,
		clientCache: clientcache.New(time.Hour),
		controller:  controller,
	}

	// kubeconfig authentication
	kubeconfigAuth, err := kubectl.New(k.log, k.config, k.controller)
	if err != nil {
		return err
	}

	// proxy access
	proxy := &httputil.ReverseProxy{
		Director:  k.director,
		Transport: roundtripper.RoundTripperFunc(k.roundTripper),
		ErrorLog:  log.New(k.log.Writer(), "", 0),
	}
	proxyRouter := router.NewRoute().Subrouter()
	proxyRouter.Use(kubeconfigAuth.Authenticate())
	// Important: if you are changing this path, make sure proxy splitters are up to date,
	// as things will go bananas otherwise.
	proxyRouter.PathPrefix("/namespaces/{namespace}/clusters/{cluster}/access/{access}/proxy").Handler(proxy)

	return nil
}
