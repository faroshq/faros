package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/faroshq/faros/pkg/models"
	"github.com/faroshq/faros/pkg/store"
	"github.com/faroshq/faros/pkg/util/auth"
)

// KubeConfigAuthentication validates a Bearer token fields and sets ClusterAccessSession
// object into context if authentication is valid
func KubeConfigAuthentication(log *logrus.Entry, auth auth.Authenticator, store store.Store) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

			request, err := store.GetClusterAccessSession(r.Context(), models.ClusterAccessSession{
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

			authenticated, err := auth.AuthenticateClusterAccessSession(ctx, request, token)
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
			cluster, err := store.GetCluster(r.Context(), models.Cluster{
				NamespaceID: namespaceID,
				ID:          clusterID,
			})
			if err != nil {
				log.WithError(err).Error("failed to get cluster in middleware")
				w.WriteHeader(http.StatusForbidden)
				return
			}

			ctx = context.WithValue(ctx, ContextKeyClusterAccessSession, request)
			ctx = context.WithValue(ctx, ContextKeyCluster, cluster)
			r = r.WithContext(ctx)

			h.ServeHTTP(w, r)
		})
	}
}
