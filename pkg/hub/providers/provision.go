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
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/faroshq/faros/pkg/apiurl"
	"github.com/faroshq/faros/pkg/kcppaths"
)

// Provisioner owns the kcp-side side-effects of provisioning a provider:
// creating the per-provider sub-workspace, the "provider" ServiceAccount, and
// the minted kubeconfig Secret. It is driven by the Provider CR reconciler
// (provider_controller.go); the provider's own APIExport/schemas come from its
// `init`.
type Provisioner struct {
	kcpConfig *rest.Config
}

// NewProvisioner returns a Provisioner that performs provider-workspace
// side-effects (workspace, ServiceAccount, minted kubeconfig) against kcp using
// the given admin config. Used by the admin onboarding API
// (pkg/hub/admin); the catalog controller no longer provisions.
func NewProvisioner(kcpConfig *rest.Config) *Provisioner {
	return &Provisioner{kcpConfig: kcpConfig}
}

// providersParentWorkspace is the parent of per-provider sub-workspaces
// (root:faros:providers:<name>). Standalone provider APIExports, schemas, and
// CatalogEntries live in those named children; platform exports and admin
// Provider objects remain in the corresponding system workspaces.
const providersParentWorkspace = kcppaths.ProvidersParent

var (
	workspaceGVR = schema.GroupVersionResource{
		Group: "tenancy.kcp.io", Version: "v1alpha1", Resource: "workspaces",
	}
	clusterRoleBindingGVR = schema.GroupVersionResource{
		Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings",
	}
	serviceAccountGVR = schema.GroupVersionResource{
		Group: "", Version: "v1", Resource: "serviceaccounts",
	}
	namespaceGVR = schema.GroupVersionResource{
		Group: "", Version: "v1", Resource: "namespaces",
	}
	logicalClusterGVR = schema.GroupVersionResource{
		Group: "core.kcp.io", Version: "v1alpha1", Resource: "logicalclusters",
	}
	apiExportV1Alpha2GVR = schema.GroupVersionResource{
		Group: "apis.kcp.io", Version: "v1alpha2", Resource: "apiexports",
	}
	apiResourceSchemaV1Alpha1GVR = schema.GroupVersionResource{
		Group: "apis.kcp.io", Version: "v1alpha1", Resource: "apiresourceschemas",
	}
)

// ProviderSAName is the standard ServiceAccount name created in every
// provider's sub-workspace. The provider pod is expected to mount a
// kubeconfig minted from this SA's token.
const ProviderSAName = "provider"

// ProviderSANamespace is the namespace ProviderSAName lives in. The
// Enable-time edge-proxy grant derives the SA's qualified identity from
// this tuple, so it must stay in lockstep with EnsureProviderSA.
const ProviderSANamespace = "default"

// ProviderTokenSecretSuffix is appended to the SA name to form the
// kubernetes.io/service-account-token Secret that holds the provider's
// long-lived bearer. kcp's token controller populates it; the token does
// not expire (valid until the Secret or its ServiceAccount is deleted), so
// the provider pod — and any downstream consumer such as the kro cluster —
// never needs a rotation loop.
const ProviderTokenSecretSuffix = "-token"

// EnsureProviderSA creates the "provider" ServiceAccount in the sub-workspace
// and grants it cluster-admin within that workspace. Idempotent. Returns the
// fully-qualified SA cluster-role-bound name "system:serviceaccount:default:provider".
func (p *Provisioner) EnsureProviderSA(ctx context.Context, providerName string) error {
	cl, err := p.clientFor(providersParentWorkspace + ":" + providerName)
	if err != nil {
		return err
	}
	// The `provider` WorkspaceType does NOT extend universal, so the `default`
	// namespace is not auto-created. Ensure it before placing the SA there.
	ns := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata":   map[string]any{"name": ProviderSANamespace},
	}}
	if _, err := cl.Resource(namespaceGVR).Create(ctx, ns, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("ensuring namespace %s in provider workspace: %w", ProviderSANamespace, err)
	}
	sa := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "ServiceAccount",
		"metadata":   map[string]any{"name": ProviderSAName, "namespace": ProviderSANamespace},
	}}
	if _, err := cl.Resource(serviceAccountGVR).Namespace(ProviderSANamespace).Create(ctx, sa, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating ServiceAccount %s/%s: %w", ProviderSANamespace, ProviderSAName, err)
	}

	// cluster-admin in the sub-workspace only. The provider pod reaches
	// other workspaces via the APIExport's virtual workspace + accepted
	// permission claims — NOT via this SA.
	crbName := "faros:providers:sa:" + ProviderSAName
	crb := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "rbac.authorization.k8s.io/v1",
		"kind":       "ClusterRoleBinding",
		"metadata":   map[string]any{"name": crbName},
		"roleRef": map[string]any{
			"apiGroup": "rbac.authorization.k8s.io",
			"kind":     "ClusterRole",
			"name":     "cluster-admin",
		},
		"subjects": []any{
			map[string]any{
				"kind":      "ServiceAccount",
				"name":      ProviderSAName,
				"namespace": "default",
			},
		},
	}}
	if err := applyUnstructured(ctx, cl, clusterRoleBindingGVR, crb); err != nil {
		return fmt.Errorf("applying %s: %w", crbName, err)
	}
	return nil
}

