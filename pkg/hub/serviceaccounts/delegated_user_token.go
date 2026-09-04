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

// Delegated user tokens.
//
// The hub backend proxy used to forward a caller's own hub bearer — an OIDC
// id token or the static admin token — to every provider it fronted. For a
// platform provider that is a (known, accepted) confused deputy. For an
// org-owned provider it is a credential leak: the provider runs in a tenant's
// cluster, so any org member who registers one receives the full hub token of
// every user in the org who touches it, and that token reaches every
// workspace and every REST endpoint the user can.
//
// A delegated user token replaces the bearer on that path. It is a
// ServiceAccount token minted in the caller's tenant workspace, scoped by kcp
// to that one workspace (the hub's kcp proxy pins SA tokens to the cluster
// claim they carry), audience-bound, ten minutes long, and annotated with the
// human user it stands in for so the far end still knows who is calling.
//
// The account is deterministic per (tenant, user, provider), so repeated
// requests reuse one ServiceAccount rather than accumulating. Nothing deletes
// them yet: a member leaving the workspace keeps an idle faros-du-* account
// bound to a role they no longer hold, though any token minted for it is dead
// within ten minutes. Garbage collection on Membership removal is a follow-up.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	authnv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/faroshq/faros/pkg/kcppaths"
)

const (
	// LabelDelegatedUser marks a ServiceAccount minted to stand in for a human
	// user at an org-owned provider. Such accounts also carry
	// LabelWorkloadIdentity, so the online verifier treats them as hub-managed
	// and the user-facing ServiceAccount CRUD surface (LabelFarosSA) never
	// lists or rotates them.
	LabelDelegatedUser = "faros.sh/delegated-user"

	// AnnotationDelegatedUser is the User CR name the account stands in for.
	// The tenant resolver surfaces it as X-Faros-User so a provider at the far
	// end attributes the call to the person, not to the account.
	AnnotationDelegatedUser = "faros.sh/delegated-user"
	// AnnotationDelegatedOrg and AnnotationDelegatedWorkspace record the tenant
	// the account was minted in. AnnotationWorkloadIdentityTenantPath carries
	// the same fact as a path and is what the verifier enforces; these exist so
	// an operator reading the object sees the tuple without parsing a path.
	AnnotationDelegatedOrg       = "faros.sh/delegated-org"
	AnnotationDelegatedWorkspace = "faros.sh/delegated-workspace"
	// AnnotationDelegatedProvider names the org-owned provider the token was
	// issued for. It participates in the account name, so a token minted for
	// one provider is a different identity from one minted for another.
	AnnotationDelegatedProvider = "faros.sh/delegated-provider"

	// DelegatedUserClusterRole is what the delegated account is bound to. It is
	// the role every workspace member holds today: the bootstrap grants members
	// and admins alike cluster-admin in their team workspace
	// (pkg/hub/kcp/bootstrap.go ensureWorkspaceAdmin), so binding anything
	// narrower here would make the delegated call weaker than the user's own
	// kubectl. Narrowing is the workspace RBAC's job; when members get a
	// bounded ClusterRole this constant follows it.
	DelegatedUserClusterRole = "cluster-admin"

	// DelegatedUserTokenCacheTTL bounds how long an issued token is reused
	// before a fresh one is minted, independently of the token's own expiry.
	// Matches kcpResolverTTL in the tenant resolver so a revoked membership is
	// honoured on the same clock across both caches.
	DelegatedUserTokenCacheTTL = 5 * time.Minute

	// delegatedUserTokenExpirySlack keeps a cached token from being handed to
	// a provider moments before it expires mid-request.
	delegatedUserTokenExpirySlack = time.Minute

	delegatedUserNamePrefix   = "faros-du-"
	delegatedUserBindingInfix = "-"
)

// Identity is the caller a delegated token stands in for.
type Identity struct {
	// User is the User CR name — the value the backend proxy injects as
	// X-Faros-User.
	User string
}

// DelegatedUserServiceAccountName returns the stable, DNS-safe ServiceAccount
// name for one (tenant, user, provider) tuple. Hash-only for the same reason
// WorkloadServiceAccountName is: user names are email-shaped and provider
// names are tenant-chosen, and neither belongs in an RBAC object name.
func DelegatedUserServiceAccountName(tenantPath string, user Identity, providerName string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{tenantPath, user.User, providerName}, "\x00")))
	return delegatedUserNamePrefix + hex.EncodeToString(sum[:20])
}

