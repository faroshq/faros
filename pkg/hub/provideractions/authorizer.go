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
	"fmt"
	"net/http"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	"github.com/faroshq/faros-kedge/pkg/hub/providers"
	"github.com/faroshq/faros-kedge/pkg/hub/serviceaccounts"
)

var invocationProjectGVR = schema.GroupVersionResource{
	Group: "ai.kedge.faros.sh", Version: "v1alpha1", Resource: "projects",
}

const (
	projectBindingProviderResource  = "providerResource"
	projectBindingProviderReference = "providerReference"
)

// KCPInvocationAuthorizer verifies workload ServiceAccount callers against
// live tenant Project state. It deliberately uses unstructured Project reads
// so the hub does not import App Studio's standalone Go module.
type KCPInvocationAuthorizer struct {
	config serviceaccounts.WorkspaceConfigBuilder

	// Factories are narrow seams for focused tests. Production construction
	// leaves them nil and uses the privileged child-workspace config directly.
	dynamicFactory   func(*rest.Config) (dynamic.Interface, error)
	workloadVerifier func(context.Context, *rest.Config, string, string) (string, *corev1.ServiceAccount, error)
}

// NewKCPInvocationAuthorizer constructs the production Project-backed grant
// authorizer. The config builder must hold hub-privileged credentials; the
// caller's token is used only for TokenReview, never for Project reads.
func NewKCPInvocationAuthorizer(config serviceaccounts.WorkspaceConfigBuilder) *KCPInvocationAuthorizer {
	return &KCPInvocationAuthorizer{config: config}
}

// Authorize implements InvocationAuthorizer. Human identities keep the
// existing provider/resource authorization path. Every SA-shaped identity is
// forced through the workload identity and exact Project grant checks.
func (a *KCPInvocationAuthorizer) Authorize(ctx context.Context, r *http.Request, user, tenantPath string, req invokeRequest, declared providers.ProviderAction) error {
	if !isServiceAccountIdentity(user) {
		return nil
	}
	if a == nil || a.config == nil || r == nil {
		return fmt.Errorf("workload action authorizer unavailable")
	}
	if !resourceMatches(declared.Resource, req.ResourceRef) || req.SchemaDigest != declared.SchemaDigest {
		return fmt.Errorf("workload invocation does not match the catalog action")
	}
	subjectName, ok := workloadServiceAccountName(user)
	if !ok {
		return fmt.Errorf("workload identity subject is malformed")
	}
	orgUUID, wsUUID, ok := tenantWorkspaceIDs(tenantPath)
	if !ok {
		return fmt.Errorf("workload identity tenant path is malformed")
	}
	cfg := a.config.ChildWorkspaceConfig(orgUUID, wsUUID)
	if cfg == nil {
		return fmt.Errorf("workload identity workspace config is unavailable")
	}
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(authHeader) <= len("Bearer ") || !strings.EqualFold(authHeader[:len("Bearer ")], "Bearer ") {
		return fmt.Errorf("workload identity requires a bearer token")
	}
	token := strings.TrimSpace(authHeader[len("Bearer "):])
	if token == "" || strings.ContainsAny(token, "\r\n") {
		return fmt.Errorf("workload identity bearer is malformed")
	}
	verify := a.workloadVerifier
	if verify == nil {
		verify = serviceaccounts.VerifyWorkloadServiceAccountDetails
	}
	username, sa, err := verify(ctx, cfg, token, tenantPath)
	if err != nil || username != user || sa == nil || sa.Name != subjectName || sa.Namespace != serviceaccounts.Namespace {
		return fmt.Errorf("workload ServiceAccount verification failed")
	}
	annotations := sa.GetAnnotations()
	projectName := safeAnnotation(annotations, serviceaccounts.AnnotationWorkloadIdentityProject)
	projectUID := safeAnnotation(annotations, serviceaccounts.AnnotationWorkloadIdentityProjectUID)
	environmentName := safeAnnotation(annotations, serviceaccounts.AnnotationWorkloadIdentityEnvironment)
	instanceName := safeAnnotation(annotations, serviceaccounts.AnnotationWorkloadIdentityInstance)
	scopeMarker := safeAnnotation(annotations, serviceaccounts.AnnotationWorkloadIdentityScope)
	if safeAnnotation(annotations, serviceaccounts.AnnotationWorkloadIdentityTenantPath) != tenantPath || projectName == "" || projectUID == "" || environmentName == "" || instanceName == "" || scopeMarker == "" {
		return fmt.Errorf("workload ServiceAccount identity annotations are incomplete")
	}

	var dyn dynamic.Interface
	if a.dynamicFactory != nil {
		dyn, err = a.dynamicFactory(cfg)
	} else {
		dyn, err = dynamic.NewForConfig(cfg)
	}
	if err != nil {
		return fmt.Errorf("building Project client: %w", err)
	}
	project, err := dyn.Resource(invocationProjectGVR).Get(ctx, projectName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("workload Project is not found")
		}
		return fmt.Errorf("getting workload Project: %w", err)
	}
	if project == nil || project.GetUID() == "" || string(project.GetUID()) != projectUID {
		return fmt.Errorf("workload Project UID does not match identity")
	}
	scope, err := resolveInvocationGrant(project, tenantPath, projectName, projectUID, environmentName, instanceName, req, declared)
	if err != nil {
		return err
	}
	if serviceaccounts.WorkloadServiceAccountName(scope) != subjectName || serviceaccounts.WorkloadIdentityScopeMarker(scope) != scopeMarker {
		return fmt.Errorf("workload ServiceAccount scope does not match Project")
	}
	return nil
}

