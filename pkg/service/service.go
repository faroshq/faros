package service

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/InVisionApp/go-health/v2"
	healthhandlers "github.com/InVisionApp/go-health/v2/handlers"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/controller"
	"github.com/faroshq/faros/pkg/service/authentication/basicauth"
	"github.com/faroshq/faros/pkg/service/kubeconfig"
	"github.com/faroshq/faros/pkg/service/middleware"
	errutil "github.com/faroshq/faros/pkg/util/error"
	"github.com/faroshq/faros/pkg/util/recover"
)

var _ Interface = &Service{}

type Interface interface {
	Run(context.Context) error
}

type Service struct {
	log      *logrus.Entry
	server   *http.Server
	listener net.Listener
	router   *mux.Router
	config   *config.Config

	controller controller.Controller

	servingKey   *rsa.PrivateKey
	servingCerts []*x509.Certificate
}

func New(
	ctx context.Context,
	logger *logrus.Entry,
	config *config.Config,
	controller controller.Controller,
	health *health.Health,
) (*Service, error) {
	s := &Service{
		log:        logger.WithField("component", "api"),
		config:     config,
		controller: controller,
	}

	// setup serving certs
	key, err := x509.ParsePKCS1PrivateKey(config.API.TLSKey)
	if err != nil {
		return nil, fmt.Errorf("error parsing private key: %s", err)
	}
	s.servingKey = key

	certs, err := x509.ParseCertificates(config.API.TLSCert)
	if err != nil {
		return nil, fmt.Errorf("error parsing certificate: %s", err)
	}
	s.servingCerts = certs

	// setup router and base middleware
	s.router, err = s.setupRouter()
	if err != nil {
		return nil, err
	}

	// setup proxy and proxy authentication
	err = s.setupProxyRoutes()
	if err != nil {
		return nil, err
	}

	err = s.setupAPIRoutes()
	if err != nil {
		return nil, err
	}

	err = s.setupDebugRouter()
	if err != nil {
		return nil, err
	}

	// setup health under root router
	s.router.HandleFunc("/healthz", healthhandlers.NewJSONHandlerFunc(health, nil))
	s.router.PathPrefix("/api").HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusNotFound, errutil.CloudErrorCodeInternalServerError, "404, whatever you are looking for does not exist"))
	})

	l, err := net.Listen("tcp", config.API.URI)
	if err != nil {
		return nil, err
	}
	s.listener = l

	s.server = &http.Server{
		ReadTimeout: 10 * time.Second,
		IdleTimeout: 2 * time.Minute,
		ErrorLog:    log.New(s.log.Writer(), "", 0),
		BaseContext: func(net.Listener) context.Context { return ctx },
		Handler: handlers.CORS(
			handlers.AllowCredentials(),
			handlers.AllowedHeaders([]string{"Content-Type"}),
			handlers.AllowedMethods([]string{"GET", "POST", "PUT", "PATCH", "DELETE"}),
			handlers.AllowedOrigins(config.API.AllowedOrigins),
		)(s),
	}

	return s, nil
}

func (s *Service) Run(ctx context.Context) error {
	s.log.WithFields(logrus.Fields{
		"uri": s.config.API.URI,
		"tls": s.config.API.TLSCertPath,
	}).Info("Starting API Service")
	go func() {
		defer recover.Panic(s.log)

		<-ctx.Done()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
		defer cancel()
		err := s.server.Shutdown(ctx)
		if err != nil {
			s.log.Errorf("api shutdown error: %s", err)
		}
		s.log.Info("Stopped API Service")
	}()

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{
			{
				PrivateKey: s.servingKey,
			},
		},
		NextProtos: []string{"h2", "http/1.1"},
		CipherSuites: []uint16{
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
		},
		PreferServerCipherSuites: true,
		SessionTicketsDisabled:   true,
		MinVersion:               tls.VersionTLS12,
		CurvePreferences: []tls.CurveID{
			tls.CurveP256,
			tls.X25519,
		},
	}

	for _, cert := range s.servingCerts {
		tlsConfig.Certificates[0].Certificate = append(tlsConfig.Certificates[0].Certificate, cert.Raw)
	}

	s.log.Info("Starting API Service with TLS")
	return s.server.Serve(tls.NewListener(s.listener, tlsConfig))
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

