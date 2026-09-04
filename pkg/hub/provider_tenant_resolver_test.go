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

package hub

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authnv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	"github.com/faroshq/faros/pkg/hub/serviceaccounts"
	kcpproxy "github.com/faroshq/faros/pkg/server/proxy"
)

type resolverConfigBuilder struct {
	cfg    *rest.Config
	gotOrg string
	gotWS  string
}

func (b *resolverConfigBuilder) ChildWorkspaceConfig(orgUUID, wsUUID string) *rest.Config {
	b.gotOrg, b.gotWS = orgUUID, wsUUID
	return b.cfg
}

type workloadResolverRoundTripper struct {
	token, serviceAccount, tenantPath string
	// delegatedUser, when set, makes the ServiceAccount a delegated user
	// identity standing in for that user instead of a workload identity.
	delegatedUser string
}

func (rt workloadResolverRoundTripper) serviceAccountObject() map[string]any {
	labels := map[string]string{serviceaccounts.LabelWorkloadIdentity: "true"}
	annotations := map[string]string{serviceaccounts.AnnotationWorkloadIdentityTenantPath: rt.tenantPath}
	if rt.delegatedUser != "" {
		labels[serviceaccounts.LabelDelegatedUser] = "true"
		rest := strings.TrimPrefix(rt.tenantPath, workspacePathRoot+":")
		org, ws, _ := strings.Cut(rest, ":")
		annotations[serviceaccounts.AnnotationDelegatedUser] = rt.delegatedUser
		annotations[serviceaccounts.AnnotationDelegatedOrg] = org
		annotations[serviceaccounts.AnnotationDelegatedWorkspace] = ws
		annotations[serviceaccounts.AnnotationDelegatedProvider] = "infrastructure"
	} else {
		annotations[serviceaccounts.AnnotationWorkloadIdentityScope] = strings.Repeat("0", 64)
		annotations[serviceaccounts.AnnotationWorkloadIdentityProject] = "project"
		annotations[serviceaccounts.AnnotationWorkloadIdentityProjectUID] = "project-uid"
		annotations[serviceaccounts.AnnotationWorkloadIdentityEnvironment] = "development"
		annotations[serviceaccounts.AnnotationWorkloadIdentityInstance] = "project-dev"
	}
	return map[string]any{
		"apiVersion": "v1", "kind": "ServiceAccount",
		"metadata": map[string]any{
			"name": rt.serviceAccount, "namespace": serviceaccounts.Namespace,
			"labels":      labels,
			"annotations": annotations,
		},
	}
}

