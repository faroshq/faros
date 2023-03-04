package service

import (
	"net/http"

	"github.com/emicklei/go-restful/v3"
	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	"github.com/faroshq/faros/pkg/models"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func returns200OrganizationList(b *restful.RouteBuilder) *restful.RouteBuilder {
	return b.Returns(http.StatusOK, "OK", tenancyv1alpha1.OrganizationList{})
}

func returns200Organization(b *restful.RouteBuilder) *restful.RouteBuilder {
	return b.Returns(http.StatusOK, "OK", tenancyv1alpha1.Organization{})
}

func returns200WorkspaceList(b *restful.RouteBuilder) *restful.RouteBuilder {
	return b.Returns(http.StatusOK, "OK", tenancyv1alpha1.WorkspaceList{})
}

func returns200Workspace(b *restful.RouteBuilder) *restful.RouteBuilder {
	return b.Returns(http.StatusOK, "OK", tenancyv1alpha1.Workspace{})
}

func returns200LoginResult(b *restful.RouteBuilder) *restful.RouteBuilder {
	return b.Returns(http.StatusOK, "OK", models.LoginResponse{})
}

// withAllSupportedStatuses is a shorthand to add all standard and auth results to the route but not validation.
func withAllSupportedStatuses(rb *restful.RouteBuilder) *restful.RouteBuilder {
	return returns500(returns502(returns401(returns200(returns301(rb)))))
}

func returns500(rb *restful.RouteBuilder) *restful.RouteBuilder {
	return rb.Returns(http.StatusInternalServerError, "Internal server error", metav1.Status{})
}

func returns502(rb *restful.RouteBuilder) *restful.RouteBuilder {
	return rb.Returns(http.StatusBadGateway, "Bad gateway", metav1.Status{})
}

func returns401(b *restful.RouteBuilder) *restful.RouteBuilder {
	return b.Returns(http.StatusUnauthorized, "Unauthorized", metav1.Status{})
}

func returns200(b *restful.RouteBuilder) *restful.RouteBuilder {
	return b.Returns(http.StatusOK, "OK", metav1.Status{})
}

func returns301(b *restful.RouteBuilder) *restful.RouteBuilder {
	return b.Returns(http.StatusMovedPermanently, "Moved Permanently", metav1.Status{})
}
