package server

import (
	"context"
	"net/http"
	"path"
	"time"

	health "github.com/InVisionApp/go-health/v2"
	healthhandlers "github.com/InVisionApp/go-health/v2/handlers"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	kcpclient "github.com/kcp-dev/kcp/pkg/client/clientset/versioned/cluster"
	"github.com/kcp-dev/logicalcluster/v3"
	"go.uber.org/zap"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	farosclient "github.com/faroshq/faros/pkg/client/clientset/versioned"
	farosclusterclient "github.com/faroshq/faros/pkg/client/clientset/versioned/cluster"
	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/server/auth"
	"github.com/faroshq/faros/pkg/store"
	k8sstore "github.com/faroshq/faros/pkg/store/k8s"
	"github.com/faroshq/faros/pkg/util/recover"
)

var (
	scheme       = runtime.NewScheme()
	codecs       = serializer.NewCodecFactory(scheme)
	limit  int64 = 1024 * 1024 * 10
)

func init() {
	utilruntime.Must(tenancyv1alpha1.AddToScheme(scheme))
}

var _ Interface = &Service{}

type Interface interface {
	Run(ctx context.Context) error
}

var (
	pathAPIVersion             = "/faros.sh/api/v1alpha1"
	pathOrganizations          = "/organizations"
	pathWorkspaces             = "/workspaces"
	pathOIDC                   = "/oidc"
	pathOIDCLogin              = path.Join(pathOIDC, "/login")
	pathOIDCCallback           = path.Join(pathOIDC, "/callback")
	pathOrganizationWorkspaces = path.Join(pathOrganizations, "{organization}", pathWorkspaces)
)

type Service struct {
	config        config.APIConfig
	authenticator auth.Authenticator
	server        *http.Server
	router        *mux.Router
	health        *health.Health
	store         store.Store

	kcpClient   kcpclient.ClusterInterface
	farosClient farosclient.Interface
}

func New(ctx context.Context, config *config.Config) (*Service, error) {
	apiConfig := config.APIConfig
	kcpConfig := config.FarosKCPConfig

	store, err := k8sstore.New(ctx, *config)
	if err != nil {
		return nil, err
	}

	kcpClient, err := kcpclient.NewForConfig(kcpConfig.KCPClusterRestConfig)
	if err != nil {
		return nil, err
	}

	// farosClient is used to manage tenants workspace objects only
	farosClient, err := farosclusterclient.NewForConfig(kcpConfig.KCPClusterRestConfig)
	if err != nil {
		return nil, err
	}

	authenticator, err := auth.NewAuthenticator(
		*config,
		store,
		path.Join(pathAPIVersion, pathOIDCCallback),
	)
	if err != nil {
		return nil, err
	}

	s := &Service{
		config:        apiConfig,
		health:        health.New(),
		store:         store,
		kcpClient:     kcpClient,
		farosClient:   farosClient.Cluster(logicalcluster.NewPath(config.FarosKCPConfig.ControllersTenantWorkspace)),
		authenticator: authenticator,
	}

	s.router = setupRouter()
	s.router.HandleFunc("/healthz", healthhandlers.NewJSONHandlerFunc(s.health, nil)) // /healthz

	apiRouter := s.router.PathPrefix(pathAPIVersion).Subrouter()
	apiRouter.HandleFunc(pathOIDCLogin, s.oidcLogin)       // /faros.sh/api/v1alpha1/oidc/login
	apiRouter.HandleFunc(pathOIDCCallback, s.oidcCallback) // /faros.sh/api/v1alpha1/oidc/callback

	apiRouter.HandleFunc(path.Join(pathOrganizations), s.listOrganizations).Methods(http.MethodGet)                       // /faros.sh/api/v1alpha1/organizations
	apiRouter.HandleFunc(path.Join(pathOrganizations, "{organization}"), s.getOrganization).Methods(http.MethodGet)       // /faros.sh/api/v1alpha1/organizations/{organization}
	apiRouter.HandleFunc(path.Join(pathOrganizations, "{organization}"), s.deleteOrganization).Methods(http.MethodDelete) // /faros.sh/api/v1alpha1/organizations/{organization}
	apiRouter.HandleFunc(pathOrganizations, s.createOrganization).Methods(http.MethodPost)

	apiRouter.HandleFunc(pathOrganizationWorkspaces, s.listWorkspaces).Methods(http.MethodGet) // /faros.sh/api/v1alpha1/organizations/{organization}/workspaces
	//apiRouter.HandleFunc(path.Join(pathWorkspaces, "{workspace}"), s.getWorkspace).Methods(http.MethodGet)       // /faros.sh/api/v1alpha1/workspaces/{workspace}
	apiRouter.HandleFunc(path.Join(pathOrganizationWorkspaces, "{workspace}"), s.deleteWorkspace).Methods(http.MethodDelete) // /faros.sh/api/v1alpha1/organizations/{organization}/workspaces/{workspace}
	apiRouter.HandleFunc(pathOrganizationWorkspaces, s.createWorkspace).Methods(http.MethodPost)                             // /faros.sh/api/v1alpha1/organizations/{organization}/workspaces

	s.server = &http.Server{
		Addr: apiConfig.Addr,
		Handler: handlers.CORS(
			handlers.AllowCredentials(),
			handlers.AllowedHeaders([]string{"Content-Type"}),
			handlers.AllowedMethods([]string{"GET", "POST", "PUT", "PATCH", "DELETE"}),
		)(s),
	}

	return s, nil
}

func (s *Service) Run(ctx context.Context) error {
	klog.Info("Starting API Service")
	go func() {
		defer recover.Panic()
		<-ctx.Done()

		err := s.health.Stop()
		if err != nil {
			klog.Error(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
		defer cancel()
		err = s.server.Shutdown(ctx)
		if err != nil {
			klog.Error("api shutdown error", zap.Error(err))
		}
		klog.Info("Stopped API Service")
	}()

	klog.V(2).Info("Server will now listen", "url", s.config.Addr)
	err := s.server.ListenAndServeTLS(s.config.TLSCertFile, s.config.TLSKeyFile)
	if err != nil {
		klog.Error("api listen error", zap.Error(err))
	}
	return err
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

func setupRouter() *mux.Router {
	r := mux.NewRouter()
	r.Use(Panic())
	r.Use(Gzip())
	r.Use(Log())

	r.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
	})

	return r
}
