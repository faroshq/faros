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
	"github.com/faroshq/faros/pkg/store"
	errutil "github.com/faroshq/faros/pkg/util/error"
	"github.com/faroshq/faros/pkg/util/httputil"
	"github.com/faroshq/faros/pkg/util/kubeconfig"
)

// listClusterAccess list cluster access sessions for specific cluster
func (s *Service) listClusterAccessSession(w http.ResponseWriter, r *http.Request) {
	cluster, namespace, err := s._getClusterAndNamespace(w, r)
	if err != nil {
		return
	}

	query := models.ClusterAccessSession{
		ClusterID:   cluster.ID,
		NamespaceID: namespace.ID,
	}

	results, err := s.store.ListClusterAccessSessions(r.Context(), query)
	if err != nil && err != store.ErrRecordNotFound {
		s.log.WithError(err).Error("failed to list cluster access sessions")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, "failed to list cluster access requests"))
		return
	}

	httputil.Respond(w, results)
}

// getClusterAccessSession gets individual cluster access session for specific cluster
func (s *Service) getClusterAccessSession(w http.ResponseWriter, r *http.Request) {
	result, err := s._getClusterAccessSession(w, r)
	if err != nil {
		return
	}

	httputil.Respond(w, result)
}

// getClusterAccessSession gets individual cluster access session for specific cluster
func (s *Service) deleteClusterAccessSession(w http.ResponseWriter, r *http.Request) {
	result, err := s._getClusterAccessSession(w, r)
	if err != nil {
		return
	}

	err = s.store.DeleteClusterAccessSession(r.Context(), *result)
	if err != nil {
		s.log.WithError(err).Error("failed to unmarshal request")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusBadRequest, errutil.CloudErrorCodeInvalidParameter, stringErrorFailure))
		return
	}

	httputil.Respond(w, "")
}

// createOrUpdateClusterAccessSession created new cluster access session for specific cluster
func (s *Service) createOrUpdateClusterAccessSession(w http.ResponseWriter, r *http.Request) {
	cluster, namespace, err := s._getClusterAndNamespace(w, r)
	if err != nil {
		return
	}

	var createClusterAccessSessionRequest models.ClusterAccessSession
	if err := read(r, &createClusterAccessSessionRequest); err != nil {
		s.log.WithError(err).Error("failed to unmarshal request")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusBadRequest, errutil.CloudErrorCodeInvalidParameter, stringErrorFailure))
		return
	}

	// check if path matches payload
	if strings.EqualFold(mux.Vars(r)["access"], createClusterAccessSessionRequest.Name) {
		s.log.WithError(err).Error("somebody is trying some funky things with payloads")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusBadRequest, errutil.CloudErrorCodeInvalidParameter, stringErrorFailure))
		return
	}

	// Never trust user input
	createClusterAccessSessionRequest.NamespaceID = namespace.ID
	createClusterAccessSessionRequest.ClusterID = cluster.ID
	if createClusterAccessSessionRequest.Name == "" {
		createClusterAccessSessionRequest.Name = uuid.New().String()
	}

	query := models.ClusterAccessSession{
		NamespaceID: namespace.ID,
		ClusterID:   cluster.ID,
	}

	sessions, err := s.store.ListClusterAccessSessions(r.Context(), query)
	if err != nil {
		s.log.WithError(err).Error("failed to list cluster access sessions")
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

		result, err := s.store.UpdateClusterAccessSession(r.Context(), *session)
		if err != nil {
			s.log.WithError(err).Error("failed to update cluster access session")
			errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
			return
		}
		httputil.Respond(w, result)
		return
	}

	// else create new one
	result, err := s.store.CreateClusterAccessSession(r.Context(), createClusterAccessSessionRequest)
	if err != nil {
		s.log.WithError(err).Error("failed to create cluster access session")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
		return
	}

	httputil.Respond(w, result)
}

// createOrUpdateClusterAccessSessionKubeconfig creates or updates kubeconfig for specific cluster access session
// Update might happen if user lost kubeconfig and wants to generate new one
func (s *Service) createOrUpdateClusterAccessSessionKubeconfig(w http.ResponseWriter, r *http.Request) {
	session, err := s._getClusterAccessSession(w, r)
	if err != nil {
		return
	}

	token := uuid.New().String()
	hashedToken, err := s.auth.HashPassword(token)
	if err != nil {
		s.log.WithError(err).Error("failed to hash token")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
		return
	}

	session.EncryptedToken = base64.RawStdEncoding.EncodeToString(hashedToken)
	session.Token = token

	// update existing session with new details
	_, err = s.store.UpdateClusterAccessSession(r.Context(), *session)
	if err != nil {
		s.log.WithError(err).Error("failed to create cluster access session")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
		return
	}

	// at this point we can generate kubeconfig and return tu user
	kubeconfig, err := s._generateKubeConfig(r.Context(), session)
	if err != nil {
		s.log.WithError(err).Error("failed to generate kubeconfig")
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
