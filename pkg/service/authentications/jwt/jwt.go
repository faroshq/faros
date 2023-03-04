package jwt

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/coreos/go-oidc"
	"github.com/gorilla/sessions"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

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

// parseJWTToken validates token's validity and returns models.User that the token belongs to
func (a *authenticator) parseJWTToken(ctx context.Context, token string) (user *tenancyv1alpha1.User, err error) {
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

func (a *authenticator) getUser(ctx context.Context, email string) (*tenancyv1alpha1.User, error) {
	return a.store.GetUser(ctx, tenancyv1alpha1.User{
		Spec: tenancyv1alpha1.UserSpec{
			Email: email,
		},
	})
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
