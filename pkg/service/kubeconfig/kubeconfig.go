package kubeconfig

import (
	"log"
	"net/http/httputil"
	"time"

	"github.com/appvia/cluster-registry-operator/pkg/service/middleware"
	"github.com/appvia/cluster-registry-operator/pkg/store"
	"github.com/appvia/cluster-registry-operator/pkg/util/clientcache"
	"github.com/appvia/cluster-registry-operator/pkg/util/roundtripper"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

type kubeconfig struct {
	log *logrus.Entry

	store store.Store

	clientCache clientcache.ClientCache
}

func New(
	logger *logrus.Entry,
	store store.Store,
	router *mux.Router,
) *kubeconfig {
	k := &kubeconfig{
		log:   logger,
		store: store,

		clientCache: clientcache.New(time.Hour),
	}

	// direct access endpoint
	rpDirect := &httputil.ReverseProxy{
		Director:  k.directorDirectAccess,
		Transport: roundtripper.RoundTripperFunc(k.roundTripper),
		ErrorLog:  log.New(k.log.Writer(), "", 0),
	}
	directRouter := router.NewRoute().Subrouter()
	directRouter.Use(middleware.Bearer(k.store))
	// Important: if you are changing this path, make sure proxy splitters are up to date,
	// as things will go bananas otherwise.
	directRouter.PathPrefix("/namespaces/{namespace}/clusters/{cluster}/access/{access}/direct").Handler(rpDirect)

	// direct access endpoint
	rpProxy := &httputil.ReverseProxy{
		Director:  k.directorProxyAccess,
		Transport: roundtripper.RoundTripperFunc(k.roundTripper),
		ErrorLog:  log.New(k.log.Writer(), "", 0),
	}
	proxyRouter := router.NewRoute().Subrouter()
	proxyRouter.Use(middleware.Proxy(k.store))
	// Important: if you are changing this path, make sure proxy splitters are up to date,
	// as things will go bananas otherwise.
	proxyRouter.PathPrefix("/namespaces/{namespace}/clusters/{cluster}/access/{access}/proxy").Handler(rpProxy)

	return k
}
