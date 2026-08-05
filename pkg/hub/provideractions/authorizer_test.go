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

package provideractions

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"

	"github.com/faroshq/faros-kedge/pkg/hub/providers"
	"github.com/faroshq/faros-kedge/pkg/hub/serviceaccounts"
)

type authorizerConfigBuilder struct{ cfg *rest.Config }

func (b authorizerConfigBuilder) ChildWorkspaceConfig(_, _ string) *rest.Config { return b.cfg }

func TestKCPInvocationAuthorizerRequiresExactProjectActionGrant(t *testing.T) {
	const digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	const tenantPath = "root:kedge:tenants:org:ws"
	declared := providers.ProviderAction{
		ID: "a/v1", Name: "a", Version: "v1", SchemaDigest: digest,
		Resource: providers.ProviderActionResource{APIVersion: "g/v1", Kind: "Thing", Resource: "things"},
	}
	baseReq := invokeRequest{
		Provider: "db", Action: "a", ActionVersion: "v1", SchemaDigest: digest,
		ResourceRef: resourceRef{APIVersion: "g/v1", Kind: "Thing", Resource: "things", Name: "one"},
	}

	for _, tc := range []struct {
		name   string
		mutate func(*unstructured.Unstructured, *corev1.ServiceAccount, *invokeRequest, *string)
		want   bool
	}{
		{name: "allowed", want: true},
		{name: "revoked", mutate: func(project *unstructured.Unstructured, _ *corev1.ServiceAccount, _ *invokeRequest, _ *string) {
			binding := project.Object["spec"].(map[string]any)["environments"].([]any)[0].(map[string]any)["bindings"].([]any)[1].(map[string]any)
			action := binding["allowedActions"].([]any)[0].(map[string]any)
			action["revoked"] = true
		}, want: false},
		{name: "cross-resource", mutate: func(_ *unstructured.Unstructured, _ *corev1.ServiceAccount, req *invokeRequest, _ *string) {
			req.ResourceRef.Name = "other"
		}, want: false},
		{name: "cross-environment", mutate: func(_ *unstructured.Unstructured, sa *corev1.ServiceAccount, _ *invokeRequest, _ *string) {
			sa.Annotations[serviceaccounts.AnnotationWorkloadIdentityEnvironment] = "production"
		}, want: false},
		{name: "cross-project", mutate: func(_ *unstructured.Unstructured, sa *corev1.ServiceAccount, _ *invokeRequest, _ *string) {
			sa.Annotations[serviceaccounts.AnnotationWorkloadIdentityProject] = "other-project"
		}, want: false},
		{name: "project-uid-mismatch", mutate: func(_ *unstructured.Unstructured, sa *corev1.ServiceAccount, _ *invokeRequest, _ *string) {
			sa.Annotations[serviceaccounts.AnnotationWorkloadIdentityProjectUID] = "uid-other"
		}, want: false},
		{name: "schema-drift", mutate: func(_ *unstructured.Unstructured, _ *corev1.ServiceAccount, req *invokeRequest, _ *string) {
			req.SchemaDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		}, want: false},
		{name: "arbitrary-service-account", mutate: func(_ *unstructured.Unstructured, _ *corev1.ServiceAccount, _ *invokeRequest, user *string) {
			*user = "system:serviceaccount:default:arbitrary"
		}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project, req, sa, user := invocationAuthorizerFixture(t, tenantPath, baseReq, declared, digest)
			if tc.mutate != nil {
				tc.mutate(project, sa, &req, &user)
			}
			project.SetUID("project-uid")
			dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
				invocationProjectGVR: "ProjectList",
			}, project)
			a := &KCPInvocationAuthorizer{
				config:         authorizerConfigBuilder{cfg: &rest.Config{Host: "https://workspace.invalid"}},
				dynamicFactory: func(*rest.Config) (dynamic.Interface, error) { return dyn, nil },
				workloadVerifier: func(_ context.Context, _ *rest.Config, _ string, _ string) (string, *corev1.ServiceAccount, error) {
					return user, sa, nil
				},
			}
			r := httptestRequestWithBearer()
			err := a.Authorize(context.Background(), r, user, tenantPath, req, declared)
			if (err == nil) != tc.want {
				t.Fatalf("Authorize error = %v, want allowed=%t", err, tc.want)
			}
		})
	}
}

func invocationAuthorizerFixture(t *testing.T, tenantPath string, req invokeRequest, declared providers.ProviderAction, digest string) (*unstructured.Unstructured, invokeRequest, *corev1.ServiceAccount, string) {
	t.Helper()
	project := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "ai.kedge.faros.sh/v1alpha1",
		"kind":       "Project",
		"metadata":   map[string]any{"name": "project", "uid": "project-uid"},
		"spec": map[string]any{
			"environments": []any{map[string]any{
				"name": "development",
				"bindings": []any{
					map[string]any{
						"name": "dev", "provider": "infrastructure", "kind": "providerResource",
						"resourceRef": map[string]any{"apiVersion": "infra/v1", "kind": "Instance", "resource": "instances", "name": "project-dev"},
					},
					map[string]any{
						"name": "db", "provider": "db", "kind": "providerReference",
						"resourceRef":    map[string]any{"apiVersion": "g/v1", "kind": "Thing", "resource": "things", "name": "one"},
						"allowedActions": []any{map[string]any{"name": "a", "version": "v1", "schemaDigest": digest, "revoked": false}},
					},
				},
			}},
		},
	}}
	project.SetUID("project-uid")
	scope, err := resolveInvocationGrant(project, tenantPath, "project", "project-uid", "development", "project-dev", req, declared)
	if err != nil {
		t.Fatalf("build fixture scope: %v", err)
	}
	name := serviceaccounts.WorkloadServiceAccountName(scope)
	user := "system:serviceaccount:default:" + name
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
		Name: name, Namespace: serviceaccounts.Namespace,
		Labels: map[string]string{serviceaccounts.LabelWorkloadIdentity: "true"},
		Annotations: map[string]string{
			serviceaccounts.AnnotationWorkloadIdentityTenantPath:  tenantPath,
			serviceaccounts.AnnotationWorkloadIdentityProject:     "project",
			serviceaccounts.AnnotationWorkloadIdentityProjectUID:  "project-uid",
			serviceaccounts.AnnotationWorkloadIdentityEnvironment: "development",
			serviceaccounts.AnnotationWorkloadIdentityInstance:    "project-dev",
			serviceaccounts.AnnotationWorkloadIdentityScope:       serviceaccounts.WorkloadIdentityScopeMarker(scope),
		},
	}}
	return project, req, sa, user
}

func httptestRequestWithBearer() *http.Request {
	r := httptest.NewRequest(http.MethodPost, PathInvoke, strings.NewReader(`{}`))
	r.Header.Set("Authorization", "Bearer workload-token")
	return r
}
