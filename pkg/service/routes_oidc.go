package service

import (
	"net/http"

	"github.com/emicklei/go-restful/v3"

	"github.com/faroshq/faros/pkg/service/authentications"
	"github.com/faroshq/faros/pkg/store"
)

type OIDCResource struct {
	store           store.Store
	authentications authentications.Authenticator
}

func (o OIDCResource) RegisterTo(container *restful.Container) {
	ws := new(restful.WebService)
	ws.
		Path(pathOIDC).
		Consumes("application/json").
		ApiVersion(apiVersion)

	ws.Consumes(restful.MIME_JSON)
	ws.Produces(restful.MIME_JSON)

	ws.Route(
		withAllSupportedStatuses(
			ws.GET(oidcLogin).To(o.login).
				Doc("Login into Faros").
				Operation("login")))

	ws.Route(
		returns200User(
			withAllSupportedStatuses(
				ws.POST(oidcRegister).To(o.register).
					AllowedMethodsWithoutContentType([]string{http.MethodPost}).
					Doc("Register into Faros").
					Operation("register"))))

	ws.Route(
		returns200LoginResult(
			withAllSupportedStatuses(
				ws.GET(oidcCallback).To(o.callback).
					Doc("Callback from OIDC provider for login flow").
					Operation("get-callback"))))

	ws.Route(
		withAllSupportedStatuses(
			ws.POST(oidcCallback).To(o.callback).
				Doc("Callback from OIDC provider for token refresh").
				Operation("post-callback")))

	container.Add(ws)
}

func (o OIDCResource) login(r *restful.Request, w *restful.Response) {
	o.authentications.OIDCLogin(r, w)
}

func (o OIDCResource) callback(r *restful.Request, w *restful.Response) {
	o.authentications.OIDCCallback(r, w)
}

func (o OIDCResource) register(r *restful.Request, w *restful.Response) {
	o.authentications.RegisterOrUpdate(r, w)
}
