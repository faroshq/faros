package service

import (
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
		Consumes("*/*").
		Produces("*/*").
		ApiVersion(apiVersion)

	ws.Consumes(restful.MIME_JSON)
	ws.Produces(restful.MIME_JSON)

	ws.Route(ws.GET(oidcLogin).To(o.login).
		Doc("Login into Faros").Do(return301, returns401, returns500))

	ws.Route(ws.GET(oidcCallback).To(o.callback).
		Doc("Callback from OIDC provider for login flow").
		Operation("get-callback").
		Do(returns200LoginResult, returns401, returns500))

	ws.Route(ws.POST(oidcCallback).To(o.callback).
		Doc("Callback from OIDC provider for token refresh").
		Operation("post-callback").
		Do(return301, returns401, returns500))

	container.Add(ws)
}

func (o OIDCResource) login(r *restful.Request, w *restful.Response) {
	o.authentications.OIDCLogin(r, w)
}

func (o OIDCResource) callback(r *restful.Request, w *restful.Response) {
	o.authentications.OIDCCallback(r, w)
}
