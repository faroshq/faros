package service

import (
	"net/http"
	"strings"

	"github.com/faroshq/faros/pkg/models"
	"github.com/faroshq/faros/pkg/service/middleware"
	errutil "github.com/faroshq/faros/pkg/util/error"
	"github.com/faroshq/faros/pkg/util/httputil"
)

func (s *Service) getNamespace(w http.ResponseWriter, r *http.Request) {
	log := middleware.GetLoggerFromRequest(r)
	result, err := s._getNamespace(w, r, log)
	if err != nil {
		return
	}
	httputil.Respond(w, result)
}

func (s *Service) listNamespaces(w http.ResponseWriter, r *http.Request) {
	result, err := s.controller.ListNamespaces(r.Context())
	if err != nil {
		s.log.WithError(err).Errorf("failed to list namespaces")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
		return
	}

	httputil.Respond(w, result)
}

func (s *Service) createOrUpdateNamespace(w http.ResponseWriter, r *http.Request) {
	var createNamespaceRequest models.Namespace
	if err := read(r, &createNamespaceRequest); err != nil {
		s.log.WithError(err).Error("failed to unmarshal")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusBadRequest, errutil.CloudErrorCodeInvalidParameter, stringErrorFailure))
		return
	}

	namespaces, err := s.controller.ListNamespaces(r.Context())
	if err != nil {
		s.log.WithError(err).Error("failed to list namespaces")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusBadRequest, errutil.CloudErrorCodeInvalidParameter, stringErrorFailure))
		return
	}

	var isUpdate bool
	var namespace models.Namespace
	for _, n := range namespaces {
		if strings.EqualFold(n.Name, createNamespaceRequest.Name) {
			isUpdate = true
			namespace = n
			break
		}
	}

	if isUpdate {
		// Update fields
		namespace.Description = createNamespaceRequest.Description

		result, err := s.controller.UpdateNamespace(r.Context(), namespace)
		if err != nil {
			s.log.WithError(err).Error("failed to update namespace")
			errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
			return
		}
		httputil.Respond(w, result)
		return
	}

	// create
	result, err := s.controller.CreateNamespace(r.Context(), createNamespaceRequest)
	if err != nil {
		s.log.WithError(err).Error("failed to create namespace")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
		return
	}

	httputil.Respond(w, result)
}

func (s *Service) deleteNamespace(w http.ResponseWriter, r *http.Request) {
	log := middleware.GetLoggerFromRequest(r)
	namespace, err := s._getNamespace(w, r, log)
	if err != nil {
		return
	}

	clusters, err := s.controller.ListClusters(r.Context(), namespace.ID)
	if err != nil {
		s.log.WithError(err).Error("failed to list clusters")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
		return
	}

	for _, cluster := range clusters {
		if err := s.controller.DeleteCluster(r.Context(), cluster.ID); err != nil {
			s.log.WithError(err).Error("failed to delete cluster")
			errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
			return
		}
	}

	if err := s.controller.DeleteNamespace(r.Context(), namespace.ID); err != nil {
		s.log.WithError(err).Error("failed to delete namespace")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
		return
	}

	httputil.Respond(w, "")
}