// MintProviderKubeconfig ensures a long-lived (legacy) token for the provider
// SA and returns a base64-encoded exec-credential-less kubeconfig the provider
// pod can mount. The token is read from a kubernetes.io/service-account-token
// Secret populated by kcp's token controller, so it does not expire and needs
// no rotation. The server URL is hubExternalURL + /clusters/{logical-cluster-id}
// so the provider's typed Kubernetes clients land in its own workspace by default.
// The ID (not the workspace path) is used so the kubeconfig works once requests
// reach a kcp shard, which only resolves /clusters/<id>.
func (p *Provisioner) MintProviderKubeconfig(ctx context.Context, providerName, hubExternalURL string) ([]byte, error) {
	cfg := rest.CopyConfig(p.kcpConfig)
	cfg.Host = apiurl.KCPClusterURL(cfg.Host, providersParentWorkspace+":"+providerName)
	typed, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("typed kube client for %s: %w", providerName, err)
	}

	token, err := ensureLegacySAToken(ctx, typed, "default", ProviderSAName, ProviderSAName+ProviderTokenSecretSuffix)
	if err != nil {
		return nil, fmt.Errorf("ensuring SA token for %s: %w", providerName, err)
	}

	// Resolve the provider workspace's logical cluster ID. The kubeconfig must
	// address kcp by ID (/clusters/<id>), not by workspace path: kcp shards only
	// resolve /clusters/<id>, and workspace-path resolution is front-proxy-only.
	// A provider kubeconfig pointed at the path 404s once the request reaches a
	// shard (the SA token also carries this ID in its clusterName claim).
	clusterID, err := resolveLogicalClusterID(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("resolving logical cluster ID for %s: %w", providerName, err)
	}

	server := hubExternalURL
	if server == "" {
		// Fall back to the kcp host we're talking to. Useful in tests
		// when no public hub URL is configured.
		server = cfg.Host
	} else {
		server = apiurl.KCPClusterURL(server, clusterID)
	}

	// Minimal kubeconfig; the provider pod uses controller-runtime which
	// is happy with token auth + insecure-skip-tls-verify in dev.
	kc := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: faros
  cluster:
    server: %s
    insecure-skip-tls-verify: true
contexts:
- name: faros
  context:
    cluster: faros
    user: faros
current-context: faros
users:
- name: faros
  user:
    token: %s
