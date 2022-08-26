package service

import (
	"net/http"
	"strings"

	"github.com/faroshq/faros/pkg/models"
	"github.com/faroshq/faros/pkg/store"
	errutil "github.com/faroshq/faros/pkg/util/error"
	httputil "github.com/faroshq/faros/pkg/util/http"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
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
	session, err := s._getClusterAccessSession(w, r)
	if err != nil {
		return
	}

	httputil.Respond(w, session)
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
		Name:        createClusterAccessSessionRequest.Name,
	}

	_, err = s.store.GetClusterAccessSession(r.Context(), query)
	if err != nil && err != store.ErrRecordNotFound {
		s.log.WithError(err).Error("failed to get cluster access session")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
		return
	}

	// if we managed to get the cluster access session, then it already exists - update
	if err == nil {
		result, err := s.store.UpdateClusterAccessSession(r.Context(), createClusterAccessSessionRequest)
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
