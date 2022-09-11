package kubectl

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"

	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/controller"
	"github.com/faroshq/faros/pkg/models"
	"github.com/faroshq/faros/pkg/service/authentication"
	"github.com/faroshq/faros/pkg/service/middleware"
)

var _ authentication.Authentication = &KubeCtlAuth{}

type KubeCtlAuth struct {
	log        *logrus.Entry
	config     *config.Config
	controller controller.Controller
}

func New(log *logrus.Entry, config *config.Config, controller controller.Controller) (*KubeCtlAuth, error) {

	b := &KubeCtlAuth{
		log:        log,
		config:     config,
		controller: controller,
	}

	return b, nil
}

func (k *KubeCtlAuth) Authenticate() func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log := middleware.GetLoggerFromRequest(r)
			ctx := r.Context()

			namespaceID := mux.Vars(r)["namespace"]
			clusterID := mux.Vars(r)["cluster"]
			accessID := mux.Vars(r)["access"]

			authorization := r.Header.Get("Authorization")
			if !strings.HasPrefix(authorization, "Bearer ") {
				log.Error("header does not contain Bearer token")
				w.WriteHeader(http.StatusForbidden)
				return
			}

			token := strings.TrimPrefix(authorization, "Bearer ")

			request, err := k.controller.GetClusterAccessSession(ctx, models.ClusterAccessSession{
				NamespaceID: namespaceID,
				ClusterID:   clusterID,
				ID:          accessID,
			})
			if err != nil {
				log.WithError(err).Error("failed to get cluster access session in middleware")
				w.WriteHeader(http.StatusForbidden)
				return
			}

			if request.Expired {
				log.Error("cluster access session expired")
				w.WriteHeader(http.StatusForbidden)
				return
			}

			authenticated, err := k.authenticateClusterAccessSession(ctx, log, request, token)
			if err != nil {
				log.WithError(err).Error("failed to authenticate cluster access session in middleware")
				w.WriteHeader(http.StatusForbidden)
				return
			}
			if !authenticated {
				log.Error("token is not valid")
				w.WriteHeader(http.StatusForbidden)
				return
			}

			// if authenticated we will need cluster to proxy the result
			cluster, err := k.controller.GetCluster(ctx, namespaceID, clusterID)
			if err != nil {
				log.WithError(err).Error("failed to get cluster in middleware")
				w.WriteHeader(http.StatusForbidden)
				return
			}

			ctx = context.WithValue(ctx, middleware.ContextKeyClusterAccessSession, request)
			ctx = context.WithValue(ctx, middleware.ContextKeyCluster, cluster)

			r = r.WithContext(ctx)

			h.ServeHTTP(w, r)
		})
	}
}

func (b *KubeCtlAuth) Enabled() bool {
	return true
}

var (
	ErrorAuthenticationFailed = fmt.Errorf("authentication failed")
)

// Authenticate is used to authenticate cluster access sessions
func (b *KubeCtlAuth) authenticateClusterAccessSession(ctx context.Context, log *logrus.Entry, req *models.ClusterAccessSession, token string) (bool, error) {
	decoded, err := base64.RawStdEncoding.DecodeString(req.EncryptedToken)
	if err != nil {
		log.WithError(err).Error("failed to decode token")
		return false, err
	}

	if bcrypt.CompareHashAndPassword(decoded, []byte(token)); err == nil {
		return true, nil
	}

	return false, ErrorAuthenticationFailed
}
