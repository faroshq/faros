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

package serviceaccounts

import (
	"context"
	"strings"
	"testing"
	"time"

	authnv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clienttesting "k8s.io/client-go/testing"
)

const (
	delegatedTestOrg      = "org"
	delegatedTestWS       = "workspace"
	delegatedTestProvider = "infrastructure"
)

var delegatedTestUser = Identity{User: "alice"}

// delegatedTokenReactor answers TokenRequests with a token that names the
// mint count, so a test can tell a cached token from a fresh one, and returns
// the issued expiry it was configured with.
func delegatedTokenReactor(m *Manager, ttl time.Duration) (*int, clienttesting.ReactionFunc) {
	mints := 0
	return &mints, func(action clienttesting.Action) (bool, runtime.Object, error) {
		mints++
		return true, &authnv1.TokenRequest{Status: authnv1.TokenRequestStatus{
			Token:               "delegated-" + strings.Repeat("x", mints),
			ExpirationTimestamp: metav1.NewTime(m.clock().Add(ttl)),
		}}, nil
	}
}

func TestIssueDelegatedUserTokenCreatesOneAccountBoundToMemberRole(t *testing.T) {
	m, cs := managerFor(t)
	defer resetTestClientset()
	var gotAudience []string
	var gotTTL int64
	cs.PrependReactor("create", "serviceaccounts/token", func(action clienttesting.Action) (bool, runtime.Object, error) {
		request := action.(clienttesting.CreateAction).GetObject().(*authnv1.TokenRequest)
		gotAudience = append([]string(nil), request.Spec.Audiences...)
		gotTTL = *request.Spec.ExpirationSeconds
		return true, &authnv1.TokenRequest{Status: authnv1.TokenRequestStatus{
			Token:               "delegated-token",
			ExpirationTimestamp: metav1.NewTime(time.Now().Add(WorkloadIdentityTokenTTL)),
		}}, nil
	})

	ctx := context.Background()
	token, expiry, err := m.IssueDelegatedUserToken(ctx, delegatedTestOrg, delegatedTestWS, delegatedTestUser, delegatedTestProvider)
	if err != nil {
		t.Fatalf("IssueDelegatedUserToken: %v", err)
	}
	if token != "delegated-token" || expiry.IsZero() {
		t.Fatalf("token/expiry = %q/%v", token, expiry)
	}
	if len(gotAudience) != 1 || gotAudience[0] != WorkloadIdentityTokenAudience {
		t.Errorf("TokenRequest audiences = %v, want [%q]", gotAudience, WorkloadIdentityTokenAudience)
	}
	if gotTTL != int64(WorkloadIdentityTokenTTL/time.Second) {
		t.Errorf("TokenRequest TTL = %d, want %d", gotTTL, int64(WorkloadIdentityTokenTTL/time.Second))
	}

	tenantPath := tenantPathFor(delegatedTestOrg, delegatedTestWS)
	name := DelegatedUserServiceAccountName(tenantPath, delegatedTestUser, delegatedTestProvider)
	if !strings.HasPrefix(name, delegatedUserNamePrefix) || len(name) > 63 {
		t.Fatalf("service account name %q is not a short faros-du-* label", name)
	}
	sa, err := cs.CoreV1().ServiceAccounts(Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get delegated ServiceAccount: %v", err)
	}
	if sa.Labels[LabelDelegatedUser] != "true" || sa.Labels[LabelWorkloadIdentity] != "true" {
		t.Errorf("labels = %v, want delegated-user and workload-identity", sa.Labels)
	}
	if _, isUserManaged := sa.Labels[LabelFarosSA]; isUserManaged {
		t.Error("delegated ServiceAccount carries the user-managed SA label and would be listable/rotatable by tenants")
	}
	for key, want := range map[string]string{
		AnnotationWorkloadIdentityTenantPath: tenantPath,
		AnnotationDelegatedUser:              "alice",
		AnnotationDelegatedOrg:               delegatedTestOrg,
		AnnotationDelegatedWorkspace:         delegatedTestWS,
		AnnotationDelegatedProvider:          delegatedTestProvider,
	} {
		if got := sa.Annotations[key]; got != want {
			t.Errorf("annotation %q = %q, want %q", key, got, want)
		}
	}

	binding, err := cs.RbacV1().ClusterRoleBindings().Get(ctx, DelegatedUserBindingName(name), metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get delegated ClusterRoleBinding: %v", err)
	}
	if binding.RoleRef.Kind != "ClusterRole" || binding.RoleRef.Name != DelegatedUserClusterRole {
		t.Errorf("binding role = %+v, want ClusterRole %s (what workspace members hold today)", binding.RoleRef, DelegatedUserClusterRole)
	}
	if len(binding.Subjects) != 1 || binding.Subjects[0].Kind != "ServiceAccount" || binding.Subjects[0].Name != name || binding.Subjects[0].Namespace != Namespace {
		t.Errorf("binding subjects = %+v, want only the delegated ServiceAccount", binding.Subjects)
	}

	// Second issuance for the same tuple after the cache is dropped reuses
	// the account: nothing accumulates per request.
	m.delegated = nil
	if _, _, err := m.IssueDelegatedUserToken(ctx, delegatedTestOrg, delegatedTestWS, delegatedTestUser, delegatedTestProvider); err != nil {
		t.Fatalf("IssueDelegatedUserToken (repeat): %v", err)
	}
	list, err := cs.CoreV1().ServiceAccounts(Namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list ServiceAccounts: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("ServiceAccounts after two issuances = %d, want 1", len(list.Items))
	}
}

