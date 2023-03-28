package service

import (
	"path"

	"github.com/emicklei/go-restful/v3"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
)

func (o OrganizationResource) RegisterTo(container *restful.Container) {
	ws := new(restful.WebService)
	filter := &filterAuthJWT{
		Authenticator: o.authentications,
	}
	ws.
		Path(pathOrganizations).
		Consumes("application/json").
		Consumes("application/json").
		ApiVersion(apiVersion).
		Filter(filter.authJWT)

	ws.Consumes(restful.MIME_JSON)
	ws.Produces(restful.MIME_JSON)

	ws.Route(
		returns200OrganizationList(
			withAllSupportedStatuses(
				ws.GET("/").To(o.listOrganizations).
					Doc("List organizations"))))

	ws.Route(
		returns200Organization(
			withAllSupportedStatuses(
				ws.POST("/").To(o.createOrganization).
					Doc("Create organization").
					Reads(tenancyv1alpha1.Organization{}))))

	ws.Route(
		returns200Organization(
			withAllSupportedStatuses(
				ws.GET(organizationArg).To(o.getOrganization).
					Doc("Get organization").
					Operation("getOrganization").
					Param(ws.PathParameter("organization", "Name of an organization")))))

	ws.Route(
		withAllSupportedStatuses(
			ws.DELETE(organizationArg).To(o.deleteOrganization).
				Doc("Delete organization").
				Operation("deleteOrganization").
				Param(ws.PathParameter("organization", "Name of an organization"))))

	// TODO: Split this into a separate resource
	// https://github.com/emicklei/go-restful/issues/320
	ws.Route(
		returns200WorkspaceList(
			withAllSupportedStatuses(
				ws.GET(path.Join(organizationArg, pathWorkspaces)).To(o.listWorkspaces).
					Doc("List workspaces").
					Param(ws.PathParameter("organization", "Name of an organization")))))

	ws.Route(
		returns200Organization(
			withAllSupportedStatuses(
				ws.POST(path.Join(organizationArg, pathWorkspaces)).To(o.createWorkspace).
					Doc("Create workspace").
					Reads(tenancyv1alpha1.Workspace{}).
					Param(ws.PathParameter("organization", "Name of an organization")))))

	ws.Route(
		returns200Workspace(
			withAllSupportedStatuses(
				ws.GET(path.Join(organizationArg, pathWorkspaces, workspaceArg)).To(o.getWorkspace).
					Doc("Get workspace").
					Param(ws.PathParameter("workspace", "Name of an workspace")).
					Param(ws.PathParameter("organization", "Name of an organization")))))

	ws.Route(
		withAllSupportedStatuses(
			ws.DELETE(path.Join(organizationArg, pathWorkspaces, workspaceArg)).To(o.deleteWorkspace).
				Doc("Delete workspace").
				Param(ws.PathParameter("workspace", "Name of an workspace")).
				Param(ws.PathParameter("organization", "Name of an organization"))))

	container.Add(ws)
}
