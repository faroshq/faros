package auth

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc"
	"github.com/golang-jwt/jwt/request"
	"github.com/gorilla/sessions"
	"golang.org/x/oauth2"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/models"
	"github.com/faroshq/faros/pkg/store"
	utiltls "github.com/faroshq/faros/pkg/util/tls"
)

// Authenticator authenticator is used to authenticate and handle all authentication related tasks
type Authenticator interface {
	// OIDCLogin will redirect user to OIDC provider
	OIDCLogin(w http.ResponseWriter, r *http.Request)
	// OIDCCallback will handle OIDC callback
	OIDCCallback(w http.ResponseWriter, r *http.Request)
	// Authenticate will authenticate the request if user already exists
	Authenticate(r *http.Request) (authenticated bool, user *tenancyv1alpha1.User, err error)
	// ParseJWTToken will parse the JWT token and return the user
	ParseJWTToken(ctx context.Context, token string) (user *tenancyv1alpha1.User, err error)
}

// Static check
var _ Authenticator = &AuthenticatorImpl{}

type AuthenticatorImpl struct {
	config config.Config

	oAuthSessions *sessions.CookieStore
	store         store.Store
	provider      *oidc.Provider
	verifier      *oidc.IDTokenVerifier
	redirectURL   string
	client        *http.Client
}

func NewAuthenticator(cfg config.Config, store store.Store, callbackURLPrefix string) (*AuthenticatorImpl, error) {
	var client *http.Client
	var err error
	ctx := context.Background()

	hostingCoreClient, err := kubernetes.NewForConfig(cfg.FarosKCPConfig.HostingClusterRestConfig)
	if err != nil {
		return nil, err
	}

	secret, err := hostingCoreClient.CoreV1().Secrets(cfg.APIConfig.OIDCCASecretNamespace).Get(ctx, cfg.APIConfig.OIDCCASecretName, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}

	if secret != nil {
		crt, ok := secret.Data["tls.crt"]
		if !ok {
			return nil, errors.New("oidc tls.crt not found in secret")
		}
		key, ok := secret.Data["tls.key"]
		if !ok {
			return nil, errors.New("oidc tls.key not found in secret")
		}
		client, err = httpClientForRootCAs(crt, key)
		if err != nil {
			return nil, err
		}
		ctx = oidc.ClientContext(ctx, client)
	}

	redirectURL := cfg.APIConfig.ControllerExternalURL + callbackURLPrefix

	provider, err := oidc.NewProvider(ctx, cfg.APIConfig.OIDCIssuerURL)
	if err != nil {
		return nil, err
	}
	// Create an ID token parser, but only trust ID tokens issued to "example-app"
	verifier := provider.Verifier(&oidc.Config{
		ClientID: cfg.APIConfig.OIDCClientID,
	})

	da := &AuthenticatorImpl{
		config:        cfg,
		store:         store,
		verifier:      verifier,
		provider:      provider,
		client:        client,
		redirectURL:   redirectURL,
		oAuthSessions: sessions.NewCookieStore([]byte(cfg.APIConfig.OIDCAuthSessionKey)),
	}
	return da, nil
}

func (a *AuthenticatorImpl) OIDCLogin(w http.ResponseWriter, r *http.Request) {
	localRedirect := r.URL.Query().Get("redirect_uri")

	var scopes []string

	b := make([]byte, 16)
	rand.Read(b)
	state := base64.URLEncoding.EncodeToString(b)

	// Getting the session, it's not an issue if we error here
	session, _ := a.oAuthSessions.Get(r, "sess")

	session.Values["state"] = state
	session.Values["redirect_uri"] = localRedirect
	err := a.oAuthSessions.Save(r, w, session)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed persist state: %q", r.Form), http.StatusBadRequest)
		return
	}

	authCodeURL := ""
	scopes = append(scopes, "openid", "profile", "email")
	if r.FormValue("offline_access") != "yes" {
		authCodeURL = a.oauth2Config(scopes).AuthCodeURL(state)
	} else {
		scopes = append(scopes, "offline_access")
		authCodeURL = a.oauth2Config(scopes).AuthCodeURL(state, oauth2.AccessTypeOffline)
	}

	http.Redirect(w, r, authCodeURL, http.StatusSeeOther)
}

