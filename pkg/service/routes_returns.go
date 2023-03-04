package service

import (
	"net/http"

	"github.com/emicklei/go-restful/v3"
	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	"github.com/faroshq/faros/pkg/models"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func returns200OrganizationList(b *restful.RouteBuilder) {
	b.Returns(http.StatusOK, "OK", tenancyv1alpha1.OrganizationList{})
}

func returns200Organization(b *restful.RouteBuilder) {
	b.Returns(http.StatusOK, "OK", tenancyv1alpha1.Organization{})
}

func returns200WorkspaceList(b *restful.RouteBuilder) {
	b.Returns(http.StatusOK, "OK", tenancyv1alpha1.WorkspaceList{})
}

func returns200Workspace(b *restful.RouteBuilder) {
	b.Returns(http.StatusOK, "OK", tenancyv1alpha1.Workspace{})
}

func returns200LoginResult(b *restful.RouteBuilder) {
	b.Returns(http.StatusOK, "OK", models.LoginResponse{})
}

func returns500(b *restful.RouteBuilder) {
	b.Returns(http.StatusInternalServerError, "Bummer, something went wrong", metav1.Status{})
}

func returns401(b *restful.RouteBuilder) {
	b.Returns(http.StatusUnauthorized, "Unauthorized", metav1.Status{})
}

func return200(b *restful.RouteBuilder) {
	b.Returns(http.StatusOK, "OK", metav1.Status{})
}

func return301(b *restful.RouteBuilder) {
	b.Returns(http.StatusMovedPermanently, "Moved Permanently", metav1.Status{})
}
