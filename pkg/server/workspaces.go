package server

import (
	"io"
	"net/http"

	"github.com/gorilla/mux"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/endpoints/handlers/negotiation"
	"k8s.io/apiserver/pkg/endpoints/handlers/responsewriters"
	"k8s.io/klog/v2"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	"github.com/faroshq/faros/pkg/models"
)

func (s *Service) getWorkspace(w http.ResponseWriter, r *http.Request) {
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

	workspaceName := vars["workspace"]
	if workspaceName == "" {
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

	workspace, err := s.store.GetWorkspace(ctx, tenancyv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name: getWorkspaceName(*organization, tenancyv1alpha1.Workspace{
				ObjectMeta: metav1.ObjectMeta{
					Name: workspaceName,
				},
			}),
		},
	})
	if err != nil {
		klog.Error(err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	responsewriters.WriteObjectNegotiated(codecs, negotiation.DefaultEndpointRestrictions, tenancyv1alpha1.SchemeGroupVersion, w, r, http.StatusOK, workspace)
}

func (s *Service) createWorkspace(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	authenticated, user, err := s.authenticate(w, r)
	if err != nil || !authenticated {
		return
	}

	organizationName := mux.Vars(r)["organization"]
	if organizationName == "" {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// get organization and check if user is a member and can create workspaces
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

	request := &tenancyv1alpha1.Workspace{}
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

	current := &tenancyv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name: getWorkspaceName(*organization, *request),
		},
	}

	_, err = s.store.GetWorkspace(ctx, *current)
	if err == nil {
		http.Error(w, "Workspace already exists", http.StatusConflict)
		return
	}

	workspace := &tenancyv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name: getWorkspaceName(*organization, *request),
			Labels: map[string]string{
				models.LabelOrganization: organization.Name,
				models.LabelWorkspace:    request.Name,
			},
		},
		Spec: tenancyv1alpha1.WorkspaceSpec{
			Description: request.Spec.Description,
			OrganizationRef: corev1.ObjectReference{
				Name:       organization.Name,
				Kind:       organization.Kind,
				APIVersion: organization.APIVersion,
			},
		},
	}

	created, err := s.store.CreateWorkspace(ctx, *workspace)
	if err != nil {
		klog.Error(err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	created.Name = request.Name
	created.ManagedFields = nil

	responsewriters.WriteObjectNegotiated(codecs, negotiation.DefaultEndpointRestrictions, tenancyv1alpha1.SchemeGroupVersion, w, r, http.StatusOK, created)
}

func (s *Service) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	authenticated, user, err := s.authenticate(w, r)
	if err != nil || !authenticated {
		return
	}

	organizationName := mux.Vars(r)["organization"]
	if organizationName == "" {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// get organization and check if user is a member and can list memberships
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

	workspaces, err := s.store.ListWorkspaces(ctx, organizationName, tenancyv1alpha1.Workspace{})
	if err != nil {
		klog.Error(err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	for idx, workspace := range workspaces.Items {
		workspaces.Items[idx].Name = workspace.Labels[models.LabelWorkspace]
		workspaces.Items[idx].ManagedFields = nil
	}

	responsewriters.WriteObjectNegotiated(codecs, negotiation.DefaultEndpointRestrictions, tenancyv1alpha1.SchemeGroupVersion, w, r, http.StatusOK, workspaces)
}

func (s *Service) deleteWorkspace(w http.ResponseWriter, r *http.Request) {
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

	workspaceName := vars["workspace"]
	if workspaceName == "" {
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

	if err := s.store.DeleteWorkspace(ctx, tenancyv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name: getWorkspaceName(*organization, tenancyv1alpha1.Workspace{
				ObjectMeta: metav1.ObjectMeta{
					Name: workspaceName,
				},
			}),
		},
	}); err != nil {
		klog.Error(err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func getWorkspaceName(org tenancyv1alpha1.Organization, workspace tenancyv1alpha1.Workspace) string {
	return org.Name + "-" + workspace.Name
}