func (a *AuthenticatorImpl) OIDCCallback(w http.ResponseWriter, r *http.Request) {
	var (
		token *oauth2.Token
	)

	ctx := oidc.ClientContext(r.Context(), a.client)

	var localRedirect string
	oauth2Config := a.oauth2Config(nil)
	var isRefresh bool
	switch r.Method {
	case http.MethodGet:
		// Authorization redirect callback from OAuth2 auth flow.
		if errMsg := r.FormValue("error"); errMsg != "" {
			http.Error(w, errMsg+": "+r.FormValue("error_description"), http.StatusBadRequest)
			return
		}
		code := r.FormValue("code")
		if code == "" {
			http.Error(w, fmt.Sprintf("no code in request: %q", r.Form), http.StatusBadRequest)
			return
		}

		session, err := a.oAuthSessions.Get(r, "sess")
		if err != nil {
			http.Error(w, fmt.Sprintf("no session present: %q", r.Form), http.StatusBadRequest)
			return
		}

		localRedirect = session.Values["redirect_uri"].(string)

		if state := r.FormValue("state"); state != session.Values["state"] {
			http.Error(w, fmt.Sprintf("expected state %q got %q", session.Values["state"], state), http.StatusBadRequest)
			return
		}
		token, err = oauth2Config.Exchange(ctx, code)
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to get token: %v", err), http.StatusInternalServerError)
			return
		}
	case http.MethodPost:
		isRefresh = true
		// Form request from frontend to refresh a token.
		refresh := r.FormValue("refresh_token")
		if refresh == "" {
			http.Error(w, fmt.Sprintf("no refresh_token in request: %q", r.Form), http.StatusBadRequest)
			return
		}
		t := &oauth2.Token{
			RefreshToken: refresh,
		}
		var err error
		token, err = oauth2Config.TokenSource(ctx, t).Token()
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to get token: %v", err), http.StatusInternalServerError)
			return
		}
	default:
		http.Error(w, fmt.Sprintf("method not implemented: %s", r.Method), http.StatusBadRequest)
		return
	}

	idToken, err := a.verifier.Verify(r.Context(), token.AccessToken)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to verify ID token: %v", err), http.StatusInternalServerError)
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
		http.Error(w, fmt.Sprintf("failed to parse claim: %v", err), http.StatusInternalServerError)
		return
	}

	_, err = a.registerOrUpdateUser(ctx, claims.Email)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to register user: %v", err), http.StatusInternalServerError)
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
		http.Error(w, fmt.Sprintf("failed to marshal response: %v", err), http.StatusInternalServerError)
		return
	}

	if isRefresh {
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
		return
	} else {
		base64.StdEncoding.EncodeToString(data)
		localRedirect = localRedirect + "?data=" + base64.StdEncoding.EncodeToString(data)
		http.Redirect(w, r, localRedirect, http.StatusSeeOther)
	}

}

func (a *AuthenticatorImpl) Authenticate(r *http.Request) (authenticated bool, user *tenancyv1alpha1.User, err error) {
	// Trying to authenticate via URL query (websocket for SSH/logs, SSE)
	if urlQueryToken := r.URL.Query().Get("_t"); urlQueryToken != "" {
		user, err = a.ParseJWTToken(r.Context(), urlQueryToken)
		if err != nil {
			return false, nil, err
		}

		// authenticated
		return true, user, nil
	}

	if r.Header.Get("Authorization") == "" {
		return false, nil, nil
	}

	// If it's basic auth (service account), it will have 'Basic' instead of
	// 'Bearer'
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer") {
		return false, nil, nil
	}

	token, err := request.AuthorizationHeaderExtractor.ExtractToken(r)
	if err != nil {
		return false, nil, err
	}

	user, err = a.ParseJWTToken(r.Context(), token)
	if err != nil {
		return false, nil, err
	}

	// authenticated
	return true, user, nil
}

// ParseJWTToken validates token's validity and returns models.User that the token belongs to
func (a *AuthenticatorImpl) ParseJWTToken(ctx context.Context, token string) (user *tenancyv1alpha1.User, err error) {
	idToken, err := a.verifier.Verify(ctx, token)
	if err != nil {
		return nil, err
	}

	// TODO: extend
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return nil, err
	}

	return a.getUser(ctx, claims.Email)
}

// return an HTTP client which trusts the provided root CAs.
func httpClientForRootCAs(crt, key []byte) (*http.Client, error) {
	c, k, err := utiltls.CertificatePairFromBytes(crt, key)
	if err != nil {
		return nil, err
	}

	pool := x509.NewCertPool()
	pool.AddCert(c)

	tlsConfig := &tls.Config{
		RootCAs: pool,
		Certificates: []tls.Certificate{
			{
				Certificate: [][]byte{
					crt,
				},
				PrivateKey: k,
			},
		},
		ServerName:         "faros",
		InsecureSkipVerify: true,
	}

	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
			Proxy:           http.ProxyFromEnvironment,
			Dial: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).Dial,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}, nil
}

func (a *AuthenticatorImpl) oauth2Config(scopes []string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     a.config.APIConfig.OIDCClientID,
		ClientSecret: a.config.APIConfig.OIDCClientSecret,
		Endpoint:     a.provider.Endpoint(),
		Scopes:       scopes,
		RedirectURL:  a.redirectURL,
	}
}

// registerOrUpdateUser will register or update user in the system when user is authenticated
func (a *AuthenticatorImpl) registerOrUpdateUser(ctx context.Context, email string) (*tenancyv1alpha1.User, error) {
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

func (a *AuthenticatorImpl) getUser(ctx context.Context, email string) (*tenancyv1alpha1.User, error) {
	user, err := a.store.GetUser(ctx, tenancyv1alpha1.User{
		Spec: tenancyv1alpha1.UserSpec{
			Email: email,
		},
	})
	if err != nil {
		return nil, err
	}

	return user, nil
}
