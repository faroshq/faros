package jwt

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc"
	"github.com/emicklei/go-restful/v3"
	"github.com/golang-jwt/jwt/request"
	"github.com/gorilla/sessions"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	"github.com/faroshq/faros/pkg/config"
	"github.com/faroshq/faros/pkg/service/authentications"
	"github.com/faroshq/faros/pkg/store"
	utiltls "github.com/faroshq/faros/pkg/util/tls"
)

var _ authentications.Authenticator = &authenticator{}

type authenticator struct {
	config *config.Config

	oAuthSessions *sessions.CookieStore
	store         store.Store
	provider      *oidc.Provider
	verifier      *oidc.IDTokenVerifier
	redirectURL   string
	client        *http.Client
}

func New(ctx context.Context, cfg *config.Config, store store.Store, callbackURLPrefix string) (*authenticator, error) {
	var client *http.Client

	hostingCoreClient, err := kubernetes.NewForConfig(cfg.FarosKCPConfig.HostingClusterRestConfig)
	if err != nil {
		return nil, err
	}

	secret, err := hostingCoreClient.CoreV1().Secrets(cfg.APIConfig.OIDCCASecretNamespace).Get(ctx, cfg.APIConfig.OIDCCASecretName, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}

	if secret != nil {
		klog.Infof("Using custom CA for OIDC issuer %s", cfg.APIConfig.OIDCIssuerURL)
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
	} else {
		klog.Infof("Using system CA for OIDC issuer %s", cfg.APIConfig.OIDCIssuerURL)
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

	return &authenticator{
		config:        cfg,
		store:         store,
		provider:      provider,
		verifier:      verifier,
		redirectURL:   redirectURL,
		client:        client,
		oAuthSessions: sessions.NewCookieStore([]byte(cfg.APIConfig.OIDCAuthSessionKey)),
	}, nil
}

type claim struct {
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
}

// parseJWTToken validates token's validity and minimal claim object
func (a *authenticator) parseJWTToken(ctx context.Context, token string) (*claim, error) {
	idToken, err := a.verifier.Verify(ctx, token)
	if err != nil {
		return nil, err
	}
	var c claim
	if err := idToken.Claims(&c); err != nil {
		return nil, err
	}

	return &c, nil
}

// getUser returns user from the store. Because store return error and empty user if user is not found,
// we are explicitly checking error type to return nil user if user is not found.
func (a *authenticator) getUser(ctx context.Context, email string) (*tenancyv1alpha1.User, error) {
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

// httpClientForRootCAs return an HTTP client which trusts the provided root CAs.
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

// RegisterOrUpdate registers or updates user in the store. This method is used by
// components to register user if they didn't used faros api to login.
func (a *authenticator) RegisterOrUpdate(req *restful.Request, w *restful.Response) (*tenancyv1alpha1.User, error) {
	ctx := req.Request.Context()
	if req.Request.Header.Get("Authorization") == "" {
		return nil, fmt.Errorf("invalid authorization header")
	}

	// If it's basic auth (service account), it will have 'Basic' instead of
	// 'Bearer'
	if !strings.HasPrefix(req.Request.Header.Get("Authorization"), "Bearer") {
		return nil, fmt.Errorf("invalid authorization header")
	}

	token, err := request.AuthorizationHeaderExtractor.ExtractToken(req.Request)
	if err != nil {
		klog.Errorf("failed to extract token: %v", err)
		return nil, fmt.Errorf("invalid authorization header")
	}

	claim, err := a.parseJWTToken(ctx, token)
	if err != nil {
		klog.Errorf("failed to parse token: %v", err)
		return nil, fmt.Errorf("invalid authorization header")
	}

	user, err := a.registerOrUpdateUser(ctx, claim.Email)
	if err != nil {
		klog.Error("failed to register or update user: %v", err)
		return nil, fmt.Errorf("failed to register or update user")
	}

	return user, nil
}