func TestIssueDelegatedUserTokenCachesAndRefreshesOnExpiry(t *testing.T) {
	m, cs := managerFor(t)
	defer resetTestClientset()
	now := time.Now()
	m.now = func() time.Time { return now }
	mints, reactor := delegatedTokenReactor(m, WorkloadIdentityTokenTTL)
	cs.PrependReactor("create", "serviceaccounts/token", reactor)
	ctx := context.Background()

	first, _, err := m.IssueDelegatedUserToken(ctx, delegatedTestOrg, delegatedTestWS, delegatedTestUser, delegatedTestProvider)
	if err != nil {
		t.Fatalf("first issue: %v", err)
	}
	second, _, err := m.IssueDelegatedUserToken(ctx, delegatedTestOrg, delegatedTestWS, delegatedTestUser, delegatedTestProvider)
	if err != nil {
		t.Fatalf("second issue: %v", err)
	}
	if first != second || *mints != 1 {
		t.Fatalf("back-to-back issuance minted %d tokens (%q, %q), want one cached token", *mints, first, second)
	}

	// A different user, workspace, or provider is a different identity and
	// must not share the cache entry.
	if _, _, err := m.IssueDelegatedUserToken(ctx, delegatedTestOrg, delegatedTestWS, Identity{User: "bob"}, delegatedTestProvider); err != nil {
		t.Fatalf("issue for bob: %v", err)
	}
	if _, _, err := m.IssueDelegatedUserToken(ctx, delegatedTestOrg, delegatedTestWS, delegatedTestUser, "vault"); err != nil {
		t.Fatalf("issue for vault: %v", err)
	}
	if *mints != 3 {
		t.Fatalf("mints after distinct tuples = %d, want 3", *mints)
	}

	// Past the cache TTL the token is re-minted even though it has not
	// expired yet.
	now = now.Add(DelegatedUserTokenCacheTTL + time.Second)
	third, _, err := m.IssueDelegatedUserToken(ctx, delegatedTestOrg, delegatedTestWS, delegatedTestUser, delegatedTestProvider)
	if err != nil {
		t.Fatalf("issue after cache TTL: %v", err)
	}
	if third == first || *mints != 4 {
		t.Fatalf("token after cache TTL = %q (mints %d), want a fresh one", third, *mints)
	}
}

func TestIssueDelegatedUserTokenRefreshesBeforeShortExpiry(t *testing.T) {
	m, cs := managerFor(t)
	defer resetTestClientset()
	now := time.Now()
	m.now = func() time.Time { return now }
	// The API server caps the token to 2 minutes, well under the cache TTL:
	// the cache must follow the token's expiry, not its own clock.
	mints, reactor := delegatedTokenReactor(m, 2*time.Minute)
	cs.PrependReactor("create", "serviceaccounts/token", reactor)
	ctx := context.Background()

	if _, _, err := m.IssueDelegatedUserToken(ctx, delegatedTestOrg, delegatedTestWS, delegatedTestUser, delegatedTestProvider); err != nil {
		t.Fatalf("first issue: %v", err)
	}
	now = now.Add(90 * time.Second)
	if _, _, err := m.IssueDelegatedUserToken(ctx, delegatedTestOrg, delegatedTestWS, delegatedTestUser, delegatedTestProvider); err != nil {
		t.Fatalf("issue near expiry: %v", err)
	}
	if *mints != 2 {
		t.Fatalf("mints = %d, want a refresh once the token is within %v of expiry", *mints, delegatedUserTokenExpirySlack)
	}
}

