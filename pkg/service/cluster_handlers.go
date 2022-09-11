package service

import (
	"net/http"
	"strings"

	"github.com/faroshq/faros/pkg/models"
	"github.com/faroshq/faros/pkg/service/middleware"
	errutil "github.com/faroshq/faros/pkg/util/error"
	"github.com/faroshq/faros/pkg/util/httputil"
)

func (s *Service) getCluster(w http.ResponseWriter, r *http.Request) {
	log := middleware.GetLoggerFromRequest(r)
	result, _, err := s._getClusterAndNamespace(w, r, log)
	if err != nil {
		return
	}

	httputil.Respond(w, result)
}

func (s *Service) listClusters(w http.ResponseWriter, r *http.Request) {
	log := middleware.GetLoggerFromRequest(r)
	namespace, err := s._getNamespace(w, r, log)
	if err != nil {
		return
	}

	result, err := s.controller.ListClusters(r.Context(), namespace.ID)
	if err != nil {
		log.WithError(err).Error("failed to list clusters")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
		return
	}

	httputil.Respond(w, result)
}

func (s *Service) createOrUpdateCluster(w http.ResponseWriter, r *http.Request) {
	log := middleware.GetLoggerFromRequest(r)
	namespace, err := s._getNamespace(w, r, log)
	if err != nil {
		return
	}

	var createClusterRequest models.Cluster
	if err := read(r, &createClusterRequest); err != nil {
		log.WithError(err).Error("failed to unmarshal request")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusBadRequest, errutil.CloudErrorCodeInvalidParameter, stringErrorFailure))
		return
	}

	createClusterRequest.NamespaceID = namespace.ID

	clusters, err := s.controller.ListClusters(r.Context(), namespace.ID)
	if err != nil {
		log.WithError(err).Error("failed to list clusters")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
		return
	}

	var cluster models.Cluster
	var isUpdate bool
	for _, c := range clusters {
		if strings.EqualFold(c.Name, createClusterRequest.Name) {
			isUpdate = true
			cluster = c
		}
	}

	if isUpdate {
		// Update fields
		cluster.Config.RawKubeConfig = createClusterRequest.Config.RawKubeConfig

		result, err := s.controller.UpdateCluster(r.Context(), cluster)
		if err != nil {
			log.WithError(err).Error("failed to update cluster")
			errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
			return
		}
		result.Config.RawKubeConfig = "redacted"

		httputil.Respond(w, result)
		return
	}

	// create
	result, err := s.controller.CreateCluster(r.Context(), createClusterRequest)
	if err != nil {
		log.WithError(err).Error("failed to create cluster")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
		return
	}

	// never return sensitive data
	result.Config.RawKubeConfig = "redacted"

	httputil.Respond(w, result)
}

func (s *Service) deleteCluster(w http.ResponseWriter, r *http.Request) {
	log := middleware.GetLoggerFromRequest(r)
	cluster, _, err := s._getClusterAndNamespace(w, r, log)
	if err != nil {
		return
	}

	if err := s.controller.DeleteCluster(r.Context(), cluster.ID); err != nil {
		log.WithError(err).Error("failed to delete cluster")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
		return
	}

	httputil.Respond(w, "")
}
