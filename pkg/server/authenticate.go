package server

import (
	"net/http"

	"k8s.io/klog/v2"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
)

func (s *Service) authenticate(w http.ResponseWriter, r *http.Request) (bool, *tenancyv1alpha1.User, error) {
	authenticated, user, err := s.authenticator.Authenticate(r)
	if err != nil {
		klog.Error(err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return false, nil, err
	}

	if !authenticated {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return false, nil, nil
	}

	return true, user, nil
}
