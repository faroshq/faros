package service

import (
	"fmt"
	"io"
	"net/http"
	"path"

	"github.com/emicklei/go-restful/v3"
	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	farosclient "github.com/faroshq/faros/pkg/client/clientset/versioned"
	"github.com/faroshq/faros/pkg/service/authentications"
	"github.com/faroshq/faros/pkg/store"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/endpoints/handlers/negotiation"
	"k8s.io/apiserver/pkg/endpoints/handlers/responsewriters"
	"k8s.io/klog/v2"
)

type OrganizationResource struct {
	store           store.Store
	authentications authentications.Authenticator
	farosClient     farosclient.Interface
}

func (o OrganizationResource) RegisterTo(container *restful.Container) {
	ws := new(restful.WebService)
	filter := &filterAuthJWT{
		Authenticator: o.authentications,
	}
	ws.
		Path(pathOrganizations).
		Consumes("*/*").
		Produces("*/*").
		ApiVersion(apiVersion).
		Filter(filter.authJWT)

	//ws.Consumes(restful.MIME_JSON)
	ws.Produces(restful.MIME_JSON)

	ws.Route(ws.GET("/").To(o.listOrganizations).
		Doc("List organizations").Do(returns200OrganizationList, returns401, returns500))

	ws.Route(ws.POST("/").To(o.createOrganization).
		Doc("Create organization").
		Reads(tenancyv1alpha1.Organization{}).
		Do(returns200Organization, returns401, returns500))

	ws.Route(ws.GET(organizationArg).To(o.getOrganization).
		Doc("Get organization").
		Operation("getOrganization").
		Param(ws.PathParameter("organization", "Name of an organization")).
		Do(returns200Organization, returns401, returns500))

	ws.Route(ws.DELETE(organizationArg).To(o.deleteOrganization).
		Doc("Delete organization").
		Operation("deleteOrganization").
		Param(ws.PathParameter("organization", "Name of an organization")).
		Do(return200, returns401, returns500))

	// TODO: Split this into a separate resource
	// https://github.com/emicklei/go-restful/issues/320
	ws.Route(ws.GET(path.Join(organizationArg, pathWorkspaces)).To(o.listWorkspaces).
		Doc("List workspaces").
		Param(ws.PathParameter("organization", "Name of an organization")).
		Do(returns200WorkspaceList, returns401, returns500))

	ws.Route(ws.POST(path.Join(organizationArg, pathWorkspaces)).To(o.createWorkspace).
		Doc("Create workspace").
		Reads(tenancyv1alpha1.Workspace{}).
		Param(ws.PathParameter("organization", "Name of an organization")).
		Do(returns200Organization, returns401, returns500))

	ws.Route(ws.GET(path.Join(organizationArg, pathWorkspaces, workspaceArg)).To(o.getWorkspace).
		Doc("Get workspace").
		Param(ws.PathParameter("workspace", "Name of an workspace")).
		Param(ws.PathParameter("organization", "Name of an organization")).
		Do(returns200Workspace, returns401, returns500))

	ws.Route(ws.DELETE(path.Join(organizationArg, pathWorkspaces, workspaceArg)).To(o.deleteWorkspace).
		Doc("Delete workspace").
		Param(ws.PathParameter("workspace", "Name of an workspace")).
		Param(ws.PathParameter("organization", "Name of an organization")).
		Do(return200, returns401, returns500))

	container.Add(ws)
}

func (o OrganizationResource) listOrganizations(r *restful.Request, w *restful.Response) {
	ctx := r.Request.Context()

	user := r.Attribute(tenancyv1alpha1.UserKind).(*tenancyv1alpha1.User)

	organizations, err := o.store.ListOrganizations(ctx, tenancyv1alpha1.Organization{})
	if err != nil {
		klog.Error(err)
		http.Error(w.ResponseWriter, "Internal server error", http.StatusInternalServerError)
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

	responsewriters.WriteObjectNegotiated(codecs, negotiation.DefaultEndpointRestrictions, tenancyv1alpha1.SchemeGroupVersion, w.ResponseWriter, r.Request, http.StatusOK, results)
}

func (o OrganizationResource) createOrganization(r *restful.Request, w *restful.Response) {
	ctx := r.Request.Context()
	user := r.Attribute(tenancyv1alpha1.UserKind).(*tenancyv1alpha1.User)

	request := &tenancyv1alpha1.Organization{}
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

	current := &tenancyv1alpha1.Organization{
		ObjectMeta: metav1.ObjectMeta{
			Name: request.Name,
		},
	}

	_, err = o.store.GetOrganization(ctx, *current)
	if err == nil {
		responsewriters.ErrorNegotiated(fmt.Errorf("organization already exists"), codecs, schema.GroupVersion{}, w, r.Request)
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

	organizationCreated, err := o.store.CreateOrganization(ctx, *organization)
	if err != nil {
		klog.Error(err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	responsewriters.WriteObjectNegotiated(codecs, negotiation.DefaultEndpointRestrictions, tenancyv1alpha1.SchemeGroupVersion, w, r.Request, http.StatusOK, organizationCreated)
}

func (o OrganizationResource) getOrganization(r *restful.Request, w *restful.Response) {
	ctx := r.Request.Context()
	user := r.Attribute(tenancyv1alpha1.UserKind).(*tenancyv1alpha1.User)

	organizationName := r.PathParameter("organization")
	if organizationName == "" {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	organizationRef, err := o.store.GetOrganization(ctx, tenancyv1alpha1.Organization{
		ObjectMeta: metav1.ObjectMeta{
			Name: organizationName,
		},
	})
	if err != nil {
		klog.Error(err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	organization, err := o.farosClient.TenancyV1alpha1().Organizations().Get(ctx, organizationRef.Name, metav1.GetOptions{})
	if err != nil {
		klog.Error(err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if !organization.IsOwner(user) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	responsewriters.WriteObjectNegotiated(codecs, negotiation.DefaultEndpointRestrictions, tenancyv1alpha1.SchemeGroupVersion, w, r.Request, http.StatusOK, organization)
}

func (o OrganizationResource) deleteOrganization(r *restful.Request, w *restful.Response) {
	ctx := r.Request.Context()
	user := r.Attribute(tenancyv1alpha1.UserKind).(*tenancyv1alpha1.User)

	organizationName := r.PathParameter("organization")
	if organizationName == "" {
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

	if err := o.store.DeleteOrganization(ctx, *organization); err != nil {
		klog.Error(err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