`, server, token)
	return []byte(kc), nil
}

// resolveLogicalClusterID returns the kcp logical cluster ID for the workspace
// addressed by cfg (cfg.Host must already point at the target workspace, by path
// or ID). It reads the well-known LogicalCluster object named "cluster" and
// returns its `kcp.io/cluster` annotation. The ID is required for kubeconfigs:
// kcp shards only resolve /clusters/<id>, while workspace-path resolution is
// front-proxy-only, so a path-based server URL 404s once a request reaches a
// shard.
func resolveLogicalClusterID(ctx context.Context, cfg *rest.Config) (string, error) {
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return "", fmt.Errorf("dynamic client: %w", err)
	}
	lc, err := dyn.Resource(logicalClusterGVR).Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("getting LogicalCluster: %w", err)
	}
	id := lc.GetAnnotations()["kcp.io/cluster"]
	if id == "" {
		return "", fmt.Errorf("LogicalCluster has no kcp.io/cluster annotation")
	}
	return id, nil
}

// ensureLegacySAToken creates (idempotently) a kubernetes.io/service-account-token
// Secret bound to saName and waits for kcp's token controller to populate its
// `token` field, then returns that token. Unlike a TokenRequest bearer this
// token does not expire — it stays valid until the Secret or its ServiceAccount
// is deleted — so callers need no rotation loop. Re-invoking reuses the existing
// Secret and returns the same token, keeping the value stable across reconciles.
func ensureLegacySAToken(ctx context.Context, cs kubernetes.Interface, namespace, saName, secretName string) (string, error) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
			Annotations: map[string]string{
				corev1.ServiceAccountNameKey: saName,
			},
		},
		Type: corev1.SecretTypeServiceAccountToken,
	}
	if _, err := cs.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return "", fmt.Errorf("creating service-account-token Secret %s/%s: %w", namespace, secretName, err)
	}

	var token string
	if err := wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, 30*time.Second, true, func(ctx context.Context) (bool, error) {
		got, err := cs.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		if t := got.Data[corev1.ServiceAccountTokenKey]; len(t) > 0 {
			token = string(t)
			return true, nil
		}
		return false, nil
	}); err != nil {
		return "", fmt.Errorf("waiting for token controller to populate Secret %s/%s: %w", namespace, secretName, err)
	}
	return token, nil
}

// EncodeKubeconfig is a tiny helper for status reporting — surface the
// minted kubeconfig as a base64-encoded blob so cluster operators can fish
// it out of the CatalogEntry status without needing to read a Secret
// (relevant when --provider-secret-write is disabled).
func EncodeKubeconfig(kc []byte) string {
	return base64.StdEncoding.EncodeToString(kc)
}

// EnsureProviderWorkspace creates root:faros:providers/{name} if it does not
// exist and waits for it to reach phase Ready. Idempotent. Returns the
// workspace's logical cluster ID (Workspace.spec.cluster) — the cluster name
// kcp embeds in the provider SA's token claims, which the Enable-time
// edges-proxy grant needs to build the qualified RBAC subject.
func (p *Provisioner) EnsureProviderWorkspace(ctx context.Context, name string) (string, error) {
	parent, err := p.clientFor(providersParentWorkspace)
	if err != nil {
		return "", err
	}
	// Use the restricted `provider` WorkspaceType (config/kcp/workspacetype-provider.yaml,
	// defined under root:faros): no universal → the provider cannot create
	// Workspaces; a defaultAPIBinding pulls in the CatalogEntry export.
	ws := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "tenancy.kcp.io/v1alpha1",
		"kind":       "Workspace",
		"metadata":   map[string]any{"name": name},
		"spec": map[string]any{
			"type": map[string]any{"name": "provider", "path": kcppaths.Root},
		},
	}}
	if _, err := parent.Resource(workspaceGVR).Create(ctx, ws, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return "", fmt.Errorf("creating sub-workspace %s: %w", name, err)
	}

	// Wait for Ready so subsequent schema/export writes target a live
	// workspace; spec.cluster is populated by then.
	var cluster string
	if err := wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, 60*time.Second, true, func(ctx context.Context) (bool, error) {
		got, err := parent.Resource(workspaceGVR).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		phase, _, _ := unstructured.NestedString(got.Object, "status", "phase")
		cluster, _, _ = unstructured.NestedString(got.Object, "spec", "cluster")
		return phase == "Ready", nil
	}); err != nil {
		return "", err
	}
	return cluster, nil
}

// ResolveWorkspaceCluster returns the logical cluster ID of the provider's
// sub-workspace (root:faros:providers/{name}), read-only. Returns "" (no error)
// when the workspace does not exist yet — i.e. the provider has not been
// onboarded. The catalog reconciler feeds this into the registry so the Enable
// endpoint can build the edges-proxy RBAC subject without the hub provisioning
// anything.
func (p *Provisioner) ResolveWorkspaceCluster(ctx context.Context, name string) (string, error) {
	parent, err := p.clientFor(providersParentWorkspace)
	if err != nil {
		return "", err
	}
	got, err := parent.Resource(workspaceGVR).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return "", nil
		}
		return "", err
	}
	cluster, _, _ := unstructured.NestedString(got.Object, "spec", "cluster")
	return cluster, nil
}

// ResolveCatalogEntryOwnerCluster returns the logical cluster that is allowed
// to own a provider's CatalogEntry. Builtins are hub-owned in the system
// providers workspace; standalone providers self-register in their onboarded
// provider workspace. The full workspace path is always derived by the hub.
func (p *Provisioner) ResolveCatalogEntryOwnerCluster(ctx context.Context, providerName string, builtin bool) (string, error) {
	parentPath, workspaceName := providersParentWorkspace, providerName
	if builtin {
		parentPath, workspaceName = kcppaths.System, "providers"
	}
	parent, err := p.clientFor(parentPath)
	if err != nil {
		return "", err
	}
	got, err := parent.Resource(workspaceGVR).Get(ctx, workspaceName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return "", nil
		}
		return "", err
	}
	cluster, _, _ := unstructured.NestedString(got.Object, "spec", "cluster")
	return cluster, nil
}

// CheckAPIExport verifies that a CatalogEntry's declared export exists in its
// provider workspace and is ready for tenant APIBindings. Provider init owns
// this object, so the catalog reconciler must observe it rather than treating
// the CatalogEntry declaration itself as proof of availability.
func (p *Provisioner) CheckAPIExport(ctx context.Context, exportPath, exportName string, requiredResources []APIExportResource, claims []PermissionClaim) error {
	_, err := p.VerifyAPIExport(ctx, exportPath, exportName, requiredResources, claims)
	return err
}

// VerifiedAPIExport is the mutation-safe portion of one APIExport snapshot.
// ClaimIdentityHashes comes from the same APIExport object whose identity,
// resources, schemas, and exact claim contract VerifyAPIExport validated.
// Enable callers must use these hashes rather than re-reading the mutable
// provider-owned export after validation.
type VerifiedAPIExport struct {
	// ClaimIdentityHashes is keyed by "group/resource" (and therefore uses a
	// leading slash for the core API group).
	ClaimIdentityHashes map[string]string
}

// VerifyAPIExport verifies the complete provider APIExport contract and
// returns the permission-claim identities from that same trusted snapshot.
// This is used both by the catalog reconciler and immediately at the tenant
// Enable mutation boundary.
func (p *Provisioner) VerifyAPIExport(ctx context.Context, exportPath, exportName string, requiredResources []APIExportResource, claims []PermissionClaim) (VerifiedAPIExport, error) {
	cl, err := p.clientFor(exportPath)
	if err != nil {
		return VerifiedAPIExport{}, fmt.Errorf("creating APIExport client for %s: %w", exportPath, err)
	}
	export, err := getAPIExport(ctx, cl, exportName)
	if err != nil {
		return VerifiedAPIExport{}, err
	}
	actualIdentities, err := apiExportPermissionClaimIdentityHashes(export)
	if err != nil {
		return VerifiedAPIExport{}, err
	}
	resolvedClaims := append([]PermissionClaim(nil), claims...)
	for i := range resolvedClaims {
		identityHash, found := actualIdentities[groupResourceKey(resolvedClaims[i].Group, resolvedClaims[i].Resource)]
		if !found {
			// Full contract validation below reports the missing claim. Avoid a
			// misleading identity-source error before then.
			continue
		}
		trustedIdentity, err := p.resolvePermissionClaimIdentity(ctx, resolvedClaims[i], identityHash)
		if err != nil {
			return VerifiedAPIExport{}, fmt.Errorf("resolving identity for permission claim %s: %w", groupResourceKey(resolvedClaims[i].Group, resolvedClaims[i].Resource), err)
		}
		resolvedClaims[i].ExpectedIdentityHash = trustedIdentity
	}
	return verifyAPIExportSnapshot(ctx, cl, export, requiredResources, resolvedClaims)
}

func (p *Provisioner) resolvePermissionClaimIdentity(ctx context.Context, claim PermissionClaim, actualIdentityHash string) (string, error) {
	hasIdentitySource := claim.IdentitySourceKind != "" || claim.IdentitySourceProvider != ""
	if actualIdentityHash == "" {
		if hasIdentitySource {
			return "", fmt.Errorf("identitySource is only valid for an identity-bearing APIExport permission claim")
		}
		return "", nil
	}
	if !hasIdentitySource {
		return "", fmt.Errorf("identity-bearing APIExport permission claim requires a Platform or Provider identitySource")
	}

	sourcePath, err := permissionClaimIdentitySourcePath(claim)
	if err != nil {
		return "", err
	}
	cl, err := p.clientFor(sourcePath)
	if err != nil {
		return "", fmt.Errorf("creating identity-source client for %s: %w", sourcePath, err)
	}
	exports, err := cl.Resource(apiExportV1Alpha2GVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("listing APIExports in identity source %s: %w", sourcePath, err)
	}
	return trustedResourceIdentity(ctx, cl, exports, sourcePath, claim.Group, claim.Resource)
}

func permissionClaimIdentitySourcePath(claim PermissionClaim) (string, error) {
	switch claim.IdentitySourceKind {
	case "Platform":
		if claim.IdentitySourceProvider != "" {
			return "", fmt.Errorf("platform identitySource must not name a provider")
		}
		return kcppaths.SystemControllers, nil
	case "Provider":
		if errs := validation.IsDNS1123Subdomain(claim.IdentitySourceProvider); len(errs) > 0 {
			return "", fmt.Errorf("invalid provider identitySource %q: %s", claim.IdentitySourceProvider, strings.Join(errs, ", "))
		}
		return kcppaths.ProviderPath(claim.IdentitySourceProvider), nil
	default:
		return "", fmt.Errorf("identity-bearing permission claim requires a Platform or Provider identitySource")
	}
}

type trustedResourceMatch struct {
	export   *unstructured.Unstructured
	resource apiExportResourceReference
}

func trustedResourceIdentity(ctx context.Context, cl dynamic.Interface, exports *unstructured.UnstructuredList, sourcePath, claimGroup, resourceName string) (string, error) {
	matches := make([]trustedResourceMatch, 0, 1)
	for i := range exports.Items {
		resources, _, err := unstructured.NestedSlice(exports.Items[i].Object, "spec", "resources")
		if err != nil {
			return "", fmt.Errorf("APIExport %q in identity source %s has malformed spec.resources: %w", exports.Items[i].GetName(), sourcePath, err)
		}
		for _, raw := range resources {
			resource, ok := raw.(map[string]any)
			if !ok {
				return "", fmt.Errorf("APIExport %q in identity source %s has malformed spec.resources entry", exports.Items[i].GetName(), sourcePath)
			}
			exportGroup, _, err := unstructured.NestedString(resource, "group")
			if err != nil {
				return "", fmt.Errorf("APIExport %q in identity source %s has malformed resource group: %w", exports.Items[i].GetName(), sourcePath, err)
			}
			name, _, err := unstructured.NestedString(resource, "name")
			if err != nil || name == "" {
				return "", fmt.Errorf("APIExport %q in identity source %s has malformed resource name", exports.Items[i].GetName(), sourcePath)
			}
			if exportGroup == claimGroup && name == resourceName {
				schemaName, _, err := unstructured.NestedString(resource, "schema")
				if err != nil || schemaName == "" {
					return "", fmt.Errorf("APIExport %q trusted resource %s has no valid schema reference", exports.Items[i].GetName(), groupResourceKey(claimGroup, resourceName))
				}
				matches = append(matches, trustedResourceMatch{
					export:   &exports.Items[i],
					resource: apiExportResourceReference{Group: exportGroup, Name: name, Schema: schemaName},
				})
			}
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no APIExport in trusted source %s publishes %s", sourcePath, groupResourceKey(claimGroup, resourceName))
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("multiple APIExports in trusted source %s publish %s", sourcePath, groupResourceKey(claimGroup, resourceName))
	}
	match := matches[0]
	schemaObject, err := cl.Resource(apiResourceSchemaV1Alpha1GVR).Get(ctx, match.resource.Schema, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return "", fmt.Errorf("source APIExport %q trusted resource %s references missing APIResourceSchema %q", match.export.GetName(), groupResourceKey(claimGroup, resourceName), match.resource.Schema)
		}
		return "", fmt.Errorf("getting APIResourceSchema %q for source APIExport %q: %w", match.resource.Schema, match.export.GetName(), err)
	}
	if err := validateAPIResourceSchemaReference(match.resource, schemaObject); err != nil {
		return "", fmt.Errorf("source APIExport %q trusted resource contract is invalid: %w", match.export.GetName(), err)
	}

	identityHash, _, err := unstructured.NestedString(match.export.Object, "status", "identityHash")
	if err != nil {
		return "", fmt.Errorf("source APIExport %q has malformed status.identityHash: %w", match.export.GetName(), err)
	}
	if identityHash == "" {
		return "", fmt.Errorf("source APIExport %q is waiting for status.identityHash", match.export.GetName())
	}
	conditions, _, err := unstructured.NestedSlice(match.export.Object, "status", "conditions")
	if err != nil {
		return "", fmt.Errorf("source APIExport %q has malformed status.conditions: %w", match.export.GetName(), err)
	}
	identityValid := false
	for _, raw := range conditions {
		condition, ok := raw.(map[string]any)
		if ok && condition["type"] == "IdentityValid" && condition["status"] == "True" {
			identityValid = true
			break
		}
	}
	if !identityValid {
		return "", fmt.Errorf("source APIExport %q is waiting for IdentityValid=True", match.export.GetName())
	}
	return identityHash, nil
}

func checkAPIExport(ctx context.Context, cl dynamic.Interface, exportName string, requiredResources []APIExportResource, claims []PermissionClaim) error {
	_, err := verifyAPIExport(ctx, cl, exportName, requiredResources, claims)
	return err
}

func verifyAPIExport(ctx context.Context, cl dynamic.Interface, exportName string, requiredResources []APIExportResource, claims []PermissionClaim) (VerifiedAPIExport, error) {
	export, err := getAPIExport(ctx, cl, exportName)
	if err != nil {
		return VerifiedAPIExport{}, err
	}
	return verifyAPIExportSnapshot(ctx, cl, export, requiredResources, claims)
}

func getAPIExport(ctx context.Context, cl dynamic.Interface, exportName string) (*unstructured.Unstructured, error) {
	export, err := cl.Resource(apiExportV1Alpha2GVR).Get(ctx, exportName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("APIExport %q does not exist", exportName)
		}
		return nil, fmt.Errorf("getting APIExport %q: %w", exportName, err)
	}
	return export, nil
}

func verifyAPIExportSnapshot(ctx context.Context, cl dynamic.Interface, export *unstructured.Unstructured, requiredResources []APIExportResource, claims []PermissionClaim) (VerifiedAPIExport, error) {
	exportName := export.GetName()
	resources, err := validateAPIExportReady(export, requiredResources, claims)
	if err != nil {
		return VerifiedAPIExport{}, err
	}
	for _, resource := range resources {
		schemaObject, err := cl.Resource(apiResourceSchemaV1Alpha1GVR).Get(ctx, resource.Schema, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return VerifiedAPIExport{}, fmt.Errorf("APIExport %q resource %s/%s references missing APIResourceSchema %q", exportName, resource.Group, resource.Name, resource.Schema)
			}
			return VerifiedAPIExport{}, fmt.Errorf("getting APIResourceSchema %q for APIExport %q: %w", resource.Schema, exportName, err)
		}
		if err := validateAPIResourceSchemaReference(resource, schemaObject); err != nil {
			return VerifiedAPIExport{}, fmt.Errorf(
				"APIExport %q resource %s/%s has an invalid schema contract: %w",
				exportName, resource.Group, resource.Name, err,
			)
		}
	}
	identities, err := apiExportPermissionClaimIdentityHashes(export)
	if err != nil {
		return VerifiedAPIExport{}, err
	}
	return VerifiedAPIExport{ClaimIdentityHashes: identities}, nil
}

func validateAPIResourceSchemaReference(resource apiExportResourceReference, schemaObject *unstructured.Unstructured) error {
	schemaGroup, _, err := unstructured.NestedString(schemaObject.Object, "spec", "group")
	if err != nil {
		return fmt.Errorf("APIResourceSchema %q has malformed spec.group: %w", resource.Schema, err)
	}
	schemaResource, _, err := unstructured.NestedString(schemaObject.Object, "spec", "names", "plural")
	if err != nil {
		return fmt.Errorf("APIResourceSchema %q has malformed spec.names.plural: %w", resource.Schema, err)
	}
	if schemaGroup != resource.Group || schemaResource != resource.Name {
		return fmt.Errorf("APIResourceSchema %q is for %s/%s, want %s/%s", resource.Schema, schemaGroup, schemaResource, resource.Group, resource.Name)
	}
	return nil
}

func apiExportPermissionClaimIdentityHashes(export *unstructured.Unstructured) (map[string]string, error) {
	permissionClaims, _, err := unstructured.NestedSlice(export.Object, "spec", "permissionClaims")
	if err != nil {
		return nil, fmt.Errorf("APIExport %q has malformed spec.permissionClaims: %w", export.GetName(), err)
	}
	identities := make(map[string]string, len(permissionClaims))
	for i, raw := range permissionClaims {
		claim, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("APIExport %q has malformed spec.permissionClaims[%d]", export.GetName(), i)
		}
		group, _, err := unstructured.NestedString(claim, "group")
		if err != nil {
			return nil, fmt.Errorf("APIExport %q has malformed spec.permissionClaims[%d].group: %w", export.GetName(), i, err)
		}
		resource, _, err := unstructured.NestedString(claim, "resource")
		if err != nil || resource == "" {
			return nil, fmt.Errorf("APIExport %q has malformed spec.permissionClaims[%d].resource", export.GetName(), i)
		}
		identityHash, _, err := unstructured.NestedString(claim, "identityHash")
		if err != nil {
			return nil, fmt.Errorf("APIExport %q has malformed spec.permissionClaims[%d].identityHash: %w", export.GetName(), i, err)
		}
		key := groupResourceKey(group, resource)
		if _, duplicate := identities[key]; duplicate {
			return nil, fmt.Errorf("APIExport %q has duplicate permission claim %s", export.GetName(), key)
		}
		identities[key] = identityHash
	}
	return identities, nil
}

type apiExportResourceReference struct {
	Group  string
	Name   string
	Schema string
}

func validateAPIExportReady(export *unstructured.Unstructured, requiredResources []APIExportResource, claims []PermissionClaim) ([]apiExportResourceReference, error) {
	if len(requiredResources) == 0 {
		return nil, fmt.Errorf("CatalogEntry must declare at least one APIExport required resource")
	}
	identityHash, _, err := unstructured.NestedString(export.Object, "status", "identityHash")
	if err != nil {
		return nil, fmt.Errorf("APIExport %q has malformed status.identityHash: %w", export.GetName(), err)
	}
	if identityHash == "" {
		return nil, fmt.Errorf("APIExport %q is waiting for status.identityHash", export.GetName())
	}

	conditions, _, err := unstructured.NestedSlice(export.Object, "status", "conditions")
	if err != nil {
		return nil, fmt.Errorf("APIExport %q has malformed status.conditions: %w", export.GetName(), err)
	}
	identityValid := false
	for _, raw := range conditions {
		condition, ok := raw.(map[string]any)
		if !ok || condition["type"] != "IdentityValid" {
			continue
		}
		identityValid = condition["status"] == "True"
		if !identityValid {
			reason, _ := condition["reason"].(string)
			if reason == "" {
				reason = "not accepted"
			}
			return nil, fmt.Errorf("APIExport %q identity is not valid: %s", export.GetName(), reason)
		}
		break
	}
	if !identityValid {
		return nil, fmt.Errorf("APIExport %q is waiting for IdentityValid=True", export.GetName())
	}

	resources, _, err := unstructured.NestedSlice(export.Object, "spec", "resources")
	if err != nil {
		return nil, fmt.Errorf("APIExport %q has malformed spec.resources: %w", export.GetName(), err)
	}
	resourceReferences := make([]apiExportResourceReference, 0, len(resources))
	actualResources := make(map[string]struct{}, len(resources))
	for i, raw := range resources {
		resource, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("APIExport %q has malformed spec.resources[%d]", export.GetName(), i)
		}
		group, _, err := unstructured.NestedString(resource, "group")
		if err != nil {
			return nil, fmt.Errorf("APIExport %q has malformed spec.resources[%d].group: %w", export.GetName(), i, err)
		}
		name, _, err := unstructured.NestedString(resource, "name")
		if err != nil || name == "" {
			return nil, fmt.Errorf("APIExport %q has malformed spec.resources[%d].name", export.GetName(), i)
		}
		schemaName, _, err := unstructured.NestedString(resource, "schema")
		if err != nil || schemaName == "" {
			return nil, fmt.Errorf("APIExport %q resource %s/%s has no schema", export.GetName(), group, name)
		}
		key := groupResourceKey(group, name)
		if _, duplicate := actualResources[key]; duplicate {
			return nil, fmt.Errorf("APIExport %q has duplicate resource %s", export.GetName(), key)
		}
		actualResources[key] = struct{}{}
		resourceReferences = append(resourceReferences, apiExportResourceReference{Group: group, Name: name, Schema: schemaName})
	}
	requiredKeys := make(map[string]struct{}, len(requiredResources))
	for _, required := range requiredResources {
		key := groupResourceKey(required.Group, required.Name)
		if required.Name == "" {
			return nil, fmt.Errorf("CatalogEntry declares an APIExport required resource with an empty name")
		}
		if _, duplicate := requiredKeys[key]; duplicate {
			return nil, fmt.Errorf("CatalogEntry declares duplicate APIExport required resource %s", key)
		}
		requiredKeys[key] = struct{}{}
		if _, found := actualResources[key]; !found {
			return nil, fmt.Errorf("APIExport %q is waiting for required resource %s", export.GetName(), key)
		}
	}

	permissionClaims, _, err := unstructured.NestedSlice(export.Object, "spec", "permissionClaims")
	if err != nil {
		return nil, fmt.Errorf("APIExport %q has malformed spec.permissionClaims: %w", export.GetName(), err)
	}
	type actualPermissionClaim struct {
		identityHash string
		verbs        sets.Set[string]
	}
	actualClaims := make(map[string]actualPermissionClaim, len(permissionClaims))
	for i, raw := range permissionClaims {
		claim, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("APIExport %q has malformed spec.permissionClaims[%d]", export.GetName(), i)
		}
		group, _, err := unstructured.NestedString(claim, "group")
		if err != nil {
			return nil, fmt.Errorf("APIExport %q has malformed spec.permissionClaims[%d].group: %w", export.GetName(), i, err)
		}
		resource, _, err := unstructured.NestedString(claim, "resource")
		if err != nil || resource == "" {
			return nil, fmt.Errorf("APIExport %q has malformed spec.permissionClaims[%d].resource", export.GetName(), i)
		}
		hash, _, err := unstructured.NestedString(claim, "identityHash")
		if err != nil {
			return nil, fmt.Errorf("APIExport %q has malformed spec.permissionClaims[%d].identityHash: %w", export.GetName(), i, err)
		}
		verbs, _, err := unstructured.NestedStringSlice(claim, "verbs")
		if err != nil {
			return nil, fmt.Errorf("APIExport %q has malformed spec.permissionClaims[%d].verbs: %w", export.GetName(), i, err)
		}
		key := groupResourceKey(group, resource)
		if _, duplicate := actualClaims[key]; duplicate {
			return nil, fmt.Errorf("APIExport %q has duplicate permission claim %s", export.GetName(), key)
		}
		actualClaims[key] = actualPermissionClaim{identityHash: hash, verbs: sets.New(verbs...)}
	}
	expectedClaims := make(map[string]PermissionClaim, len(claims))
	for _, claim := range claims {
		key := groupResourceKey(claim.Group, claim.Resource)
		if claim.Resource == "" {
			return nil, fmt.Errorf("CatalogEntry declares an APIExport permission claim with an empty resource")
		}
		if _, duplicate := expectedClaims[key]; duplicate {
			return nil, fmt.Errorf("CatalogEntry declares duplicate APIExport permission claim %s", key)
		}
		expectedClaims[key] = claim
		actual, found := actualClaims[key]
		if !found {
			return nil, fmt.Errorf("APIExport %q is missing declared permission claim %s", export.GetName(), key)
		}
		if !verbSetsMatch(sets.New(claim.Verbs...), actual.verbs) {
			return nil, fmt.Errorf("APIExport %q permission claim %s verbs do not match CatalogEntry", export.GetName(), key)
		}
		if actual.identityHash != "" {
			if claim.ExpectedIdentityHash == "" {
				return nil, fmt.Errorf("CatalogEntry permission claim %s has no resolved trusted identity", key)
			}
			if actual.identityHash != claim.ExpectedIdentityHash {
				return nil, fmt.Errorf("APIExport %q permission claim %s identityHash does not match its trusted source", export.GetName(), key)
			}
		} else if claim.IdentitySourceKind != "" || claim.IdentitySourceProvider != "" || claim.ExpectedIdentityHash != "" {
			return nil, fmt.Errorf("CatalogEntry permission claim %s declares an identitySource for an identity-less built-in API", key)
		}
	}
	for key := range actualClaims {
		if _, found := expectedClaims[key]; !found {
			return nil, fmt.Errorf("APIExport %q has undeclared permission claim %s", export.GetName(), key)
		}
	}
	return resourceReferences, nil
}

func groupResourceKey(group, resource string) string {
	if group == "" {
		return resource
	}
	return group + "/" + resource
}

func verbSetsMatch(expected, actual sets.Set[string]) bool {
	return expected.Equal(actual)
}

// OnboardedWorkspace is a provider sub-workspace under root:faros:providers
// created by onboarding (independent of whether a CatalogEntry has registered
// the provider yet).
type OnboardedWorkspace struct {
	Name    string
	Cluster string
	Phase   string
}

// ListProviderWorkspaces returns the provider sub-workspaces under
// root:faros:providers. Used by the admin UI so onboarded providers appear even
// before their Helm chart (and CatalogEntry) is installed.
func (p *Provisioner) ListProviderWorkspaces(ctx context.Context) ([]OnboardedWorkspace, error) {
	parent, err := p.clientFor(providersParentWorkspace)
	if err != nil {
		return nil, err
	}
	list, err := parent.Resource(workspaceGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing workspaces in %s: %w", providersParentWorkspace, err)
	}
	out := make([]OnboardedWorkspace, 0, len(list.Items))
	for i := range list.Items {
		w := &list.Items[i]
		cluster, _, _ := unstructured.NestedString(w.Object, "spec", "cluster")
		phase, _, _ := unstructured.NestedString(w.Object, "status", "phase")
		out = append(out, OnboardedWorkspace{Name: w.GetName(), Cluster: cluster, Phase: phase})
	}
	return out, nil
}

// The provider's CatalogEntry APIBinding is no longer created imperatively —
// the `provider` WorkspaceType declares a defaultAPIBinding to
// providers.faros.sh (in system:controllers), so kcp's WorkspaceType
// initializer binds it automatically when the sub-workspace is created.

// ProviderKubeconfigSecretKey is the data key the provider kubeconfig is stored
// under in the Secret the Provider controller writes into system:providers.
const ProviderKubeconfigSecretKey = "kubeconfig"

// WriteKubeconfigSecret create-or-updates a Secret in root:faros:system:providers
// (where the Provider CR lives, NOT the provider sub-workspace) holding the
// provider's minted kubeconfig under key. The Secret lives next to the Provider
// CR so a provider pod (or dev tooling) can read its credentials from one
// well-known place. Idempotent. Ensures the target namespace exists first.
func (p *Provisioner) WriteKubeconfigSecret(ctx context.Context, namespace, name, key string, kc []byte, providerName string) error {
	cfg := rest.CopyConfig(p.kcpConfig)
	cfg.Host = apiurl.KCPClusterURL(cfg.Host, kcppaths.SystemProviders)
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("typed kube client for %s: %w", kcppaths.SystemProviders, err)
	}

	// Defensively ensure the namespace exists.
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
	if _, err := cs.CoreV1().Namespaces().Create(ctx, ns, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("ensuring namespace %s in %s: %w", namespace, kcppaths.SystemProviders, err)
	}

	desired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"providers.faros.sh/provider":   providerName,
				"providers.faros.sh/managed-by": "provider-controller",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{key: kc},
	}
	existing, err := cs.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := cs.CoreV1().Secrets(namespace).Create(ctx, desired, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("creating kubeconfig Secret %s/%s: %w", namespace, name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("getting kubeconfig Secret %s/%s: %w", namespace, name, err)
	}
	desired.SetResourceVersion(existing.GetResourceVersion())
	if _, err := cs.CoreV1().Secrets(namespace).Update(ctx, desired, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("updating kubeconfig Secret %s/%s: %w", namespace, name, err)
	}
	return nil
}

// DeleteKubeconfigSecret removes the kubeconfig Secret from
// root:faros:system:providers. Idempotent (NotFound tolerated).
func (p *Provisioner) DeleteKubeconfigSecret(ctx context.Context, namespace, name string) error {
	cfg := rest.CopyConfig(p.kcpConfig)
	cfg.Host = apiurl.KCPClusterURL(cfg.Host, kcppaths.SystemProviders)
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("typed kube client for %s: %w", kcppaths.SystemProviders, err)
	}
	if err := cs.CoreV1().Secrets(namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting kubeconfig Secret %s/%s: %w", namespace, name, err)
	}
	return nil
}

// DeleteProviderWorkspace deletes the provider sub-workspace
// root:faros:providers/{name}. kcp cascades the ServiceAccount, its token
// Secret, and any APIExport / APIResourceSchemas the provider created there.
// Idempotent (NotFound tolerated).
func (p *Provisioner) DeleteProviderWorkspace(ctx context.Context, name string) error {
	parent, err := p.clientFor(providersParentWorkspace)
	if err != nil {
		return err
	}
	if err := parent.Resource(workspaceGVR).Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("deleting sub-workspace %s: %w", name, err)
	}
	return nil
}

// applyUnstructured is a create-or-update helper for cluster-scoped resources.
// Preserves resourceVersion on update.
func applyUnstructured(ctx context.Context, cl dynamic.Interface, gvr schema.GroupVersionResource, desired *unstructured.Unstructured) error {
	existing, err := cl.Resource(gvr).Get(ctx, desired.GetName(), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = cl.Resource(gvr).Create(ctx, desired, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	desired.SetResourceVersion(existing.GetResourceVersion())
	_, err = cl.Resource(gvr).Update(ctx, desired, metav1.UpdateOptions{})
	return err
}

func (p *Provisioner) clientFor(clusterPath string) (dynamic.Interface, error) {
	cfg := rest.CopyConfig(p.kcpConfig)
	cfg.Host = apiurl.KCPClusterURL(cfg.Host, clusterPath)
	return dynamic.NewForConfig(cfg)
}
