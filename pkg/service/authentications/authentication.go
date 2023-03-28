package authentications

import (
	"github.com/emicklei/go-restful/v3"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
)

type Authenticator interface {
	Authenticate(req *restful.Request) (authenticated bool, user *tenancyv1alpha1.User, err error)
	// OIDCLogin will redirect user to OIDC provider
	OIDCLogin(r *restful.Request, w *restful.Response)
	// OIDCCallback will handle OIDC callback
	OIDCCallback(r *restful.Request, w *restful.Response)
	// RegisterOrUpdate will register or update user in the system
	RegisterOrUpdate(req *restful.Request, w *restful.Response) (*tenancyv1alpha1.User, error)
}
