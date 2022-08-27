package service

import (
	"net/http"
	"strings"

	"github.com/davecgh/go-spew/spew"
	"github.com/faroshq/faros/pkg/models"
	errutil "github.com/faroshq/faros/pkg/util/error"
	"github.com/faroshq/faros/pkg/util/httputil"
)

func (s *Service) getCluster(w http.ResponseWriter, r *http.Request) {
	result, _, err := s._getClusterAndNamespace(w, r)
	if err != nil {
		return
	}

	httputil.Respond(w, result)
}

func (s *Service) listClusters(w http.ResponseWriter, r *http.Request) {
	namespace, err := s._getNamespace(w, r)
	if err != nil {
		return
	}

	clusterQuery := models.Cluster{
		NamespaceID: namespace.ID,
	}

	result, err := s.store.ListClusters(r.Context(), clusterQuery)
	if err != nil {
		s.log.WithError(err).Error("failed to list clusters")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
		return
	}

	for idx := range result {
		result[idx].Config.RawKubeConfig = "redacted"
	}

	httputil.Respond(w, result)
}

func (s *Service) createOrUpdateCluster(w http.ResponseWriter, r *http.Request) {
	spew.Dump("createOrUpdateCluster")
	namespace, err := s._getNamespace(w, r)
	if err != nil {
		return
	}

	var createClusterRequest models.Cluster
	if err := read(r, &createClusterRequest); err != nil {
		s.log.WithError(err).Error("failed to unmarshal request")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusBadRequest, errutil.CloudErrorCodeInvalidParameter, stringErrorFailure))
		return
	}

	createClusterRequest.NamespaceID = namespace.ID

	query := models.Cluster{
		NamespaceID: namespace.ID,
	}

	clusters, err := s.store.ListClusters(r.Context(), query)
	if err != nil {
		s.log.WithError(err).Error("failed to list clusters")
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

		result, err := s.store.UpdateCluster(r.Context(), cluster)
		if err != nil {
			s.log.WithError(err).Error("failed to update cluster")
			errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
			return
		}
		result.Config.RawKubeConfig = "redacted"

		httputil.Respond(w, result)
		return
	}

	// create
	result, err := s.store.CreateCluster(r.Context(), createClusterRequest)
	if err != nil {
		s.log.WithError(err).Error("failed to create cluster")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
		return
	}

	// never return sensitive data
	result.Config.RawKubeConfig = "redacted"

	httputil.Respond(w, result)
}

func (s *Service) deleteCluster(w http.ResponseWriter, r *http.Request) {
	cluster, _, err := s._getClusterAndNamespace(w, r)
	if err != nil {
		return
	}

	if err := s.store.DeleteCluster(r.Context(), *cluster); err != nil {
		s.log.WithError(err).Error("failed to delete cluster")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
		return
	}

	httputil.Respond(w, "")
}
