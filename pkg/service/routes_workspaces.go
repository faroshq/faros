package service

import (
	"io"
	"net/http"

	"github.com/emicklei/go-restful/v3"
	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	"github.com/faroshq/faros/pkg/models"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/endpoints/handlers/negotiation"
	"k8s.io/apiserver/pkg/endpoints/handlers/responsewriters"
	"k8s.io/klog/v2"
)

func (o OrganizationResource) listWorkspaces(r *restful.Request, w *restful.Response) {
	ctx := r.Request.Context()

	user := r.Attribute(tenancyv1alpha1.UserKind).(*tenancyv1alpha1.User)

	organizationName := r.PathParameter("organization")
	if organizationName == "" {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// get organization and check if user is a member and can list memberships
	organization, err := o.store.GetOrganization(ctx, tenancyv1alpha1.Organization{
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

	workspaces, err := o.store.ListWorkspaces(ctx, organizationName, tenancyv1alpha1.Workspace{})
	if err != nil {
		klog.Error(err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	for idx, workspace := range workspaces.Items {
		workspaces.Items[idx].Name = workspace.Labels[models.LabelWorkspace]
		workspaces.Items[idx].ManagedFields = nil
	}

	responsewriters.WriteObjectNegotiated(codecs, negotiation.DefaultEndpointRestrictions, tenancyv1alpha1.SchemeGroupVersion, w, r.Request, http.StatusOK, workspaces)
}

func (o OrganizationResource) createWorkspace(r *restful.Request, w *restful.Response) {
	ctx := r.Request.Context()
	user := r.Attribute(tenancyv1alpha1.UserKind).(*tenancyv1alpha1.User)

	organizationName := r.PathParameter("organization")
	if organizationName == "" {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	// get organization and check if user is a member and can create workspaces
	organization, err := o.store.GetOrganization(ctx, tenancyv1alpha1.Organization{
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
	limitedReader := &io.LimitedReader{R: r.Request.Body, N: limit}
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		responsewriters.ErrorNegotiated(err, codecs, schema.GroupVersion{}, w, r.Request)
		return
	}
	if err := runtime.DecodeInto(codecs.UniversalDecoder(), body, request); err != nil {
		responsewriters.ErrorNegotiated(err, codecs, schema.GroupVersion{}, w, r.Request)
		return
	}

	current := &tenancyv1alpha1.Workspace{
		ObjectMeta: metav1.ObjectMeta{
			Name: getWorkspaceName(*organization, *request),
		},
	}

	_, err = o.store.GetWorkspace(ctx, *current)
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

	created, err := o.store.CreateWorkspace(ctx, *workspace)
	if err != nil {
		klog.Error(err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	created.Name = request.Name
	created.ManagedFields = nil

	responsewriters.WriteObjectNegotiated(codecs, negotiation.DefaultEndpointRestrictions, tenancyv1alpha1.SchemeGroupVersion, w, r.Request, http.StatusOK, created)
}

func getWorkspaceName(org tenancyv1alpha1.Organization, workspace tenancyv1alpha1.Workspace) string {
	return org.Name + "-" + workspace.Name
}

func (o OrganizationResource) getWorkspace(r *restful.Request, w *restful.Response) {
	ctx := r.Request.Context()
	user := r.Attribute(tenancyv1alpha1.UserKind).(*tenancyv1alpha1.User)

	organizationName := r.PathParameter("organization")
	if organizationName == "" {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	workspaceName := r.PathParameter("workspace")
	if workspaceName == "" {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	organization, err := o.store.GetOrganization(ctx, tenancyv1alpha1.Organization{
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

	workspace, err := o.store.GetWorkspace(ctx, tenancyv1alpha1.Workspace{
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

	responsewriters.WriteObjectNegotiated(codecs, negotiation.DefaultEndpointRestrictions, tenancyv1alpha1.SchemeGroupVersion, w, r.Request, http.StatusOK, workspace)
}

func (o OrganizationResource) deleteWorkspace(r *restful.Request, w *restful.Response) {
	ctx := r.Request.Context()
	user := r.Attribute(tenancyv1alpha1.UserKind).(*tenancyv1alpha1.User)

	organizationName := r.PathParameter("organization")
	if organizationName == "" {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	workspaceName := r.PathParameter("workspace")
	if workspaceName == "" {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	organization, err := o.store.GetOrganization(ctx, tenancyv1alpha1.Organization{
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

	if err := o.store.DeleteWorkspace(ctx, tenancyv1alpha1.Workspace{
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
