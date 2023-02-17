package server

import (
	"io"
	"net/http"

	"github.com/gorilla/mux"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/endpoints/handlers/negotiation"
	"k8s.io/apiserver/pkg/endpoints/handlers/responsewriters"
	"k8s.io/klog/v2"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
)

func (s *Service) getOrganization(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	authenticated, user, err := s.authenticate(w, r)
	if err != nil || !authenticated {
		return
	}

	vars := mux.Vars(r)
	organizationName := vars["organization"]
	if organizationName == "" {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	organizationRef, err := s.store.GetOrganization(ctx, tenancyv1alpha1.Organization{
		ObjectMeta: metav1.ObjectMeta{
			Name: organizationName,
		},
	})
	if err != nil {
		klog.Error(err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	organization, err := s.farosClient.TenancyV1alpha1().Organizations().Get(ctx, organizationRef.Name, metav1.GetOptions{})
	if err != nil {
		klog.Error(err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if !organization.IsOwner(user) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	responsewriters.WriteObjectNegotiated(codecs, negotiation.DefaultEndpointRestrictions, tenancyv1alpha1.SchemeGroupVersion, w, r, http.StatusOK, organization)
}

func (s *Service) listOrganizations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	authenticated, user, err := s.authenticate(w, r)
	if err != nil || !authenticated {
		return
	}

	organizations, err := s.store.ListOrganizations(ctx, tenancyv1alpha1.Organization{})
	if err != nil {
		klog.Error(err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	results := &tenancyv1alpha1.OrganizationList{
		Items: []tenancyv1alpha1.Organization{},
	}
	for _, organization := range organizations.Items {
		if organization.IsOwner(user) {
			results.Items = append(results.Items, organization)
		}
	}

	responsewriters.WriteObjectNegotiated(codecs, negotiation.DefaultEndpointRestrictions, tenancyv1alpha1.SchemeGroupVersion, w, r, http.StatusOK, results)
}

func (s *Service) createOrganization(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	authenticated, user, err := s.authenticate(w, r)
	if err != nil || !authenticated {
		return
	}

	request := &tenancyv1alpha1.Organization{}
	limitedReader := &io.LimitedReader{R: r.Body, N: limit}
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		responsewriters.ErrorNegotiated(err, codecs, schema.GroupVersion{}, w, r)
		return
	}
	if err := runtime.DecodeInto(codecs.UniversalDecoder(), body, request); err != nil {
		responsewriters.ErrorNegotiated(err, codecs, schema.GroupVersion{}, w, r)
		return
	}

	current := &tenancyv1alpha1.Organization{
		ObjectMeta: metav1.ObjectMeta{
			Name: request.Name,
		},
	}

	_, err = s.store.GetOrganization(ctx, *current)
	if err == nil {
		http.Error(w, "Organization already exists", http.StatusConflict)
		return
	}

	organization := &tenancyv1alpha1.Organization{
		ObjectMeta: metav1.ObjectMeta{
			Name: request.Name,
		},
		Spec: tenancyv1alpha1.OrganizationSpec{
			Description: request.Spec.Description,
			OwnersRef: []tenancyv1alpha1.ObjectReference{
				{
					Kind:       user.Kind,
					APIVersion: user.APIVersion,
					Name:       user.Name,
					Email:      user.Spec.Email,
				},
			},
		},
	}

	organizationCreated, err := s.store.CreateOrganization(ctx, *organization)
	if err != nil {
		klog.Error(err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	responsewriters.WriteObjectNegotiated(codecs, negotiation.DefaultEndpointRestrictions, tenancyv1alpha1.SchemeGroupVersion, w, r, http.StatusOK, organizationCreated)
}

func (s *Service) deleteOrganization(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	authenticated, user, err := s.authenticate(w, r)
	if err != nil || !authenticated {
		return
	}

	vars := mux.Vars(r)
	organizationName := vars["organization"]
	if organizationName == "" {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	organization, err := s.store.GetOrganization(ctx, tenancyv1alpha1.Organization{
		ObjectMeta: metav1.ObjectMeta{
			Name: organizationName,
		},
	})
	if err != nil {
		klog.Error(err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if !organization.IsOwner(user) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if err := s.store.DeleteOrganization(ctx, *organization); err != nil {
		klog.Error(err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
