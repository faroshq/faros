package service

import (
	"net/http"

	"github.com/emicklei/go-restful/v3"
	"k8s.io/klog/v2"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	"github.com/faroshq/faros/pkg/service/authentications"
)

type filterAuthJWT struct {
	Authenticator authentications.Authenticator
}

func (f *filterAuthJWT) authJWT(req *restful.Request, resp *restful.Response, chain *restful.FilterChain) {

	authenticated, user, err := f.Authenticator.Authenticate(req)
	if err != nil {
		klog.Error(err)
		resp.WriteErrorString(http.StatusInternalServerError, "Internal server error")
		return
	}

	if !authenticated {
		resp.WriteErrorString(http.StatusUnauthorized, "Unauthorized")
		return
	}
	req.SetAttribute(tenancyv1alpha1.UserKind, user)

	chain.ProcessFilter(req, resp)
}
