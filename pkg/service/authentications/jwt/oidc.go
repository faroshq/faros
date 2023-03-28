package jwt

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/coreos/go-oidc"
	"github.com/emicklei/go-restful/v3"
	"golang.org/x/oauth2"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	"github.com/faroshq/faros/pkg/models"
)

func (a *authenticator) OIDCLogin(r *restful.Request, w *restful.Response) {
	localRedirect := r.QueryParameter("redirect_uri")

	var scopes []string

	b := make([]byte, 16)
	rand.Read(b)
	state := base64.URLEncoding.EncodeToString(b)

	// Getting the session, it's not an issue if we error here
	session, _ := a.oAuthSessions.Get(r.Request, "sess")

	session.Values["state"] = state
	session.Values["redirect_uri"] = localRedirect
	err := a.oAuthSessions.Save(r.Request, w.ResponseWriter, session)
	if err != nil {
		klog.Error(err)
		http.Error(w.ResponseWriter, "Internal server error", http.StatusInternalServerError)
		return
	}

	authCodeURL := ""
	scopes = append(scopes, "openid", "profile", "email")
	if r.Request.FormValue("offline_access") != "yes" {
		authCodeURL = a.oauth2Config(scopes).AuthCodeURL(state)
	} else {
		scopes = append(scopes, "offline_access")
		authCodeURL = a.oauth2Config(scopes).AuthCodeURL(state, oauth2.AccessTypeOffline)
	}

	http.Redirect(w.ResponseWriter, r.Request, authCodeURL, http.StatusSeeOther)
}

func (a *authenticator) OIDCCallback(r *restful.Request, w *restful.Response) {
	var (
		token *oauth2.Token
	)

	ctx := oidc.ClientContext(r.Request.Context(), a.client)

	var localRedirect string
	oauth2Config := a.oauth2Config(nil)
	var isRefresh bool
	switch r.Request.Method {
	case http.MethodGet:
		// Authorization redirect callback from OAuth2 auth flow.
		if errMsg := r.Request.FormValue("error"); errMsg != "" {
			http.Error(w.ResponseWriter, errMsg+": "+r.Request.FormValue("error_description"), http.StatusBadRequest)
			return
		}
		code := r.Request.FormValue("code")
		if code == "" {
			http.Error(w.ResponseWriter, fmt.Sprintf("no code in request: %q", r.Request.Form), http.StatusBadRequest)
			return
		}

		session, err := a.oAuthSessions.Get(r.Request, "sess")
		if err != nil {
			http.Error(w.ResponseWriter, fmt.Sprintf("no session present: %q", r.Request.Form), http.StatusBadRequest)
			return
		}

		localRedirect = session.Values["redirect_uri"].(string)

		if state := r.Request.FormValue("state"); state != session.Values["state"] {
			http.Error(w.ResponseWriter, fmt.Sprintf("expected state %q got %q", session.Values["state"], state), http.StatusBadRequest)
			return
		}
		token, err = oauth2Config.Exchange(ctx, code)
		if err != nil {
			http.Error(w.ResponseWriter, fmt.Sprintf("failed to get token: %v", err), http.StatusInternalServerError)
			return
		}
	case http.MethodPost:
		isRefresh = true
		// Form request from frontend to refresh a token.
		refresh := r.Request.FormValue("refresh_token")
		if refresh == "" {
			http.Error(w.ResponseWriter, fmt.Sprintf("no refresh_token in request: %q", r.Request.Form), http.StatusBadRequest)
			return
		}
		t := &oauth2.Token{
			RefreshToken: refresh,
		}
		var err error
		token, err = oauth2Config.TokenSource(ctx, t).Token()
		if err != nil {
			http.Error(w.ResponseWriter, fmt.Sprintf("failed to get token: %v", err), http.StatusInternalServerError)
			return
		}
	default:
		http.Error(w.ResponseWriter, fmt.Sprintf("method not implemented: %s", r.Request.Method), http.StatusBadRequest)
		return
	}

	idToken, err := a.verifier.Verify(ctx, token.AccessToken)
	if err != nil {
		http.Error(w.ResponseWriter, fmt.Sprintf("failed to verify ID token: %v", err), http.StatusInternalServerError)
		return
	}

	var claims struct {
		Email         string   `json:"email"`
		EmailVerified bool     `json:"email_verified"`
		Groups        []string `json:"groups"`
		Name          string   `json:"name"`
		IAT           int      `json:"iat"`
		EXP           int      `json:"exp"`
	}
	if err := idToken.Claims(&claims); err != nil {
		http.Error(w.ResponseWriter, fmt.Sprintf("failed to parse claim: %v", err), http.StatusInternalServerError)
		return
	}

	_, err = a.registerOrUpdateUser(ctx, claims.Email)
	if err != nil {
		http.Error(w.ResponseWriter, fmt.Sprintf("failed to register user: %v", err), http.StatusInternalServerError)
		return
	}

	response := models.LoginResponse{
		IDToken:       *idToken,
		AccessToken:   token.AccessToken,
		RefreshToken:  token.RefreshToken,
		Email:         claims.Email,
		ServerBaseURL: fmt.Sprintf("%s/clusters", a.config.APIConfig.ControllerExternalURL),
		ExpiresAt:     claims.EXP,
	}

	data, err := json.Marshal(response)
	if err != nil {
		http.Error(w.ResponseWriter, fmt.Sprintf("failed to marshal response: %v", err), http.StatusInternalServerError)
		return
	}

	if isRefresh {
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
		return
	} else {
		base64.StdEncoding.EncodeToString(data)
		localRedirect = localRedirect + "?data=" + base64.StdEncoding.EncodeToString(data)
		http.Redirect(w.ResponseWriter, r.Request, localRedirect, http.StatusSeeOther)
	}

}

func (a *authenticator) oauth2Config(scopes []string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     a.config.APIConfig.OIDCClientID,
		ClientSecret: a.config.APIConfig.OIDCClientSecret,
		Endpoint:     a.provider.Endpoint(),
		Scopes:       scopes,
		RedirectURL:  a.redirectURL,
	}
}

// registerOrUpdateUser will register or update user in the system when user is authenticated
func (a *authenticator) registerOrUpdateUser(ctx context.Context, email string) (*tenancyv1alpha1.User, error) {
	current, err := a.getUser(ctx, email)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}

	if current != nil {
		// no update of any kind for now
		return current, nil
	} else {
		// create the user
		return a.store.CreateUser(ctx, tenancyv1alpha1.User{
			ObjectMeta: metav1.ObjectMeta{},
			Spec: tenancyv1alpha1.UserSpec{
				Email: email,
			},
		})
	}
}