func resolveInvocationGrant(project *unstructured.Unstructured, tenantPath, projectName, projectUID, environmentName, instanceName string, req invokeRequest, declared providers.ProviderAction) (serviceaccounts.WorkloadIdentityScope, error) {
	environments, found, err := unstructured.NestedSlice(project.Object, "spec", "environments")
	if err != nil || !found {
		return serviceaccounts.WorkloadIdentityScope{}, fmt.Errorf("workload Project environment is not declared")
	}
	var selected map[string]any
	environmentMatches := 0
	for _, raw := range environments {
		environment, ok := raw.(map[string]any)
		if !ok {
			return serviceaccounts.WorkloadIdentityScope{}, fmt.Errorf("workload Project environments are malformed")
		}
		name, found, err := unstructured.NestedString(environment, "name")
		if err != nil || !found || strings.TrimSpace(name) == "" {
			return serviceaccounts.WorkloadIdentityScope{}, fmt.Errorf("workload Project environment name is malformed")
		}
		if name == environmentName {
			environmentMatches++
			selected = environment
		}
	}
	if environmentMatches != 1 {
		return serviceaccounts.WorkloadIdentityScope{}, fmt.Errorf("workload Project environment does not match identity")
	}

	bindings, found, err := unstructured.NestedSlice(selected, "bindings")
	if err != nil || !found {
		return serviceaccounts.WorkloadIdentityScope{}, fmt.Errorf("workload Project environment bindings are not declared")
	}
	resources := make([]serviceaccounts.ProviderResourceScope, 0, len(bindings))
	instanceMatches := 0
	providerReferenceMatches := 0
	grantMatches := 0
	for _, raw := range bindings {
		binding, ok := raw.(map[string]any)
		if !ok {
			return serviceaccounts.WorkloadIdentityScope{}, fmt.Errorf("workload Project binding is malformed")
		}
		kind, found, err := unstructured.NestedString(binding, "kind")
		if err != nil || !found || (kind != projectBindingProviderResource && kind != projectBindingProviderReference) {
			return serviceaccounts.WorkloadIdentityScope{}, fmt.Errorf("workload Project binding kind is malformed")
		}
		bindingName, found, err := unstructured.NestedString(binding, "name")
		if err != nil || !found || strings.TrimSpace(bindingName) == "" {
			return serviceaccounts.WorkloadIdentityScope{}, fmt.Errorf("workload Project binding name is malformed")
		}
		providerName, found, err := unstructured.NestedString(binding, "provider")
		if err != nil || !found || strings.TrimSpace(providerName) == "" {
			return serviceaccounts.WorkloadIdentityScope{}, fmt.Errorf("workload Project binding provider is malformed")
		}
		ref, found, err := unstructured.NestedMap(binding, "resourceRef")
		if err != nil {
			return serviceaccounts.WorkloadIdentityScope{}, fmt.Errorf("workload Project resource reference is malformed")
		}
		if !found {
			if kind == projectBindingProviderReference {
				return serviceaccounts.WorkloadIdentityScope{}, fmt.Errorf("providerReference has no resourceRef")
			}
			continue
		}
		resource, err := invocationResourceScope(ref)
		if err != nil {
			return serviceaccounts.WorkloadIdentityScope{}, err
		}
		if kind == projectBindingProviderResource {
			if resource.Name == instanceName && (providerName == "infrastructure" || (bindingName == "dev" && providerName == "app-studio")) {
				instanceMatches++
				resources = append(resources, resource)
			}
			continue
		}
		resources = append(resources, resource)
		if providerName != req.Provider || !invocationResourceMatches(resource, req.ResourceRef) {
			continue
		}
		providerReferenceMatches++
		matched, err := invocationActionGrant(binding, req, declared)
		if err != nil {
			return serviceaccounts.WorkloadIdentityScope{}, err
		}
		if matched {
			grantMatches++
		}
	}
	if instanceMatches != 1 || providerReferenceMatches != 1 || grantMatches != 1 {
		return serviceaccounts.WorkloadIdentityScope{}, fmt.Errorf("workload Project action grant is not allowed")
	}
	resources = dedupeInvocationResources(resources)
	return serviceaccounts.WorkloadIdentityScope{
		TenantPath: tenantPath, Project: projectName, ProjectUID: projectUID,
		Environment: environmentName, Instance: instanceName, ProviderResources: resources,
	}, nil
}

