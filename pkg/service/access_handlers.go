package service

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"github.com/faroshq/faros/pkg/models"
	"github.com/faroshq/faros/pkg/service/middleware"
	"github.com/faroshq/faros/pkg/store"
	bcryptutil "github.com/faroshq/faros/pkg/util/bcrypt"
	errutil "github.com/faroshq/faros/pkg/util/error"
	"github.com/faroshq/faros/pkg/util/httputil"
	"github.com/faroshq/faros/pkg/util/kubeconfig"
)

// listClusterAccess list cluster access sessions for specific cluster
func (s *Service) listClusterAccessSession(w http.ResponseWriter, r *http.Request) {
	log := middleware.GetLoggerFromRequest(r)
	cluster, namespace, err := s._getClusterAndNamespace(w, r, log)
	if err != nil {
		return
	}

	results, err := s.controller.ListClusterAccessSessions(r.Context(), namespace.ID, cluster.ID)
	if err != nil && err != store.ErrRecordNotFound {
		log.WithError(err).Error("failed to list cluster access sessions")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, "failed to list cluster access requests"))
		return
	}

	httputil.Respond(w, results)
}

// getClusterAccessSession gets individual cluster access session for specific cluster
func (s *Service) getClusterAccessSession(w http.ResponseWriter, r *http.Request) {
	log := middleware.GetLoggerFromRequest(r)
	result, err := s._getClusterAccessSession(w, r, log)
	if err != nil {
		return
	}

	httputil.Respond(w, result)
}

// getClusterAccessSession gets individual cluster access session for specific cluster
func (s *Service) deleteClusterAccessSession(w http.ResponseWriter, r *http.Request) {
	log := middleware.GetLoggerFromRequest(r)
	result, err := s._getClusterAccessSession(w, r, log)
	if err != nil {
		return
	}

	err = s.controller.DeleteClusterAccessSessions(r.Context(), result.ID)
	if err != nil {
		log.WithError(err).Error("failed to unmarshal request")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusBadRequest, errutil.CloudErrorCodeInvalidParameter, stringErrorFailure))
		return
	}

	httputil.Respond(w, "")
}

// createOrUpdateClusterAccessSession created new cluster access session for specific cluster
func (s *Service) createOrUpdateClusterAccessSession(w http.ResponseWriter, r *http.Request) {
	log := middleware.GetLoggerFromRequest(r)
	cluster, namespace, err := s._getClusterAndNamespace(w, r, log)
	if err != nil {
		return
	}

	var createClusterAccessSessionRequest models.ClusterAccessSession
	if err := read(r, &createClusterAccessSessionRequest); err != nil {
		log.WithError(err).Error("failed to unmarshal request")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusBadRequest, errutil.CloudErrorCodeInvalidParameter, stringErrorFailure))
		return
	}

	// check if path matches payload
	if strings.EqualFold(mux.Vars(r)["access"], createClusterAccessSessionRequest.Name) {
		log.WithError(err).Error("somebody is trying some funky things with payloads")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusBadRequest, errutil.CloudErrorCodeInvalidParameter, stringErrorFailure))
		return
	}

	// Never trust user input
	createClusterAccessSessionRequest.NamespaceID = namespace.ID
	createClusterAccessSessionRequest.ClusterID = cluster.ID
	if createClusterAccessSessionRequest.Name == "" {
		createClusterAccessSessionRequest.Name = uuid.New().String()
	}

	sessions, err := s.controller.ListClusterAccessSessions(r.Context(), namespace.ID, cluster.ID)
	if err != nil {
		log.WithError(err).Error("failed to list cluster access sessions")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
		return
	}

	var isUpdate bool
	var session *models.ClusterAccessSession

	for _, s := range sessions {
		if s.Name == createClusterAccessSessionRequest.Name {
			session = &s
			isUpdate = true
		}
	}
	if isUpdate {
		// update fields
		session.TTL = createClusterAccessSessionRequest.TTL

		result, err := s.controller.UpdateClusterAccessSession(r.Context(), *session)
		if err != nil {
			log.WithError(err).Error("failed to update cluster access session")
			errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
			return
		}
		httputil.Respond(w, result)
		return
	}

	// else create new one
	result, err := s.controller.CreateClusterAccessSession(r.Context(), createClusterAccessSessionRequest)
	if err != nil {
		log.WithError(err).Error("failed to create cluster access session")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
		return
	}

	httputil.Respond(w, result)
}

// createOrUpdateClusterAccessSessionKubeconfig creates or updates kubeconfig for specific cluster access session
// Update might happen if user lost kubeconfig and wants to generate new one
func (s *Service) createOrUpdateClusterAccessSessionKubeconfig(w http.ResponseWriter, r *http.Request) {
	log := middleware.GetLoggerFromRequest(r)
	session, err := s._getClusterAccessSession(w, r, log)
	if err != nil {
		return
	}

	// TODO: Maybe it would be good to move this to controller layer and make api server
	// without this logic
	token := uuid.New().String()
	hashedToken, err := bcryptutil.HashPassword(token)
	if err != nil {
		log.WithError(err).Error("failed to hash token")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
		return
	}

	session.EncryptedToken = base64.RawStdEncoding.EncodeToString(hashedToken)
	session.Token = token

	// update existing session with new details
	_, err = s.controller.UpdateClusterAccessSession(r.Context(), *session)
	if err != nil {
		log.WithError(err).Error("failed to create cluster access session")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
		return
	}

	// at this point we can generate kubeconfig and return tu user
	kubeconfig, err := s._generateKubeConfig(r.Context(), session)
	if err != nil {
		log.WithError(err).Error("failed to generate kubeconfig")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
		return
	}

	kc := models.KubeConfig{
		KubeConfig: base64.RawStdEncoding.EncodeToString(kubeconfig),
	}

	httputil.Respond(w, kc)
}

func (s *Service) _generateKubeConfig(ctx context.Context, session *models.ClusterAccessSession) ([]byte, error) {
	path := fmt.Sprintf("/namespaces/%s/clusters/%s/access/%s/proxy",
		session.NamespaceID, session.ClusterID, session.ID)
	server := "https://" + s.config.API.URI + path

	return kubeconfig.MakeKubeconfig(server, session.Token)
}