func (s *Service) setupRouter() (*mux.Router, error) {
	r := mux.NewRouter()

	r.Use(middleware.Panic(s.log)) // must be first as its saves from server going under
	r.Use(middleware.Log(s.log))   // must be second as it sets request logger into context

	r.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
	})

	return r, nil
}

// setupProxyRoutes configure proxy routes
func (s *Service) setupProxyRoutes() error {
	return kubeconfig.New(s.log, s.config, s.controller, s.router)
}

// setupAPIRoutes will configure API server routes
func (s *Service) setupAPIRoutes() error {
	// setup all the user routes
	apiRouter := s.router.PathPrefix("/api/v1").Subrouter()

	apiRouter.HandleFunc("/namespaces", s.createOrUpdateNamespace).Methods(http.MethodPost)
	apiRouter.HandleFunc("/namespaces", s.listNamespaces).Methods(http.MethodGet)
	apiRouter.HandleFunc("/namespaces/{namespace}", s.getNamespace).Methods(http.MethodGet)
	apiRouter.HandleFunc("/namespaces/{namespace}", s.createOrUpdateNamespace).Methods(http.MethodPost)
	apiRouter.HandleFunc("/namespaces/{namespace}", s.deleteNamespace).Methods(http.MethodDelete)

	apiRouter.HandleFunc("/namespaces/{namespace}/clusters", s.listClusters).Methods(http.MethodGet)
	apiRouter.HandleFunc("/namespaces/{namespace}/clusters", s.createOrUpdateCluster).Methods(http.MethodPost)
	apiRouter.HandleFunc("/namespaces/{namespace}/clusters/{cluster}", s.createOrUpdateCluster).Methods(http.MethodPost)
	apiRouter.HandleFunc("/namespaces/{namespace}/clusters/{cluster}", s.getCluster).Methods(http.MethodGet)
	apiRouter.HandleFunc("/namespaces/{namespace}/clusters/{cluster}", s.deleteCluster).Methods(http.MethodDelete)

	apiRouter.HandleFunc("/namespaces/{namespace}/clusters/{cluster}/access", s.listClusterAccessSession).Methods(http.MethodGet)
	apiRouter.HandleFunc("/namespaces/{namespace}/clusters/{cluster}/access", s.createOrUpdateClusterAccessSession).Methods(http.MethodPost)
	apiRouter.HandleFunc("/namespaces/{namespace}/clusters/{cluster}/access/{access}", s.createOrUpdateClusterAccessSession).Methods(http.MethodPost)
	apiRouter.HandleFunc("/namespaces/{namespace}/clusters/{cluster}/access{access}", s.getClusterAccessSession).Methods(http.MethodGet)
	apiRouter.HandleFunc("/namespaces/{namespace}/clusters/{cluster}/access/{access}", s.deleteClusterAccessSession).Methods(http.MethodDelete)
	// This is post. All methods dealing with security should be POST
	apiRouter.HandleFunc("/namespaces/{namespace}/clusters/{cluster}/access/{access}/kubeconfig", s.createOrUpdateClusterAccessSessionKubeconfig).Methods(http.MethodPost)

	basicAuthMiddleware, err := basicauth.New(s.log, s.config, s.controller)
	if err != nil {
		s.log.Errorf("failed to create basic auth middleware: %s", err)
		return err
	}
	// set basic authentication on api router
	apiRouter.Use(basicAuthMiddleware.Authenticate()) // basic auth middleware
	return nil
}

// setupDebugRouter will configure API server routes
func (s *Service) setupDebugRouter() error {
	// setup all the user routes
	debugRouter := s.router.PathPrefix("/debug").Subrouter()

	debugRouter.HandleFunc("/pprof/cmdline", pprof.Cmdline)
	debugRouter.HandleFunc("/pprof/profile", pprof.Profile)
	debugRouter.HandleFunc("/pprof/symbol", pprof.Symbol)
	debugRouter.HandleFunc("/pprof/trace", pprof.Trace)
	debugRouter.PathPrefix("/pprof/").Handler(http.StripPrefix("/api", http.HandlerFunc(pprof.Index)))

	basicAuthMiddleware, err := basicauth.New(s.log, s.config, s.controller)
	if err != nil {
		s.log.Errorf("failed to create basic auth middleware: %s", err)
		return err
	}
	// set basic authentication on debug router
	debugRouter.Use(basicAuthMiddleware.Authenticate()) // basic auth middleware

	return nil
}