// DelegatedUserBindingName returns the ClusterRoleBinding name paired with a
// delegated ServiceAccount.
func DelegatedUserBindingName(serviceAccountName string) string {
	return serviceAccountName + delegatedUserBindingInfix + DelegatedUserClusterRole
}

type delegatedTokenKey struct {
	orgUUID, wsUUID, user, provider string
}

type delegatedTokenEntry struct {
	token     string
	expiresAt time.Time
	// refreshAt is when the cache stops serving this entry: the earlier of
	// the cache TTL and the token's expiry minus slack.
	refreshAt time.Time
}

// IssueDelegatedUserToken returns a short-lived ServiceAccount token that
// represents user in the tenant workspace (orgUUID, wsUUID) towards the
// org-owned provider providerName. The account is created on first use, bound
// to DelegatedUserClusterRole, and reused thereafter; tokens are cached in
// memory per (tenant, user, provider) for DelegatedUserTokenCacheTTL and
// re-minted before they expire.
//
// The caller is responsible for having authenticated user and verified their
// membership in the workspace: this method mints on the hub's own authority
// and does not re-check either.
func (m *Manager) IssueDelegatedUserToken(ctx context.Context, orgUUID, wsUUID string, user Identity, providerName string) (string, time.Time, error) {
	if err := validateDelegatedInputs(orgUUID, wsUUID, user, providerName); err != nil {
		return "", time.Time{}, err
	}
	key := delegatedTokenKey{orgUUID: orgUUID, wsUUID: wsUUID, user: user.User, provider: providerName}
	now := m.clock()

	m.delegatedMu.Lock()
	if entry, ok := m.delegated[key]; ok && now.Before(entry.refreshAt) {
		m.delegatedMu.Unlock()
		return entry.token, entry.expiresAt, nil
	}
	m.delegatedMu.Unlock()

	cs, err := m.clientset(orgUUID, wsUUID)
	if err != nil {
		return "", time.Time{}, err
	}
	tenantPath := tenantPathFor(orgUUID, wsUUID)
	name := DelegatedUserServiceAccountName(tenantPath, user, providerName)
	if err := ensureDelegatedUserServiceAccount(ctx, cs, name, tenantPath, orgUUID, wsUUID, user, providerName); err != nil {
		return "", time.Time{}, err
	}
	if err := ensureWorkloadClusterRoleBinding(ctx, cs, DelegatedUserBindingName(name), DelegatedUserClusterRole, name); err != nil {
		return "", time.Time{}, err
	}

	expirationSeconds := int64(WorkloadIdentityTokenTTL / time.Second)
	issued, err := cs.CoreV1().ServiceAccounts(Namespace).CreateToken(ctx, name, &authnv1.TokenRequest{Spec: authnv1.TokenRequestSpec{
		Audiences:         []string{WorkloadIdentityTokenAudience},
		ExpirationSeconds: &expirationSeconds,
	}}, metav1.CreateOptions{})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("issuing delegated user token: %w", err)
	}
	if strings.TrimSpace(issued.Status.Token) == "" || issued.Status.ExpirationTimestamp.IsZero() {
		return "", time.Time{}, fmt.Errorf("delegated user token response was incomplete")
	}
	expiresAt := issued.Status.ExpirationTimestamp.Time
	// Same guard as the workload identity: the API server must not be able to
	// turn this into a longer-lived credential than policy allows.
	if expiresAt.After(now.Add(WorkloadIdentityTokenTTL + time.Second)) {
		return "", time.Time{}, fmt.Errorf("delegated user token exceeds maximum lifetime")
	}

	refreshAt := now.Add(DelegatedUserTokenCacheTTL)
	if byExpiry := expiresAt.Add(-delegatedUserTokenExpirySlack); byExpiry.Before(refreshAt) {
		refreshAt = byExpiry
	}
	m.delegatedMu.Lock()
	if m.delegated == nil {
		m.delegated = map[delegatedTokenKey]delegatedTokenEntry{}
	}
	m.delegated[key] = delegatedTokenEntry{token: issued.Status.Token, expiresAt: expiresAt, refreshAt: refreshAt}
	m.delegatedMu.Unlock()
	return issued.Status.Token, expiresAt, nil
}

// IsDelegatedUserServiceAccount reports whether sa was minted by
// IssueDelegatedUserToken.
func IsDelegatedUserServiceAccount(sa *corev1.ServiceAccount) bool {
	return sa != nil && sa.Labels[LabelDelegatedUser] == "true"
}

