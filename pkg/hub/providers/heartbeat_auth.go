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
	"net/http"
	"strings"

	authnv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// HeartbeatAuthenticator validates a bearer token against the same kcp
// authentication boundary that protects the rest of the provider plane.
type HeartbeatAuthenticator func(ctx context.Context, token string) (bool, error)

// NewTokenReviewHeartbeatAuthenticator authenticates heartbeat credentials
// online. This intentionally accepts every identity kcp authenticates: local
// development uses the hub's static token, while deployed providers can use
// OIDC or ServiceAccount credentials. Provider-specific credential binding is
// a separate protocol change because FAROS_HUB_TOKEN is currently independent
// from the provider kubeconfig.
func NewTokenReviewHeartbeatAuthenticator(kcpConfig *rest.Config) (HeartbeatAuthenticator, error) {
	client, err := kubernetes.NewForConfig(kcpConfig)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, token string) (bool, error) {
		review, err := client.AuthenticationV1().TokenReviews().Create(ctx, &authnv1.TokenReview{
			Spec: authnv1.TokenReviewSpec{Token: token},
		}, metav1.CreateOptions{})
		if err != nil {
			return false, err
		}
		return review.Status.Authenticated && strings.TrimSpace(review.Status.User.Username) != "", nil
	}, nil
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
		authenticated, err := authenticate(r.Context(), token)
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
