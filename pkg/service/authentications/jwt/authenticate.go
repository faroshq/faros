package jwt

import (
	"strings"

	"github.com/emicklei/go-restful/v3"
	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	"github.com/golang-jwt/jwt/request"
)

func (a *authenticator) Authenticate(req *restful.Request) (authenticated bool, user *tenancyv1alpha1.User, err error) {
	ctx := req.Request.Context()
	// Trying to authenticate via URL query (websocket for SSH/logs, SSE)
	if urlQueryToken := req.Request.URL.Query().Get("_t"); urlQueryToken != "" {
		claim, err := a.parseJWTToken(ctx, urlQueryToken)
		if err != nil {
			return false, nil, err
		}

		user, err = a.getUser(ctx, claim.Email)
		if err != nil {
			return false, nil, err
		}

		// authenticated
		return true, user, nil
	}

	if req.Request.Header.Get("Authorization") == "" {
		return false, nil, nil
	}

	// If it's basic auth (service account), it will have 'Basic' instead of
	// 'Bearer'
	if !strings.HasPrefix(req.Request.Header.Get("Authorization"), "Bearer") {
		return false, nil, nil
	}

	token, err := request.AuthorizationHeaderExtractor.ExtractToken(req.Request)
	if err != nil {
		return false, nil, err
	}

	claim, err := a.parseJWTToken(ctx, token)
	if err != nil {
		return false, nil, err
	}

	user, err = a.getUser(ctx, claim.Email)
	if err != nil {
		return false, nil, err
	}

	// authenticated
	return true, user, nil
}
