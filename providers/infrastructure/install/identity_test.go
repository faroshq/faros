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

package install

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestEnsureClusterRoleUsesOnlyRuntimePermissions(t *testing.T) {
	client := fake.NewSimpleClientset()
	if err := ensureClusterRole(context.Background(), client); err != nil {
		t.Fatalf("ensureClusterRole: %v", err)
	}

	role, err := client.RbacV1().ClusterRoles().Get(context.Background(), RuntimeRoleName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get ClusterRole: %v", err)
	}
	want := []rbacv1.PolicyRule{
		{
			APIGroups: []string{"infrastructure.faros.sh"},
			Resources: []string{"templates"},
			Verbs:     []string{"get", "list", "watch", "update", "delete"},
		},
		{
			APIGroups: []string{"infrastructure.faros.sh"},
			Resources: []string{"templates/status"},
			Verbs:     []string{"get", "patch", "update"},
		},
		{
			APIGroups: []string{"infrastructure.faros.sh"},
			Resources: []string{"instances"},
			Verbs:     []string{"get", "list", "watch", "patch", "update"},
		},
		{
			APIGroups: []string{"infrastructure.faros.sh"},
			Resources: []string{"instances/status"},
			Verbs:     []string{"get", "patch", "update"},
		},
		{
			APIGroups: []string{"apis.kcp.io"},
			Resources: []string{"apiexportendpointslices"},
			Verbs:     []string{"get", "list", "watch"},
		},
		{
			APIGroups:     []string{"apis.kcp.io"},
			Resources:     []string{"apiexports/content"},
			ResourceNames: []string{APIExportName},
			Verbs:         []string{"get", "list", "watch", "patch", "update"},
		},
		{
			NonResourceURLs: []string{"/api", "/api/*", "/apis", "/apis/*", "/version", "/openapi", "/openapi/*", "/healthz", "/livez", "/readyz"},
			Verbs:           []string{"get"},
		},
	}
	if !reflect.DeepEqual(role.Rules, want) {
		t.Fatalf("runtime rules = %#v, want %#v", role.Rules, want)
	}
}

func TestEnsureLegacySATokenValidatesBoundSecret(t *testing.T) {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      RuntimeServiceAccountName,
			Namespace: RuntimeServiceAccountNamespace,
			UID:       types.UID("runtime-sa-uid"),
		},
	}
	validAnnotations := map[string]string{
		corev1.ServiceAccountNameKey: sa.Name,
		corev1.ServiceAccountUIDKey:  string(sa.UID),
	}

	t.Run("accepts the intended ServiceAccount token", func(t *testing.T) {
		client := fake.NewSimpleClientset(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:        RuntimeTokenSecretName,
				Namespace:   sa.Namespace,
				Annotations: validAnnotations,
			},
			Type: corev1.SecretTypeServiceAccountToken,
			Data: map[string][]byte{corev1.ServiceAccountTokenKey: []byte("runtime-token")},
		})
		token, err := ensureLegacySAToken(context.Background(), client, sa, RuntimeTokenSecretName)
		if err != nil {
			t.Fatalf("ensureLegacySAToken: %v", err)
		}
		if token != "runtime-token" {
			t.Fatalf("token = %q, want runtime-token", token)
		}
	})

	tests := []struct {
		name        string
		secretType  corev1.SecretType
		annotations map[string]string
		wantError   string
	}{
		{
			name:        "rejects an opaque Secret at the fixed name",
			secretType:  corev1.SecretTypeOpaque,
			annotations: validAnnotations,
			wantError:   "has type",
		},
		{
			name:       "rejects a Secret for another ServiceAccount",
			secretType: corev1.SecretTypeServiceAccountToken,
			annotations: map[string]string{
				corev1.ServiceAccountNameKey: "another-sa",
				corev1.ServiceAccountUIDKey:  string(sa.UID),
			},
			wantError: "names ServiceAccount",
		},
		{
			name:       "rejects a Secret for an earlier ServiceAccount UID",
			secretType: corev1.SecretTypeServiceAccountToken,
			annotations: map[string]string{
				corev1.ServiceAccountNameKey: sa.Name,
				corev1.ServiceAccountUIDKey:  "stale-sa-uid",
			},
			wantError: "has ServiceAccount UID",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := fake.NewSimpleClientset(&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:        RuntimeTokenSecretName,
					Namespace:   sa.Namespace,
					Annotations: test.annotations,
				},
				Type: test.secretType,
				Data: map[string][]byte{corev1.ServiceAccountTokenKey: []byte("attacker-controlled-token")},
			})
			if _, err := ensureLegacySAToken(context.Background(), client, sa, RuntimeTokenSecretName); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("ensureLegacySAToken error = %v, want substring %q", err, test.wantError)
			}
		})
	}
}

func TestEnsureLegacySATokenFailsFastOnPermanentGetError(t *testing.T) {
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
		Name: RuntimeServiceAccountName, Namespace: RuntimeServiceAccountNamespace, UID: types.UID("runtime-sa-uid"),
	}}
	client := fake.NewSimpleClientset()
	client.PrependReactor("get", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(
			schema.GroupResource{Resource: "secrets"},
			RuntimeTokenSecretName,
			errors.New("access denied"),
		)
	})

	started := time.Now()
	_, err := ensureLegacySAToken(context.Background(), client, sa, RuntimeTokenSecretName)
	if err == nil || !apierrors.IsForbidden(errors.Unwrap(errors.Unwrap(err))) {
		t.Fatalf("ensureLegacySAToken error = %v, want Forbidden", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("permanent GET error took %s; want fail-fast", elapsed)
	}
}