// DelegatedUserFromServiceAccount returns the human identity a verified
// delegated ServiceAccount stands in for. Callers must have run the object
// through VerifyWorkloadServiceAccountDetails first; this only reads.
func DelegatedUserFromServiceAccount(sa *corev1.ServiceAccount) (Identity, error) {
	if !IsDelegatedUserServiceAccount(sa) {
		return Identity{}, fmt.Errorf("ServiceAccount is not a delegated user identity")
	}
	if err := verifyDelegatedUserAnnotations(sa); err != nil {
		return Identity{}, err
	}
	return Identity{User: sa.Annotations[AnnotationDelegatedUser]}, nil
}

func verifyDelegatedUserAnnotations(sa *corev1.ServiceAccount) error {
	for _, key := range []string{
		AnnotationDelegatedUser,
		AnnotationDelegatedOrg,
		AnnotationDelegatedWorkspace,
		AnnotationDelegatedProvider,
	} {
		value := strings.TrimSpace(sa.Annotations[key])
		if value == "" || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("delegated ServiceAccount has incomplete identity annotations")
		}
	}
	// The path annotation is what the verifier enforced; the org/workspace
	// annotations must agree with it or the object has been tampered with.
	if tenantPathFor(sa.Annotations[AnnotationDelegatedOrg], sa.Annotations[AnnotationDelegatedWorkspace]) != sa.Annotations[AnnotationWorkloadIdentityTenantPath] {
		return fmt.Errorf("delegated ServiceAccount tenant annotations disagree")
	}
	return nil
}

func validateDelegatedInputs(orgUUID, wsUUID string, user Identity, providerName string) error {
	for name, value := range map[string]string{
		"orgUUID":      orgUUID,
		"wsUUID":       wsUUID,
		"user":         user.User,
		"providerName": providerName,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
		if strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("%s contains prohibited characters", name)
		}
	}
	// Org and workspace become path segments of the tenant path; a colon in
	// either would let one tuple spell another's path.
	if strings.ContainsAny(orgUUID+wsUUID, ":") {
		return fmt.Errorf("orgUUID and wsUUID must not contain ':'")
	}
	return nil
}

// tenantPathFor is the workspace path the tenant resolver composes from
// X-Faros-Org / X-Faros-Workspace, which the verifier compares against
// AnnotationWorkloadIdentityTenantPath.
func tenantPathFor(orgUUID, wsUUID string) string {
	return kcppaths.WorkspacePath(orgUUID, wsUUID)
}

func delegatedUserAnnotations(tenantPath, orgUUID, wsUUID string, user Identity, providerName string) map[string]string {
	return map[string]string{
		AnnotationWorkloadIdentityTenantPath: tenantPath,
		AnnotationDelegatedUser:              user.User,
		AnnotationDelegatedOrg:               orgUUID,
		AnnotationDelegatedWorkspace:         wsUUID,
		AnnotationDelegatedProvider:          providerName,
	}
}

func ensureDelegatedUserServiceAccount(ctx context.Context, cs kubernetes.Interface, name, tenantPath, orgUUID, wsUUID string, user Identity, providerName string) error {
	sas := cs.CoreV1().ServiceAccounts(Namespace)
	want := delegatedUserAnnotations(tenantPath, orgUUID, wsUUID, user, providerName)
	sa, err := sas.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = sas.Create(ctx, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: Namespace,
			Labels: map[string]string{
				LabelWorkloadIdentity: "true",
				LabelDelegatedUser:    "true",
			},
			Annotations: want,
		}}, metav1.CreateOptions{})
		if err == nil {
			return nil
		}
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("creating delegated ServiceAccount: %w", err)
		}
		sa, err = sas.Get(ctx, name, metav1.GetOptions{})
	}
	if err != nil {
		return fmt.Errorf("getting delegated ServiceAccount: %w", err)
	}
	// Every annotation participates in the name, so an existing account that
	// disagrees on any of them is not ours — refuse rather than adopt it. The
	// name is a hash of the tuple, so this only trips on a hand-made object.
	if !IsDelegatedUserServiceAccount(sa) || sa.Labels[LabelWorkloadIdentity] != "true" {
		return fmt.Errorf("ServiceAccount %q exists and is not a delegated user identity", name)
	}
	for key, value := range want {
		if sa.Annotations[key] != value {
			return fmt.Errorf("ServiceAccount %q is bound to a different delegated identity", name)
		}
	}
	return nil
}

// clock is the time source for the token cache; tests override it.
func (m *Manager) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

// delegatedCache is embedded in Manager so one Manager caches across requests.
type delegatedCache struct {
	delegatedMu sync.Mutex
	delegated   map[delegatedTokenKey]delegatedTokenEntry
	now         func() time.Time
}
