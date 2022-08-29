package kubeconfig

import (
	"log"
	"net/http/httputil"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/faroshq/faros/pkg/service/middleware"
	"github.com/faroshq/faros/pkg/store"
	"github.com/faroshq/faros/pkg/util/auth"
	"github.com/faroshq/faros/pkg/util/clientcache"
	"github.com/faroshq/faros/pkg/util/roundtripper"
)

type kubeconfig struct {
	log         *logrus.Entry
	store       store.Store
	clientCache clientcache.ClientCache
}

func New(
	logger *logrus.Entry,
	store store.Store,
	router *mux.Router,
	auth auth.Authenticator,
) *kubeconfig {
	k := &kubeconfig{
		log:         logger,
		store:       store,
		clientCache: clientcache.New(time.Hour),
	}

	// proxy access
	proxy := &httputil.ReverseProxy{
		Director:  k.director,
		Transport: roundtripper.RoundTripperFunc(k.roundTripper),
		ErrorLog:  log.New(k.log.Writer(), "", 0),
	}
	proxyRouter := router.NewRoute().Subrouter()
	proxyRouter.Use(middleware.KubeConfigAuthentication(logger, auth, k.store))
	// Important: if you are changing this path, make sure proxy splitters are up to date,
	// as things will go bananas otherwise.
	proxyRouter.PathPrefix("/namespaces/{namespace}/clusters/{cluster}/access/{access}/proxy").Handler(proxy)

	return k
}
