package service

import (
	"context"
	"net/http"
	"path"
	"time"

	restful "github.com/emicklei/go-restful/v3"
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
	"github.com/faroshq/faros/pkg/service/authentications"
	authjwt "github.com/faroshq/faros/pkg/service/authentications/jwt"
	"github.com/faroshq/faros/pkg/store"
	k8sstore "github.com/faroshq/faros/pkg/store/k8s"
	"github.com/faroshq/faros/pkg/util/recover"
)

var (
	scheme = runtime.NewScheme()
	codecs = serializer.NewCodecFactory(scheme)
)

var _ Interface = &service{}

func init() {
	utilruntime.Must(tenancyv1alpha1.AddToScheme(scheme))
}

type Interface interface {
	Run(ctx context.Context) error
}

type service struct {
	config config.APIConfig
	store  store.Store

	server *http.Server

	kcpClient   kcpclient.ClusterInterface
	farosClient farosclient.Interface

	authentications authentications.Authenticator
}

func New(ctx context.Context, config *config.Config) (Interface, error) {
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

	jwtAuthenticator, err := authjwt.New(ctx, config, store, path.Join(pathOIDC, oidcCallback))
	if err != nil {
		return nil, err
	}

	s := &service{
		config:          apiConfig,
		store:           store,
		kcpClient:       kcpClient,
		farosClient:     farosClient.Cluster(logicalcluster.NewPath(config.FarosKCPConfig.ControllersTenantWorkspace)),
		authentications: jwtAuthenticator,
	}

	wsContainer := restful.NewContainer()
	wsContainer.EnableContentEncoding(true)

	// TODO: Loop over all resources and register them
	u := OrganizationResource{
		store:           s.store,
		authentications: jwtAuthenticator,
		farosClient:     s.farosClient,
	}
	u.RegisterTo(wsContainer)

	// OIDC methods
	oidc := OIDCResource{
		store:           s.store,
		authentications: jwtAuthenticator,
	}
	oidc.RegisterTo(wsContainer)

	swagger := SwaggerResource{}
	swagger.RegisterTo(wsContainer)

	// Add container filter to enable CORS
	cors := restful.CrossOriginResourceSharing{
		AllowedHeaders: []string{"Content-Type", "Accept"},
		AllowedMethods: []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions},
		AllowedDomains: apiConfig.AllowedCORSOrigins,
		CookiesAllowed: true,
		Container:      wsContainer,
	}

	wsContainer.Filter(cors.Filter)

	// Add container filter to respond to OPTIONS
	wsContainer.Filter(wsContainer.OPTIONSFilter)

	s.server = &http.Server{
		Addr:    apiConfig.Addr,
		Handler: wsContainer,
	}

	return s, nil
}

func (s *service) Run(ctx context.Context) error {
	klog.Info("Starting API Service")

	go func() {
		defer recover.Panic()
		<-ctx.Done()

		ctxS, cancel := context.WithTimeout(context.Background(), time.Second*30)
		defer cancel()
		err := s.server.Shutdown(ctxS)
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
	return nil
}

func (s *service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.server.Handler.ServeHTTP(w, r)
}
