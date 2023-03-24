package service

import (
	"io"
	"net/http"

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

func (o OrganizationResource) listOrganizations(r *restful.Request, w *restful.Response) {
	ctx := r.Request.Context()

	user := r.Attribute(tenancyv1alpha1.UserKind).(*tenancyv1alpha1.User)

	organizations, err := o.store.ListOrganizations(ctx, tenancyv1alpha1.Organization{})
	if err != nil {
		klog.Error(err)
		responsewriters.ErrorNegotiated(errInternalServerError("failed to get organizations"), codecs, schema.GroupVersion{}, w, r.Request)
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
		klog.Error(err)
		responsewriters.ErrorNegotiated(errBadRequest("exceded request size limit"), codecs, schema.GroupVersion{}, w, r.Request)
		return
	}
	if err := runtime.DecodeInto(codecs.UniversalDecoder(), body, request); err != nil {
		klog.Error(err)
		responsewriters.ErrorNegotiated(errBadRequest("failed reading body"), codecs, schema.GroupVersion{}, w, r.Request)
		return
	}

	current := &tenancyv1alpha1.Organization{
		ObjectMeta: metav1.ObjectMeta{
			Name: request.Name,
		},
	}

	_, err = o.store.GetOrganization(ctx, *current)
	if err == nil {
		responsewriters.ErrorNegotiated(errConflict("organization already exists"), codecs, schema.GroupVersion{}, w, r.Request)
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
		responsewriters.ErrorNegotiated(errInternalServerError("failed to create organization"), codecs, schema.GroupVersion{}, w, r.Request)
		return
	}

	responsewriters.WriteObjectNegotiated(codecs, negotiation.DefaultEndpointRestrictions, tenancyv1alpha1.SchemeGroupVersion, w, r.Request, http.StatusOK, organizationCreated)
}

func (o OrganizationResource) getOrganization(r *restful.Request, w *restful.Response) {
	ctx := r.Request.Context()
	user := r.Attribute(tenancyv1alpha1.UserKind).(*tenancyv1alpha1.User)

	organizationName := r.PathParameter("organization")
	if organizationName == "" {
		responsewriters.ErrorNegotiated(errBadRequest(""), codecs, schema.GroupVersion{}, w, r.Request)
		return
	}

	organizationRef, err := o.store.GetOrganization(ctx, tenancyv1alpha1.Organization{
		ObjectMeta: metav1.ObjectMeta{
			Name: organizationName,
		},
	})
	if err != nil {
		klog.Error(err)
		responsewriters.ErrorNegotiated(errInternalServerError("failed to get organization"), codecs, schema.GroupVersion{}, w, r.Request)
		return
	}

	if !organizationRef.IsOwner(user) {
		responsewriters.ErrorNegotiated(errForbidden(""), codecs, schema.GroupVersion{}, w, r.Request)
		return
	}

	responsewriters.WriteObjectNegotiated(codecs, negotiation.DefaultEndpointRestrictions, tenancyv1alpha1.SchemeGroupVersion, w, r.Request, http.StatusOK, organizationRef)
}

func (o OrganizationResource) deleteOrganization(r *restful.Request, w *restful.Response) {
	ctx := r.Request.Context()
	user := r.Attribute(tenancyv1alpha1.UserKind).(*tenancyv1alpha1.User)

	organizationName := r.PathParameter("organization")
	if organizationName == "" {
		responsewriters.ErrorNegotiated(errBadRequest(""), codecs, schema.GroupVersion{}, w, r.Request)
		return
	}

	organization, err := o.store.GetOrganization(ctx, tenancyv1alpha1.Organization{
		ObjectMeta: metav1.ObjectMeta{
			Name: organizationName,
		},
	})
	if err != nil {
		klog.Error(err)
		responsewriters.ErrorNegotiated(errInternalServerError("failed to get organization"), codecs, schema.GroupVersion{}, w, r.Request)
		return
	}

	if !organization.IsOwner(user) {
		responsewriters.ErrorNegotiated(errForbidden(""), codecs, schema.GroupVersion{}, w, r.Request)
		return
	}

	if err := o.store.DeleteOrganization(ctx, *organization); err != nil {
		klog.Error(err)
		responsewriters.ErrorNegotiated(errInternalServerError("failed to delete organization"), codecs, schema.GroupVersion{}, w, r.Request)
		return
	}

	// TODO: Deletion is not marked in this object we return.  We should return it as deleting or status object.
	responsewriters.WriteObjectNegotiated(codecs, negotiation.DefaultEndpointRestrictions, tenancyv1alpha1.SchemeGroupVersion, w, r.Request, http.StatusOK, organization)
}
