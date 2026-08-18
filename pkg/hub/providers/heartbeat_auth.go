/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package providers

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"

	authnv1 "k8s.io/api/authentication/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/faroshq/faros/pkg/apiurl"
)

// HeartbeatAuthenticator validates that a bearer token belongs to the named
// provider. Authentication alone is insufficient: otherwise any signed-in
// tenant could spoof another provider's liveness.
type HeartbeatAuthenticator func(ctx context.Context, name, token string) (bool, error)

// NewProviderHeartbeatAuthenticator verifies the token online in the provider
// workspace and binds it to that workspace's exact default/provider
// ServiceAccount UID. A same-named ServiceAccount from another workspace does
// not pass the UID check.
func NewProviderHeartbeatAuthenticator(kcpConfig *rest.Config, clusters ClusterResolver) (HeartbeatAuthenticator, error) {
	if kcpConfig == nil {
		return nil, fmt.Errorf("kcp config is required")
	}
	if clusters == nil {
		return nil, fmt.Errorf("cluster resolver is required")
	}
	return func(ctx context.Context, name, token string) (bool, error) {
		cluster, ok := clusters.CatalogEntryCluster(name)
		if !ok || cluster == "" {
			return false, nil
		}
		cfg := rest.CopyConfig(kcpConfig)
		cfg.Host = apiurl.KCPClusterURL(cfg.Host, cluster)
		client, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			return false, err
		}
		review, err := client.AuthenticationV1().TokenReviews().Create(ctx, &authnv1.TokenReview{
			Spec: authnv1.TokenReviewSpec{Token: token},
		}, metav1.CreateOptions{})
		if err != nil {
			return false, err
		}
		expectedUsername := "system:serviceaccount:" + ProviderSANamespace + ":" + ProviderSAName
		if !review.Status.Authenticated || review.Status.User.Username != expectedUsername || strings.TrimSpace(review.Status.User.UID) == "" {
			return false, nil
		}
		sa, err := client.CoreV1().ServiceAccounts(ProviderSANamespace).Get(ctx, ProviderSAName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return string(sa.UID) == review.Status.User.UID, nil
	}, nil
}

// WithHeartbeatStaticTokenFallback permits exact configured static tokens
// before provider-SA verification. The hub uses this only in explicit DevMode,
// where local Make/Tilt providers share the development login token.
func WithHeartbeatStaticTokenFallback(authenticate HeartbeatAuthenticator, tokens []string) HeartbeatAuthenticator {
	return func(ctx context.Context, name, token string) (bool, error) {
		for _, allowed := range tokens {
			if allowed != "" && subtle.ConstantTimeCompare([]byte(token), []byte(allowed)) == 1 {
				return true, nil
			}
		}
		if authenticate == nil {
			return false, nil
		}
		return authenticate(ctx, name, token)
	}
}

// RequireHeartbeatAuthentication fails closed unless the request carries a
// bearer credential that the configured authenticator accepts. Operational
// TokenReview failures are 503s so providers retry instead of treating a kcp
// outage as a permanently invalid credential.
func RequireHeartbeatAuthentication(authenticate HeartbeatAuthenticator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authenticate == nil {
			http.Error(w, "heartbeat authentication unavailable", http.StatusServiceUnavailable)
			return
		}
		token, ok := heartbeatBearer(r.Header.Get("Authorization"))
		if !ok {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		name, ok := parseHeartbeatPath(r.URL.Path)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		authenticated, err := authenticate(r.Context(), name, token)
		if err != nil {
			http.Error(w, "heartbeat authentication unavailable", http.StatusServiceUnavailable)
			return
		}
		if !authenticated {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func heartbeatBearer(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.ContainsAny(parts[1], "\r\n") {
		return "", false
	}
	return parts[1], true
}
