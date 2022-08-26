package service

import (
	"net/http"
	"strings"

	"github.com/faroshq/faros/pkg/models"
	"github.com/faroshq/faros/pkg/store"
	errutil "github.com/faroshq/faros/pkg/util/error"
	"github.com/gorilla/mux"
)

// _getNamespace is shared helper to get namespace from request.
// If failed, we should stop processing. Errors will be written to response and
// logged by helper
func (s *Service) _getNamespace(w http.ResponseWriter, r *http.Request) (*models.Namespace, error) {
	namespaceQuery := models.Namespace{}
	namespaceArg := mux.Vars(r)["namespace"]
	if strings.HasPrefix(namespaceArg, models.NamespacePrefix) {
		namespaceQuery.ID = namespaceArg
	} else {
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusNotFound, errutil.CloudErrorCodeNotFound, stringErrorClusterAccessSessionNotFound))
		s.log.WithError(errorIDFormatInvalid).Error("ID format namespace is invalid")
		return nil, errorIDFormatInvalid
	}

	namespace, err := s.store.GetNamespace(r.Context(), namespaceQuery)
	if err != nil {
		if err == store.ErrRecordNotFound {
			errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusNotFound, errutil.CloudErrorCodeNotFound, stringErrorNamespaceNotFound))
			return nil, err
		}
		s.log.WithError(err).Error("failed to get namespace")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
		return nil, err
	}
	return namespace, nil
}

// _getCluster is shared helper to get cluster from request.
// If failed, we should stop processing. Errors will be written to response and
// logged by helper
func (s *Service) _getCluster(w http.ResponseWriter, r *http.Request, namespace *models.Namespace) (*models.Cluster, error) {
	clusterQuery := models.Cluster{
		NamespaceID: namespace.ID,
	}
	clusterArg := mux.Vars(r)["cluster"]
	if strings.HasPrefix(clusterArg, models.ClusterPrefix) {
		clusterQuery.ID = clusterArg
	} else {
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusNotFound, errutil.CloudErrorCodeNotFound, stringErrorClusterAccessSessionNotFound))
		s.log.WithError(errorIDFormatInvalid).Error("ID format cluster is invalid")
		return nil, errorIDFormatInvalid
	}

	cluster, err := s.store.GetCluster(r.Context(), clusterQuery)
	if err != nil {
		if err == store.ErrRecordNotFound {
			errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusNotFound, errutil.CloudErrorCodeNotFound, stringErrorClusterNotFound))
			return nil, err
		}
		s.log.WithError(err).Error("failed to get clusters")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
	}
	cluster.Config.RawKubeConfig = "redacted"
	return cluster, nil
}

// _getClusterAccessSession is shared helper to get cluster access session from request.
// If failed, we should stop processing. Errors will be written to response and
// logged by helper
func (s *Service) _getClusterAccessSession(w http.ResponseWriter, r *http.Request) (*models.ClusterAccessSession, error) {
	namespace, err := s._getNamespace(w, r)
	if err != nil {
		return nil, err
	}
	cluster, err := s._getCluster(w, r, namespace)
	if err != nil {
		return nil, err
	}

	query := models.ClusterAccessSession{
		ClusterID:   cluster.ID,
		NamespaceID: namespace.ID,
	}
	clusterAccessSessionArg := mux.Vars(r)["access"]
	if strings.HasPrefix(clusterAccessSessionArg, models.ClusterAccessSessionPrefix) {
		query.ID = clusterAccessSessionArg
	} else {
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusNotFound, errutil.CloudErrorCodeNotFound, stringErrorClusterAccessSessionNotFound))
		s.log.WithError(errorIDFormatInvalid).Error("ID format cluster access session is invalid")
		return nil, errorIDFormatInvalid
	}

	session, err := s.store.GetClusterAccessSession(r.Context(), query)
	if err != nil {
		if err == store.ErrRecordNotFound {
			errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusNotFound, errutil.CloudErrorCodeNotFound, stringErrorClusterAccessSessionNotFound))
			return nil, err
		}
		s.log.WithError(err).Error("failed to get cluster access session")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
	}
	return session, nil
}

// _getClusterAndNamespace is shared helper to get cluster and namespace from request.
// If failed, we should stop processing. Errors will be written to response and
// logged by helper
func (s *Service) _getClusterAndNamespace(w http.ResponseWriter, r *http.Request) (*models.Cluster, *models.Namespace, error) {
	namespace, err := s._getNamespace(w, r)
	if err != nil {
		return nil, nil, err
	}
	cluster, err := s._getCluster(w, r, namespace)
	if err != nil {
		return nil, nil, err
	}

	return cluster, namespace, nil
}