func (rt workloadResolverRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	response := func(status int, value any) (*http.Response, error) {
		body, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(string(body))),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/apis/authentication.k8s.io/v1/tokenreviews":
		var review authnv1.TokenReview
		if err := json.NewDecoder(r.Body).Decode(&review); err != nil {
			return response(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		if review.Spec.Token != rt.token || len(review.Spec.Audiences) != 1 || review.Spec.Audiences[0] != serviceaccounts.WorkloadIdentityTokenAudience {
			return response(http.StatusForbidden, map[string]string{"error": "unexpected TokenReview"})
		}
		return response(http.StatusOK, &authnv1.TokenReview{
			TypeMeta: metav1.TypeMeta{APIVersion: "authentication.k8s.io/v1", Kind: "TokenReview"},
			Status: authnv1.TokenReviewStatus{
				Authenticated: true,
				User:          authnv1.UserInfo{Username: "system:serviceaccount:default:" + rt.serviceAccount},
				Audiences:     []string{serviceaccounts.WorkloadIdentityTokenAudience},
			},
		})
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/namespaces/default/serviceaccounts/"+rt.serviceAccount:
		return response(http.StatusOK, rt.serviceAccountObject())
	default:
		return response(http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func TestKCPTenantResolverRoutesServiceAccountIdentityThroughWorkloadVerification(t *testing.T) {
	const token = "runtime-token"
	const serviceAccount = "faros-wi-test"
	const tenantPath = "root:faros:tenants:org:workspace"
	builder := &resolverConfigBuilder{cfg: &rest.Config{
		Host:      "https://workload.test",
		Transport: workloadResolverRoundTripper{token: token, serviceAccount: serviceAccount, tenantPath: tenantPath},
	}}

	for _, tc := range []struct {
		name string
		user string
		err  error
	}{
		{name: "IdentifyUser returns SA-shaped identity", user: "system:serviceaccount:default:" + serviceAccount},
		{name: "IdentifyUser rejects workload token", err: kcpproxy.ErrIdentifyNoBearer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &kcpTenantResolver{
				workloadConfig: builder,
				identifyUser: func(*http.Request) (string, error) {
					return tc.user, tc.err
				},
			}
			req := httptest.NewRequest(http.MethodGet, "/services/providers/databricks/x", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set(headerFarosOrg, "org")
			req.Header.Set(headerFarosWorkspace, "workspace")

			user, gotPath, err := r.resolve(req)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if user != "system:serviceaccount:default:"+serviceAccount || gotPath != tenantPath {
				t.Fatalf("resolved identity = %q/%q", user, gotPath)
			}
			if builder.gotOrg != "org" || builder.gotWS != "workspace" {
				t.Fatalf("workspace config selected %q/%q", builder.gotOrg, builder.gotWS)
			}
		})
	}
}

// A delegated user token — what the backend proxy hands an org-owned provider
// in place of the caller's bearer — resolves to the HUMAN it stands in for,
// so a provider calling back into the hub with it gets X-Faros-User=alice, not
// the faros-du-* account name, and the same tenant binding every workload
// token is held to.
func TestKCPTenantResolverResolvesDelegatedUserTokenToTheHumanUser(t *testing.T) {
	const token = "delegated-token"
	const serviceAccount = "faros-du-test"
	const tenantPath = "root:faros:tenants:org:workspace"
	builder := &resolverConfigBuilder{cfg: &rest.Config{
		Host: "https://workload.test",
		Transport: workloadResolverRoundTripper{
			token: token, serviceAccount: serviceAccount, tenantPath: tenantPath, delegatedUser: "alice",
		},
	}}
	r := &kcpTenantResolver{
		workloadConfig: builder,
		identifyUser: func(*http.Request) (string, error) {
			return "system:serviceaccount:default:" + serviceAccount, nil
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/services/providers/infrastructure/x", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(headerFarosOrg, "org")
	req.Header.Set(headerFarosWorkspace, "workspace")
	user, gotPath, err := r.resolve(req)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if user != "alice" || gotPath != tenantPath {
		t.Fatalf("resolved identity = %q/%q, want alice/%s", user, gotPath, tenantPath)
	}
	if builder.gotOrg != "org" || builder.gotWS != "workspace" {
		t.Fatalf("workspace config selected %q/%q", builder.gotOrg, builder.gotWS)
	}

	// The token is bound to the workspace it was minted in; another
	// selection is refused exactly as it is for a workload token.
	other := httptest.NewRequest(http.MethodGet, "/services/providers/infrastructure/x", nil)
	other.Header.Set("Authorization", "Bearer "+token)
	other.Header.Set(headerFarosOrg, "org")
	other.Header.Set(headerFarosWorkspace, "other-workspace")
	if _, _, err := r.resolve(other); err == nil {
		t.Fatal("delegated token accepted for a workspace it was not minted in")
	}
}

func TestKCPTenantResolverRejectsWorkloadTokenForWrongTenantSelection(t *testing.T) {
	const token = "runtime-token"
	const serviceAccount = "faros-wi-test"
	r := &kcpTenantResolver{
		workloadConfig: &resolverConfigBuilder{cfg: &rest.Config{
			Host:      "https://workload.test",
			Transport: workloadResolverRoundTripper{token: token, serviceAccount: serviceAccount, tenantPath: "root:faros:tenants:org:workspace"},
		}},
		identifyUser: func(*http.Request) (string, error) {
			return "system:serviceaccount:default:" + serviceAccount, nil
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(headerFarosOrg, "other-org")
	req.Header.Set(headerFarosWorkspace, "workspace")
	if _, _, err := r.resolve(req); err == nil {
		t.Fatal("resolve accepted workload token with a different tenant selection")
	}
}

func TestKCPTenantResolverRejectsWrongTenantWhenIdentifyUserReturnsNoBearer(t *testing.T) {
	const token = "runtime-token"
	const serviceAccount = "faros-wi-test"
	r := &kcpTenantResolver{
		workloadConfig: &resolverConfigBuilder{cfg: &rest.Config{
			Host:      "https://workload.test",
			Transport: workloadResolverRoundTripper{token: token, serviceAccount: serviceAccount, tenantPath: "root:faros:tenants:org:workspace"},
		}},
		identifyUser: func(*http.Request) (string, error) {
			return "", kcpproxy.ErrIdentifyNoBearer
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(headerFarosOrg, "other-org")
	req.Header.Set(headerFarosWorkspace, "workspace")
	if _, _, err := r.resolve(req); err == nil || errors.Is(err, ErrAnonymousProviderCaller) {
		t.Fatalf("resolve error = %v, want fail-closed workload verification error", err)
	}
}

func TestKCPTenantResolverMapsOnlyMissingAuthorizationToAnonymous(t *testing.T) {
	r := &kcpTenantResolver{
		identifyUser: func(*http.Request) (string, error) {
			return "", kcpproxy.ErrIdentifyNoBearer
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if _, _, err := r.resolve(req); !errors.Is(err, ErrAnonymousProviderCaller) {
		t.Fatalf("resolve error = %v, want anonymous caller", err)
	}
}

func TestKCPTenantResolverRejectsUnavailableWorkloadIdentity(t *testing.T) {
	r := &kcpTenantResolver{identifyUser: func(*http.Request) (string, error) {
		return "system:serviceaccount:default:faros-wi-test", nil
	}}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer runtime-token")
	req.Header.Set(headerFarosOrg, "org")
	req.Header.Set(headerFarosWorkspace, "workspace")
	if _, _, err := r.resolve(req); err == nil || errors.Is(err, ErrAnonymousProviderCaller) {
		t.Fatalf("resolve error = %v, want fail-closed workload error", err)
	}
}
