package service

import (
	"encoding/base64"
	"net/http"

	"github.com/faroshq/faros/pkg/models"
	"github.com/faroshq/faros/pkg/service/middleware"
	"github.com/faroshq/faros/pkg/store"
	bcryptutil "github.com/faroshq/faros/pkg/util/bcrypt"
	errutil "github.com/faroshq/faros/pkg/util/error"
	"github.com/faroshq/faros/pkg/util/httputil"
	"github.com/faroshq/faros/pkg/util/stringutils"
	"github.com/google/uuid"
)

// listClusterRegistrationTokens list cluster registration tokens
func (s *Service) listClusterRegistrationTokens(w http.ResponseWriter, r *http.Request) {
	log := middleware.GetLoggerFromRequest(r)
	namespace, err := s._getNamespace(w, r, log)
	if err != nil {
		return
	}

	results, err := s.controller.ListRegistrationToken(r.Context(), namespace.ID)
	if err != nil && err != store.ErrRecordNotFound {
		log.WithError(err).Error("failed to list cluster registration tokens")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, "failed to list cluster registration tokens"))
		return
	}

	httputil.Respond(w, results)
}

// getClusterAccessSession gets individual cluster access session for specific cluster
func (s *Service) deleteClusterRegistrationToken(w http.ResponseWriter, r *http.Request) {
	log := middleware.GetLoggerFromRequest(r)
	result, err := s._getClusterAccessSession(w, r, log)
	if err != nil {
		return
	}

	err = s.controller.DeleteRegistrationToken(r.Context(), result.ID)
	if err != nil {
		log.WithError(err).Error("failed to unmarshal request")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusBadRequest, errutil.CloudErrorCodeInvalidParameter, stringErrorFailure))
		return
	}

	httputil.Respond(w, "")
}

// createClusterRegistrationToken created new cluster registration token
func (s *Service) createClusterRegistrationToken(w http.ResponseWriter, r *http.Request) {
	log := middleware.GetLoggerFromRequest(r)
	namespace, err := s._getNamespace(w, r, log)
	if err != nil {
		return
	}

	var createClusterRegistrationTokenRequest models.ClusterRegistrationToken
	if err := read(r, &createClusterRegistrationTokenRequest); err != nil {
		log.WithError(err).Error("failed to unmarshal request")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusBadRequest, errutil.CloudErrorCodeInvalidParameter, stringErrorFailure))
		return
	}

	// Never trust user input
	createClusterRegistrationTokenRequest.NamespaceID = namespace.ID
	if createClusterRegistrationTokenRequest.ClusterName == "" {
		createClusterRegistrationTokenRequest.ClusterName = stringutils.GetRandomName()
	}

	sessions, err := s.controller.ListRegistrationToken(r.Context(), namespace.ID)
	if err != nil {
		log.WithError(err).Error("failed to list cluster access sessions")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
		return
	}

	for _, s := range sessions {
		if s.ClusterName == createClusterRegistrationTokenRequest.ClusterName {
			log.WithError(err).Error("cluster registration token for this cluster already exists")
			errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusConflict, errutil.CloudErrorCodeConflict, stringErrorClusterRegistrationAlreadyExistsFound))
			return
		}
	}

	token := uuid.New().String()
	hashedToken, err := bcryptutil.HashPassword(token)
	if err != nil {
		log.WithError(err).Error("failed to hash token")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
		return
	}

	createClusterRegistrationTokenRequest.EncryptedToken = base64.RawStdEncoding.EncodeToString(hashedToken)
	createClusterRegistrationTokenRequest.Token = token

	// else create new one
	result, err := s.controller.CreateRegistrationToken(r.Context(), createClusterRegistrationTokenRequest)
	if err != nil {
		log.WithError(err).Error("failed to create cluster registration token")
		errutil.WriteCloudError(w, errutil.NewCloudError(http.StatusInternalServerError, errutil.CloudErrorCodeInternalServerError, stringErrorFailure))
		return
	}

	httputil.Respond(w, result)
}