func TestIssueDelegatedUserTokenRejectsOverlongTokenAndBadInput(t *testing.T) {
	m, cs := managerFor(t)
	defer resetTestClientset()
	cs.PrependReactor("create", "serviceaccounts/token", func(action clienttesting.Action) (bool, runtime.Object, error) {
		return true, &authnv1.TokenRequest{Status: authnv1.TokenRequestStatus{
			Token:               "too-long",
			ExpirationTimestamp: metav1.NewTime(time.Now().Add(WorkloadIdentityTokenTTL + 2*time.Second)),
		}}, nil
	})
	ctx := context.Background()
	if _, _, err := m.IssueDelegatedUserToken(ctx, delegatedTestOrg, delegatedTestWS, delegatedTestUser, delegatedTestProvider); err == nil {
		t.Fatal("accepted a token longer than policy allows")
	}
	for name, call := range map[string]func() error{
		"empty user": func() error {
			_, _, err := m.IssueDelegatedUserToken(ctx, delegatedTestOrg, delegatedTestWS, Identity{}, delegatedTestProvider)
			return err
		},
		"empty workspace": func() error {
			_, _, err := m.IssueDelegatedUserToken(ctx, delegatedTestOrg, "", delegatedTestUser, delegatedTestProvider)
			return err
		},
		"colon in org": func() error {
			_, _, err := m.IssueDelegatedUserToken(ctx, "org:evil", delegatedTestWS, delegatedTestUser, delegatedTestProvider)
			return err
		},
		"empty provider": func() error {
			_, _, err := m.IssueDelegatedUserToken(ctx, delegatedTestOrg, delegatedTestWS, delegatedTestUser, "")
			return err
		},
	} {
		if err := call(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestIssueDelegatedUserTokenRefusesForeignAccountOfSameName(t *testing.T) {
	tenantPath := tenantPathFor(delegatedTestOrg, delegatedTestWS)
	name := DelegatedUserServiceAccountName(tenantPath, delegatedTestUser, delegatedTestProvider)
	// A hand-made account squatting on the deterministic name, without the
	// hub's labels: never adopt it, never mint for it.
	m, _ := managerFor(t, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: Namespace}})
	defer resetTestClientset()
	if _, _, err := m.IssueDelegatedUserToken(context.Background(), delegatedTestOrg, delegatedTestWS, delegatedTestUser, delegatedTestProvider); err == nil {
		t.Fatal("minted a token for a ServiceAccount the hub did not create")
	}
}

func TestVerifyDelegatedUserAnnotationsAcceptsHubMintedAndRejectsTampered(t *testing.T) {
	tenantPath := tenantPathFor(delegatedTestOrg, delegatedTestWS)
	good := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
		Name: "faros-du-x", Namespace: Namespace,
		Labels:      map[string]string{LabelWorkloadIdentity: "true", LabelDelegatedUser: "true"},
		Annotations: delegatedUserAnnotations(tenantPath, delegatedTestOrg, delegatedTestWS, delegatedTestUser, delegatedTestProvider),
	}}
	if err := verifyWorkloadServiceAccountAnnotations(good, tenantPath); err != nil {
		t.Fatalf("hub-minted delegated account rejected: %v", err)
	}
	identity, err := DelegatedUserFromServiceAccount(good)
	if err != nil || identity.User != "alice" {
		t.Fatalf("DelegatedUserFromServiceAccount = %+v, %v", identity, err)
	}

	otherTenant := good.DeepCopy()
	otherTenant.Annotations[AnnotationDelegatedWorkspace] = "elsewhere"
	if err := verifyWorkloadServiceAccountAnnotations(otherTenant, tenantPath); err == nil {
		t.Error("accepted a delegated account whose workspace annotation disagrees with its tenant path")
	}
	noUser := good.DeepCopy()
	delete(noUser.Annotations, AnnotationDelegatedUser)
	if err := verifyWorkloadServiceAccountAnnotations(noUser, tenantPath); err == nil {
		t.Error("accepted a delegated account with no user annotation")
	}
	if err := verifyWorkloadServiceAccountAnnotations(good, "root:faros:tenants:org:other"); err == nil {
		t.Error("accepted a delegated account for a tenant it was not minted in")
	}
	// A workload identity is still held to the full Project tuple.
	notDelegated := good.DeepCopy()
	delete(notDelegated.Labels, LabelDelegatedUser)
	if err := verifyWorkloadServiceAccountAnnotations(notDelegated, tenantPath); err == nil {
		t.Error("a non-delegated account with only delegated annotations passed the workload check")
	}
}