func invocationActionGrant(binding map[string]any, req invokeRequest, declared providers.ProviderAction) (bool, error) {
	allowed, found, err := unstructured.NestedSlice(binding, "allowedActions")
	if err != nil || !found {
		return false, fmt.Errorf("providerReference allowedActions are not declared")
	}
	declarations := 0
	activeMatches := 0
	for _, raw := range allowed {
		action, ok := raw.(map[string]any)
		if !ok {
			return false, fmt.Errorf("providerReference allowed action is malformed")
		}
		name, nameFound, nameErr := unstructured.NestedString(action, "name")
		version, versionFound, versionErr := unstructured.NestedString(action, "version")
		digest, digestFound, digestErr := unstructured.NestedString(action, "schemaDigest")
		revoked, revokedFound, revokedErr := unstructured.NestedBool(action, "revoked")
		if nameErr != nil || versionErr != nil || digestErr != nil || revokedErr != nil || !nameFound || !versionFound || !digestFound || strings.TrimSpace(name) == "" || strings.TrimSpace(version) == "" || strings.TrimSpace(digest) == "" {
			return false, fmt.Errorf("providerReference allowed action is malformed")
		}
		if name != req.Action || version != req.ActionVersion || digest != req.SchemaDigest || digest != declared.SchemaDigest {
			continue
		}
		declarations++
		if !revokedFound || !revoked {
			activeMatches++
		}
	}
	if declarations > 1 {
		return false, fmt.Errorf("providerReference action grant is ambiguous")
	}
	return activeMatches == 1, nil
}

func invocationResourceScope(ref map[string]any) (serviceaccounts.ProviderResourceScope, error) {
	apiVersion, apiVersionFound, apiVersionErr := unstructured.NestedString(ref, "apiVersion")
	kind, kindFound, kindErr := unstructured.NestedString(ref, "kind")
	resource, resourceFound, resourceErr := unstructured.NestedString(ref, "resource")
	name, nameFound, nameErr := unstructured.NestedString(ref, "name")
	if apiVersionErr != nil || kindErr != nil || resourceErr != nil || nameErr != nil || !apiVersionFound || !kindFound || !resourceFound || !nameFound || strings.TrimSpace(apiVersion) == "" || strings.TrimSpace(kind) == "" || strings.TrimSpace(resource) == "" || strings.TrimSpace(name) == "" {
		return serviceaccounts.ProviderResourceScope{}, fmt.Errorf("workload Project resource reference is incomplete")
	}
	return serviceaccounts.ProviderResourceScope{APIVersion: apiVersion, Kind: kind, Resource: resource, Name: name}, nil
}

func invocationResourceMatches(resource serviceaccounts.ProviderResourceScope, ref resourceRef) bool {
	return resource.APIVersion == ref.APIVersion && resource.Kind == ref.Kind && resource.Resource == ref.Resource && resource.Name == ref.Name
}

func dedupeInvocationResources(resources []serviceaccounts.ProviderResourceScope) []serviceaccounts.ProviderResourceScope {
	seen := make(map[string]struct{}, len(resources))
	out := make([]serviceaccounts.ProviderResourceScope, 0, len(resources))
	for _, resource := range resources {
		key := resource.APIVersion + "\x00" + resource.Resource + "\x00" + resource.Name
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, resource)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].APIVersion+"/"+out[i].Resource+"/"+out[i].Name < out[j].APIVersion+"/"+out[j].Resource+"/"+out[j].Name
	})
	return out
}

func safeAnnotation(annotations map[string]string, key string) string {
	value := annotations[key]
	if value == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n\x00") {
		return ""
	}
	return value
}

func isServiceAccountIdentity(user string) bool {
	return strings.HasPrefix(strings.TrimSpace(user), "system:serviceaccount:")
}

func workloadServiceAccountName(user string) (string, bool) {
	parts := strings.Split(strings.TrimSpace(user), ":")
	if len(parts) != 4 || parts[0] != "system" || parts[1] != "serviceaccount" || parts[2] != serviceaccounts.Namespace || parts[3] == "" {
		return "", false
	}
	return parts[3], true
}

func tenantWorkspaceIDs(path string) (string, string, bool) {
	parts := strings.Split(strings.TrimSpace(path), ":")
	if len(parts) != 5 || parts[0] != "root" || parts[1] != "kedge" || parts[2] != "tenants" || parts[3] == "" || parts[4] == "" {
		return "", "", false
	}
	return parts[3], parts[4], true
}
