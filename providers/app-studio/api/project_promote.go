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

// Promote to production (Phase D of the build→launch loop). "Production" is
// not a mode flip on the Project — it is a SECOND environment alongside the
// development sandbox. Promotion records the exact reviewed source/artifacts
// in an immutable Release and owns a "<project>-prod" Deployment through the
// deployment provider. The provider's kro-direct class preserves today's
// Infrastructure Template backend while moving its lifecycle behind a stable
// deployment contract. The dev sandbox keeps running as a direct
// Infrastructure binding — see docs/app-studio-template-sandboxes.md.

package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	asclient "github.com/faroshq/provider-app-studio/client"
	"github.com/faroshq/provider-app-studio/tenant"
)

// projectRegistryPullSecretName is the tenant Secret (holding a ghcr
// dockerconfigjson) the kro-direct deployment driver currently expects the
// infrastructure provider to bridge into the runtime namespace. This is a POC
// compatibility path: registry credentials ultimately belong behind the
// deployment-provider boundary. The transitional convention remains
// "<instance>-registry" in the tenant default namespace.
func projectRegistryPullSecretName(instanceName string) string {
	return instanceName + "-registry"
}

// ensureProjectRegistryPullSecret derives a ghcr image-pull credential from the
// project's Code connection token and writes it as a dockerconfigjson Secret in
// the tenant workspace, named for the production instance. The infrastructure
// provider's secret-bridge controller carries it into the runtime namespace and
// attaches it to the default ServiceAccount so every production pod can pull the
// private image. A no-op (nil) when there is no connection/token.
func (s *Server) ensureProjectRegistryPullSecret(ctx context.Context, c *asclient.Client, p *aiv1alpha1.Project) error {
	if c == nil || p == nil || p.Spec.Repository == nil {
		return nil
	}
	connectionRef := strings.TrimSpace(p.Spec.Repository.ConnectionRef)
	if connectionRef == "" {
		return nil
	}
	conn, err := c.Resource(codeConnectionResource, "").Get(ctx, connectionRef, metav1.GetOptions{})
	if err != nil {
		return err
	}
	secretName, _, _ := unstructured.NestedString(conn.Object, "spec", "secretRef", "name")
	secretKey, _, _ := unstructured.NestedString(conn.Object, "spec", "secretRef", "key")
	if strings.TrimSpace(secretKey) == "" {
		secretKey = "token"
	}
	login, _, _ := unstructured.NestedString(conn.Object, "status", "login")
	if strings.TrimSpace(secretName) == "" {
		return nil
	}

	tokenSecret, err := c.Resource(secretResource, projectLLMSecretNamespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	token := secretDataValue(tokenSecret, secretKey)
	if strings.TrimSpace(token) == "" {
		return nil
	}

	username := strings.TrimSpace(login)
	if username == "" {
		username = "faros-app-studio" // ghcr validates the token, not the username
	}
	dockerConfig, err := json.Marshal(map[string]any{
		"auths": map[string]any{
			"ghcr.io": map[string]any{
				"username": username,
				"password": token,
				"auth":     base64.StdEncoding.EncodeToString([]byte(username + ":" + token)),
			},
		},
	})
	if err != nil {
		return err
	}

	name := projectRegistryPullSecretName(projectTemplateProdInstanceName(p))
	desired := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata": map[string]any{
			"name":      name,
			"namespace": projectLLMSecretNamespace,
		},
		"type": "kubernetes.io/dockerconfigjson",
		"stringData": map[string]any{
			".dockerconfigjson": string(dockerConfig),
		},
	}}
	res := c.Resource(secretResource, projectLLMSecretNamespace)
	existing, err := res.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = res.Create(ctx, desired, metav1.CreateOptions{})
		return err
	}
	if err != nil {
		return err
	}
	desired.SetResourceVersion(existing.GetResourceVersion())
	_, err = res.Update(ctx, desired, metav1.UpdateOptions{})
	return err
}

const (
	projectProductionEnvironmentName = "production"
	projectProductionBindingName     = "prod"
	// projectProductionHostnamePrefixPath is tenant-selected on the first
	// production deployment, then becomes part of the instance's public
	// identity. Re-promoting must not silently move that identity.
	projectProductionHostnamePrefixPath = "expose.hostnamePrefix"
	// projectRedeployRevisionField is a platform-owned input on the
	// infrastructure Template instance. A fresh value is minted for every
	// accepted promotion so a provider can roll only the application workload
	// pods without recreating the production instance (or its database).
	projectRedeployRevisionField = "farosRedeployRevision"

	projectDeploymentProvider   = "deployments"
	projectDeploymentAPIVersion = "deployments.faros.sh/v1alpha1"
	projectDeploymentKind       = "Deployment"
	projectDeploymentResource   = "deployments"
	projectReleaseKind          = "Release"
	projectReleaseResource      = "releases"
	projectDeploymentClassName  = "kro-direct"

	projectToolPromoteProject = "promote_project"
)

var projectPlatformOwnedProductionFields = map[string]struct{}{
	"name":                       {},
	"farosMode":                  {},
	projectRedeployRevisionField: {},
	"farosCluster":               {},
	"credentialsSecretName":      {},
}

var (
	projectReleaseGVR = schema.GroupVersionResource{Group: "deployments.faros.sh", Version: "v1alpha1", Resource: projectReleaseResource}
)

// Nested platform-owned fields need explicit paths because expose itself is a
// mixed-ownership object: hostnamePrefix is a first-deploy tenant input, while
// fqdn is stamped by the infrastructure controller.
var projectPlatformOwnedProductionPaths = map[string]struct{}{
	"expose.fqdn": {},
}

// projectPromoteRequest is the "Promote to Prod" form submission: the
// template's production inputs (ports, replicas, oidc, …). The instance name,
// farosMode, per-component image fields, and farosRedeployRevision are
// platform-owned and ignored if supplied — name/farosMode are deterministic,
// images come from the build, and the revision is minted for this promotion.
type projectPromoteRequest struct {
	Values map[string]any `json:"values,omitempty"`
}

type projectPromoteResponse struct {
	Environment     string                       `json:"environment"`
	Instance        string                       `json:"instance"`
	RolloutRevision string                       `json:"rolloutRevision"`
	GitOps          *projectGitOpsPromotionView  `json:"gitOps,omitempty"`
	Components      []projectBuildCheckComponent `json:"components,omitempty"`
	Project         json.RawMessage              `json:"project,omitempty"`
}

type projectGitOpsPromotionView struct {
	Phase         string `json:"phase"`
	ChangeRequest string `json:"changeRequest"`
	Branch        string `json:"branch"`
}

// newProjectRedeployRevision mints an opaque, non-secret rollout token. It is
// deliberately independent of the production instance identity: re-promoting
// must preserve the instance name/UID while still changing its workload pod
// template.
func newProjectRedeployRevision() string {
	return uuid.NewString()
}

// projectTemplateProdBinding builds the production binding: an instance of the
// template kind named "<project>-prod", provisioned with farosMode: production,
// the user's production input values, and each imageInput set to the built
// digest. Platform-owned fields (name, farosMode, image inputs, and the
// rollout revision) always win over anything in values. The optional revision
// argument exists so promoteProject can mint once and return the exact value
// written to the binding; callers that omit it get a fresh revision too.
func projectTemplateProdBinding(p *aiv1alpha1.Project, info projectTemplateInfo, images map[string]string, values map[string]any, rolloutRevisions ...string) (aiv1alpha1.ProjectProviderBindingSpec, error) {
	name := projectTemplateProdInstanceName(p)
	if name == "" {
		return aiv1alpha1.ProjectProviderBindingSpec{}, fmt.Errorf("project has no name")
	}
	rolloutRevision := ""
	for _, candidate := range rolloutRevisions {
		if candidate = strings.TrimSpace(candidate); candidate != "" {
			rolloutRevision = candidate
			break
		}
	}
	if rolloutRevision == "" {
		rolloutRevision = newProjectRedeployRevision()
	}
	merged := projectProductionInputValues(info, images, values)
	if err := preserveAndValidateProjectImmutableInputs(p, info, merged); err != nil {
		return aiv1alpha1.ProjectProviderBindingSpec{}, err
	}
	for imageInput, image := range images {
		merged[imageInput] = image
	}
	merged["name"] = name
	merged["farosMode"] = "production"
	merged[projectRedeployRevisionField] = rolloutRevision
	if err := validateProjectProductionValue(info.ProductionSchema, merged, "production settings"); err != nil {
		return aiv1alpha1.ProjectProviderBindingSpec{}, newValidationError(err.Error())
	}
	raw, err := json.Marshal(merged)
	if err != nil {
		return aiv1alpha1.ProjectProviderBindingSpec{}, err
	}
	return aiv1alpha1.ProjectProviderBindingSpec{
		Name:     projectProductionBindingName,
		Provider: projectDevelopmentProviderAppStudio,
		Kind:     aiv1alpha1.ProjectBindingKindProviderResource,
		ResourceRef: &aiv1alpha1.ProjectProviderResourceReference{
			Name:       name,
			APIVersion: info.APIVersion,
			Kind:       info.Kind,
			Resource:   info.Resource,
		},
		Values: runtime.RawExtension{Raw: raw},
	}, nil
}

type projectReleaseArtifact struct {
	Name  string `json:"name"`
	Image string `json:"image"`
}

type projectReleaseSpec struct {
	Source struct {
		RepositoryRef string `json:"repositoryRef"`
		Revision      string `json:"revision"`
	} `json:"source"`
	BlueprintRef struct {
		Name string `json:"name"`
	} `json:"blueprintRef"`
	Artifacts []projectReleaseArtifact `json:"artifacts"`
}

// projectDeploymentProdBinding validates the exact same Infrastructure
// Template production schema as the legacy direct binding, then stores only
// tenant-authored inputs under Deployment.spec.configuration. Images and the
// reviewed source revision are immutable Release inputs; rolloutID is the
// mutable redeploy trigger.
func projectDeploymentProdBinding(p *aiv1alpha1.Project, info projectTemplateInfo, releaseName string, images map[string]string, values map[string]any, rolloutID string) (aiv1alpha1.ProjectProviderBindingSpec, error) {
	name := projectTemplateProdInstanceName(p)
	if name == "" {
		return aiv1alpha1.ProjectProviderBindingSpec{}, fmt.Errorf("project has no name")
	}
	configuration := projectProductionInputValues(info, images, values)
	if err := preserveAndValidateProjectImmutableInputs(p, info, configuration); err != nil {
		return aiv1alpha1.ProjectProviderBindingSpec{}, err
	}
	validated := make(map[string]any, len(configuration)+len(images)+3)
	for key, value := range configuration {
		validated[key] = value
	}
	for imageInput, image := range images {
		validated[imageInput] = image
	}
	validated["name"] = name
	validated["farosMode"] = "production"
	validated[projectRedeployRevisionField] = rolloutID
	if err := validateProjectProductionValue(info.ProductionSchema, validated, "production settings"); err != nil {
		return aiv1alpha1.ProjectProviderBindingSpec{}, newValidationError(err.Error())
	}
	raw, err := json.Marshal(map[string]any{
		"releaseRef":    releaseName,
		"className":     projectDeploymentClassName,
		"configuration": configuration,
		"rolloutID":     rolloutID,
	})
	if err != nil {
		return aiv1alpha1.ProjectProviderBindingSpec{}, err
	}
	return aiv1alpha1.ProjectProviderBindingSpec{
		Name:     projectProductionBindingName,
		Provider: projectDeploymentProvider,
		Kind:     aiv1alpha1.ProjectBindingKindProviderResource,
		ResourceRef: &aiv1alpha1.ProjectProviderResourceReference{
			Name:       name,
			APIVersion: projectDeploymentAPIVersion,
			Kind:       projectDeploymentKind,
			Resource:   projectDeploymentResource,
		},
		Values: runtime.RawExtension{Raw: raw},
	}, nil
}

func projectReleaseForPromotion(p *aiv1alpha1.Project, info projectTemplateInfo, commitSHA string, images map[string]string) (string, projectReleaseSpec, error) {
	var spec projectReleaseSpec
	if p == nil || p.Spec.Repository == nil || strings.TrimSpace(p.Spec.Repository.RepositoryRef) == "" {
		return "", spec, newValidationError("project has no linked repository to release")
	}
	spec.Source.RepositoryRef = strings.TrimSpace(p.Spec.Repository.RepositoryRef)
	spec.Source.Revision = strings.TrimSpace(commitSHA)
	spec.BlueprintRef.Name = strings.TrimSpace(p.Spec.Template.Name)
	for name, image := range images {
		spec.Artifacts = append(spec.Artifacts, projectReleaseArtifact{Name: name, Image: image})
	}
	sort.Slice(spec.Artifacts, func(i, j int) bool { return spec.Artifacts[i].Name < spec.Artifacts[j].Name })
	canonical, err := json.Marshal(spec)
	if err != nil {
		return "", spec, err
	}
	digest := sha256.Sum256(canonical)
	prefix := strings.TrimSuffix(strings.TrimSpace(p.Name), "-")
	if len(prefix) > 50 {
		prefix = strings.TrimSuffix(prefix[:50], "-")
	}
	return prefix + "-" + hex.EncodeToString(digest[:])[:12], spec, nil
}

func ensureProjectRelease(ctx context.Context, c *asclient.Client, p *aiv1alpha1.Project, name string, spec projectReleaseSpec) error {
	desiredRaw, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	var desired map[string]any
	if err := json.Unmarshal(desiredRaw, &desired); err != nil {
		return err
	}
	resource := c.Resource(tenant.Resource{GVR: projectReleaseGVR, Kind: projectReleaseKind, Plural: "Releases"}, "")
	existing, err := resource.Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		return verifyProjectReleaseSpec(name, existing, desired)
	}
	if !apierrors.IsNotFound(err) {
		return err
	}
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": projectDeploymentAPIVersion,
		"kind":       projectReleaseKind,
		"metadata":   map[string]any{"name": name},
		"spec":       desired,
	}}
	if owner := bindingsOwnerReference(p); owner != nil {
		obj.SetOwnerReferences([]metav1.OwnerReference{*owner})
	}
	_, err = resource.Create(ctx, obj, metav1.CreateOptions{})
	if !apierrors.IsAlreadyExists(err) {
		return err
	}
	existing, err = resource.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	return verifyProjectReleaseSpec(name, existing, desired)
}

func verifyProjectReleaseSpec(name string, release *unstructured.Unstructured, desired map[string]any) error {
	observed, found, err := unstructured.NestedMap(release.Object, "spec")
	if err != nil || !found {
		return fmt.Errorf("existing Release %q has no readable spec", name)
	}
	if !reflect.DeepEqual(observed, desired) {
		return fmt.Errorf("immutable Release %q already exists with different contents", name)
	}
	return nil
}

func bindingsOwnerReference(p *aiv1alpha1.Project) *metav1.OwnerReference {
	if p == nil || p.UID == "" {
		return nil
	}
	controller := true
	return &metav1.OwnerReference{APIVersion: aiv1alpha1.SchemeGroupVersion.String(), Kind: "Project", Name: p.Name, UID: p.UID, Controller: &controller}
}

// projectProductionInputValues keeps the promotion boundary honest even when
// a caller bypasses the portal form. Template fields computed by the platform,
// and image inputs owned by the reviewed build, never become tenant-authored
// production configuration. Nested computed fields (for example expose.fqdn)
// are removed recursively.
func projectProductionInputValues(info projectTemplateInfo, images map[string]string, values map[string]any) map[string]any {
	return filterProjectProductionObject(info.ProductionSchema, images, values, "")
}

func filterProjectProductionObject(schema map[string]any, images map[string]string, values map[string]any, paths ...string) map[string]any {
	if values == nil {
		return map[string]any{}
	}
	path := ""
	if len(paths) > 0 {
		path = paths[0]
	}
	properties, _ := schema["properties"].(map[string]any)
	out := make(map[string]any, len(values))
	for name, value := range values {
		fieldPath := name
		if path != "" {
			fieldPath = path + "." + name
		}
		if _, reserved := projectPlatformOwnedProductionFields[name]; reserved {
			continue
		}
		if _, reserved := projectPlatformOwnedProductionPaths[fieldPath]; reserved {
			continue
		}
		if _, imageOwned := images[name]; imageOwned {
			continue
		}
		field, _ := properties[name].(map[string]any)
		description, _ := field["description"].(string)
		if strings.HasPrefix(strings.TrimSpace(description), "Computed by the platform") {
			continue
		}
		out[name] = filterProjectProductionValue(field, images, value, fieldPath)
	}
	return out
}

func filterProjectProductionValue(schema map[string]any, images map[string]string, value any, paths ...string) any {
	path := ""
	if len(paths) > 0 {
		path = paths[0]
	}
	switch typed := value.(type) {
	case map[string]any:
		return filterProjectProductionObject(schema, images, typed, path)
	case []any:
		items, _ := schema["items"].(map[string]any)
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = filterProjectProductionValue(items, images, typed[i], path)
		}
		return out
	default:
		return value
	}
}

func projectNestedValue(values map[string]any, path string) (any, bool) {
	var current any = values
	for _, segment := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[segment]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func projectSetNestedValue(values map[string]any, path string, value any) {
	segments := strings.Split(path, ".")
	current := values
	for _, segment := range segments[:len(segments)-1] {
		nested, _ := current[segment].(map[string]any)
		if nested == nil {
			nested = map[string]any{}
			current[segment] = nested
		}
		current = nested
	}
	current[segments[len(segments)-1]] = value
}

func projectSchemaDefault(schema map[string]any, path string) (any, bool) {
	current := schema
	for _, segment := range strings.Split(path, ".") {
		properties, _ := current["properties"].(map[string]any)
		current, _ = properties[segment].(map[string]any)
		if current == nil {
			return nil, false
		}
	}
	value, ok := current["default"]
	return value, ok
}

func projectSchemaPathDeclared(schema map[string]any, path string) bool {
	current := schema
	for _, segment := range strings.Split(path, ".") {
		properties, _ := current["properties"].(map[string]any)
		next, ok := properties[segment].(map[string]any)
		if !ok {
			return false
		}
		current = next
	}
	return true
}

func projectProductionImmutableInputPaths(info projectTemplateInfo) []string {
	paths := append([]string(nil), info.ImmutableProductionInputs...)
	if !projectSchemaPathDeclared(info.ProductionSchema, projectProductionHostnamePrefixPath) {
		return paths
	}
	for _, path := range paths {
		if path == projectProductionHostnamePrefixPath {
			return paths
		}
	}
	return append(paths, projectProductionHostnamePrefixPath)
}

func projectProductionHostnamePrefixDefault(p *aiv1alpha1.Project, info projectTemplateInfo) (any, bool) {
	if value, ok := projectSchemaDefault(info.ProductionSchema, projectProductionHostnamePrefixPath); ok {
		return value, true
	}
	if !projectSchemaPathDeclared(info.ProductionSchema, projectProductionHostnamePrefixPath) {
		return nil, false
	}
	if instance := projectTemplateProdInstanceName(p); instance != "" {
		// The infrastructure controller uses the instance name when the optional
		// prefix is omitted. Treat that effective value as the first-deploy input.
		return instance, true
	}
	return nil, false
}

func preserveAndValidateProjectImmutableInputs(p *aiv1alpha1.Project, info projectTemplateInfo, values map[string]any) error {
	existing := findProjectProductionBinding(p)
	if existing == nil {
		return nil
	}
	previous := projectProductionConfiguration(existing)
	for _, path := range projectProductionImmutableInputPaths(info) {
		oldValue, oldFound := projectNestedValue(previous, path)
		if !oldFound {
			oldValue, oldFound = projectSchemaDefault(info.ProductionSchema, path)
		}
		if !oldFound && path == projectProductionHostnamePrefixPath {
			oldValue, oldFound = projectProductionHostnamePrefixDefault(p, info)
		}
		newValue, newFound := projectNestedValue(values, path)
		if !newFound {
			if oldFound {
				projectSetNestedValue(values, path, oldValue)
			}
			continue
		}
		if !oldFound {
			oldValue, oldFound = projectSchemaDefault(info.ProductionSchema, path)
		}
		if oldFound && !reflect.DeepEqual(oldValue, newValue) {
			return newValidationError(fmt.Sprintf("production setting %q is locked after the first deployment", path))
		}
	}
	return nil
}

func projectSchemaNumber(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case float32:
		return float64(number), true
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	default:
		return 0, false
	}
}

func projectSchemaStrings(value any) []string {
	switch list := value.(type) {
	case []string:
		return list
	case []any:
		out := make([]string, 0, len(list))
		for _, item := range list {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func validateProjectProductionValue(schema map[string]any, value any, path string) error {
	if len(schema) == 0 {
		return nil
	}
	if enum, ok := schema["enum"].([]any); ok {
		matched := false
		for _, candidate := range enum {
			matched = matched || reflect.DeepEqual(candidate, value)
		}
		if !matched {
			return fmt.Errorf("%s must be one of the allowed values", path)
		}
	}
	typeName, _ := schema["type"].(string)
	switch typeName {
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be an object", path)
		}
		properties, _ := schema["properties"].(map[string]any)
		for _, name := range projectSchemaStrings(schema["required"]) {
			if name != "" {
				if _, found := object[name]; !found {
					return fmt.Errorf("%s.%s is required", path, name)
				}
			}
		}
		for name, nested := range object {
			field, found := properties[name].(map[string]any)
			if !found {
				if allowed, ok := schema["additionalProperties"].(bool); ok && !allowed {
					return fmt.Errorf("%s.%s is not supported", path, name)
				}
				field, _ = schema["additionalProperties"].(map[string]any)
			}
			if err := validateProjectProductionValue(field, nested, path+"."+name); err != nil {
				return err
			}
		}
	case "array":
		items, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s must be a list", path)
		}
		itemSchema, _ := schema["items"].(map[string]any)
		for i := range items {
			if err := validateProjectProductionValue(itemSchema, items[i], fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case "string":
		text, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s must be text", path)
		}
		length := float64(utf8.RuneCountInString(text))
		if minimum, ok := projectSchemaNumber(schema["minLength"]); ok && length < minimum {
			return fmt.Errorf("%s is too short", path)
		}
		if maximum, ok := projectSchemaNumber(schema["maxLength"]); ok && length > maximum {
			return fmt.Errorf("%s is too long", path)
		}
		if pattern, _ := schema["pattern"].(string); pattern != "" {
			expression, err := regexp.Compile(pattern)
			if err != nil {
				return fmt.Errorf("template schema pattern for %s is invalid: %w", path, err)
			}
			if !expression.MatchString(text) {
				return fmt.Errorf("%s has an invalid format", path)
			}
		}
	case "number", "integer":
		number, ok := projectSchemaNumber(value)
		if !ok || (typeName == "integer" && math.Trunc(number) != number) {
			return fmt.Errorf("%s must be a %s", path, typeName)
		}
		if minimum, ok := projectSchemaNumber(schema["minimum"]); ok && number < minimum {
			return fmt.Errorf("%s must be at least %v", path, minimum)
		}
		if maximum, ok := projectSchemaNumber(schema["maximum"]); ok && number > maximum {
			return fmt.Errorf("%s must be no more than %v", path, maximum)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s must be true or false", path)
		}
	}
	return nil
}

func projectBindingValues(binding *aiv1alpha1.ProjectProviderBindingSpec) map[string]any {
	if binding == nil || len(binding.Values.Raw) == 0 {
		return nil
	}
	var values map[string]any
	if err := json.Unmarshal(binding.Values.Raw, &values); err != nil {
		return nil
	}
	return values
}

func projectIsDeploymentBinding(binding *aiv1alpha1.ProjectProviderBindingSpec) bool {
	return binding != nil && binding.ResourceRef != nil &&
		binding.Provider == projectDeploymentProvider &&
		binding.ResourceRef.APIVersion == projectDeploymentAPIVersion &&
		binding.ResourceRef.Kind == projectDeploymentKind &&
		binding.ResourceRef.Resource == projectDeploymentResource
}

func projectProductionConfiguration(binding *aiv1alpha1.ProjectProviderBindingSpec) map[string]any {
	values := projectBindingValues(binding)
	if !projectIsDeploymentBinding(binding) {
		return values
	}
	configuration, _ := values["configuration"].(map[string]any)
	return configuration
}

func projectRequestedRedeployRevision(binding *aiv1alpha1.ProjectProviderBindingSpec) string {
	values := projectBindingValues(binding)
	if projectIsDeploymentBinding(binding) {
		revision, _ := values["rolloutID"].(string)
		return strings.TrimSpace(revision)
	}
	revision, _ := values[projectRedeployRevisionField].(string)
	return strings.TrimSpace(revision)
}

// promoteProject stands up (or re-deploys) the project's production
// environment from the current build evidence. It refuses unless every
// launchable component has a built image (check_project_build == "built"), so
// production never references an image that was not built.
func (s *Server) promoteProject(ctx context.Context, c *asclient.Client, id identity, p *aiv1alpha1.Project, httpReq *http.Request, values map[string]any) (*aiv1alpha1.Project, projectPromoteResponse, error) {
	if p.Spec.Template == nil || strings.TrimSpace(p.Spec.Template.Name) == "" {
		return nil, projectPromoteResponse{}, newValidationError("project has no template to promote; select a template and build first")
	}

	// The digest tether (vibe promote semantics): refuse to ship while the
	// workspace holds uncommitted changes — the built images were made from
	// git, and promoting over a dirty workspace would run production on code
	// the user is no longer looking at. The Project reconciler's commit
	// convergence clears this on its own once the project is idle.
	if s.workspaces != nil {
		if dirty, err := s.workspaces.UncommittedPaths(ctx, projectWorkspaceScope(id, p)); err == nil && len(dirty) > 0 {
			return nil, projectPromoteResponse{}, newValidationError(fmt.Sprintf(
				"the workspace has %d uncommitted file(s); commit them (or wait for the automatic sync) and rebuild before promoting", len(dirty)))
		}
	}

	check, err := s.checkProjectBuild(ctx, c, id, p)
	if err != nil {
		return nil, projectPromoteResponse{}, err
	}
	if check.Status != "built" {
		return nil, projectPromoteResponse{}, newValidationError("project is not ready to promote: " + check.Note)
	}

	info, err := fetchProjectTemplate(ctx, c, p.Spec.Template.Name)
	if err != nil {
		return nil, projectPromoteResponse{}, err
	}

	images := make(map[string]string, len(check.Components))
	artifacts := make(map[string]string, len(check.Components))
	for _, comp := range check.Components {
		if comp.ImageInput != "" && comp.Image != "" {
			images[comp.ImageInput] = comp.Image
			artifacts[comp.Name] = comp.Image
		}
	}
	if len(images) == 0 {
		return nil, projectPromoteResponse{}, newValidationError("no built component images recorded for this project")
	}

	rolloutRevision := newProjectRedeployRevision()
	releaseName, releaseSpec, err := projectReleaseForPromotion(p, info, check.CommitSHA, artifacts)
	if err != nil {
		return nil, projectPromoteResponse{}, err
	}
	binding, err := projectDeploymentProdBinding(p, info, releaseName, images, values, rolloutRevision)
	if err != nil {
		return nil, projectPromoteResponse{}, err
	}
	if projectProductionIsGitManaged(p) {
		deploymentValues := projectBindingValues(&binding)
		configuration, _ := deploymentValues["configuration"].(map[string]any)
		gitBinding, gitView, err := s.proposeProjectGitOpsPromotion(ctx, c, id, p, httpReq, releaseName, releaseSpec, configuration)
		if err != nil {
			return nil, projectPromoteResponse{}, err
		}
		next := p.DeepCopy()
		upsertProjectProductionBinding(next, gitBinding)
		// Registry credential delivery remains outside Git: secret material is
		// runtime authority, not desired configuration committed to a repo.
		_ = s.ensureProjectRegistryPullSecret(ctx, c, p)
		updated, err := c.Projects().Update(ctx, next, metav1.UpdateOptions{})
		if err != nil {
			return nil, projectPromoteResponse{}, err
		}
		raw, _ := json.Marshal(updated)
		return updated, projectPromoteResponse{
			Environment: projectProductionEnvironmentName,
			Instance:    projectTemplateProdInstanceName(p),
			// No rollout exists until the PR merges and RepositorySync records
			// the immutable configuration revision.
			RolloutRevision: "",
			GitOps:          &gitView,
			Components:      check.Components,
			Project:         raw,
		}, nil
	}
	if err := ensureProjectRelease(ctx, c, p, releaseName, releaseSpec); err != nil {
		return nil, projectPromoteResponse{}, err
	}

	next := p.DeepCopy()
	upsertProjectProductionBinding(next, binding)

	// Transitional POC compatibility: mint the pull credential expected by the
	// current kro-direct/Infrastructure backend. The deployment provider should
	// own this boundary once registry credential delivery is part of its target
	// contract. Best-effort remains intentional because public images need none.
	_ = s.ensureProjectRegistryPullSecret(ctx, c, p)

	updated, err := c.Projects().Update(ctx, next, metav1.UpdateOptions{})
	if err != nil {
		return nil, projectPromoteResponse{}, err
	}
	// Promotion IS the spec write: the Project reconciler converges every
	// environment's bindings — the production artifact binding just appended
	// included — so no explicit provisioning happens here anymore.
	reconciled := projectWithLiveBindingStatus(ctx, c, updated, id)

	raw, _ := json.Marshal(reconciled)
	return reconciled, projectPromoteResponse{
		Environment:     projectProductionEnvironmentName,
		Instance:        projectTemplateProdInstanceName(p),
		RolloutRevision: rolloutRevision,
		Components:      check.Components,
		Project:         raw,
	}, nil
}

// upsertProjectProductionBinding sets the production environment's binding,
// replacing any existing one (re-promote redeploys), and leaves every other
// environment — notably the live development sandbox — untouched.
func upsertProjectProductionBinding(p *aiv1alpha1.Project, binding aiv1alpha1.ProjectProviderBindingSpec) {
	for i := range p.Spec.Environments {
		env := &p.Spec.Environments[i]
		if strings.TrimSpace(env.Name) != projectProductionEnvironmentName {
			continue
		}
		kept := env.Bindings[:0]
		for _, b := range env.Bindings {
			managedReference := b.Kind == aiv1alpha1.ProjectBindingKindProviderReference && projectIsDeploymentBinding(&b)
			if strings.TrimSpace(b.Name) == projectProductionBindingName &&
				(b.Kind != aiv1alpha1.ProjectBindingKindProviderReference || managedReference) {
				continue
			}
			kept = append(kept, b)
		}
		env.Bindings = append(kept, binding)
		return
	}
	p.Spec.Environments = append(p.Spec.Environments, aiv1alpha1.ProjectEnvironmentSpec{
		Name:      projectProductionEnvironmentName,
		Mode:      aiv1alpha1.ProjectEnvironmentModeArtifact,
		Promotion: aiv1alpha1.ProjectPromotionManual,
		Bindings:  []aiv1alpha1.ProjectProviderBindingSpec{binding},
	})
}

// promoteProjectHandler is POST /api/projects/{project}/promote — the portal's
// "Promote to Prod" action.
func (s *Server) promoteProjectHandler(w http.ResponseWriter, r *http.Request) {
	c, id, p, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	var req projectPromoteRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
			writeStatus(w, http.StatusBadRequest, "BadRequest", "invalid JSON body: "+err.Error())
			return
		}
	}
	_, resp, err := s.promoteProject(r.Context(), c, id, p, r, req.Values)
	if err != nil {
		writeProjectPromoteError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// projectPromotionReadinessResponse gates the portal's "Promote to Prod"
// button and seeds its form: whether the build is green, the component image
// plan, and the production instance name.
type projectPromotionReadinessResponse struct {
	Template                  string                  `json:"template,omitempty"`
	Instance                  string                  `json:"instance,omitempty"`
	ProductionSchema          map[string]any          `json:"productionSchema,omitempty"`
	ImmutableProductionInputs []string                `json:"immutableProductionInputs,omitempty"`
	ProductionValues          map[string]any          `json:"productionValues,omitempty"`
	RequestedRolloutRevision  string                  `json:"requestedRolloutRevision,omitempty"`
	ObservedRolloutRevision   string                  `json:"observedRolloutRevision,omitempty"`
	ProductionObserved        bool                    `json:"productionObserved"`
	Promotable                bool                    `json:"promotable"`
	Build                     projectBuildCheckResult `json:"build"`
	// Production reports the live production environment when the project has
	// been promoted at least once: its phase and, once serving, its URL. Nil
	// when the project has never been promoted. ProductionObserved separately
	// reports whether the referenced provider object was successfully fetched;
	// a Pending status alone is not evidence that the Deployment exists.
	Production *aiv1alpha1.ProjectProviderBindingStatus `json:"production,omitempty"`
}

// findProjectProductionBinding returns the project's production binding spec,
// or nil when it has never been promoted.
func findProjectProductionBinding(p *aiv1alpha1.Project) *aiv1alpha1.ProjectProviderBindingSpec {
	for i := range p.Spec.Environments {
		env := &p.Spec.Environments[i]
		if strings.TrimSpace(env.Name) != projectProductionEnvironmentName {
			continue
		}
		for j := range env.Bindings {
			if strings.TrimSpace(env.Bindings[j].Name) == projectProductionBindingName &&
				(env.Bindings[j].Kind != aiv1alpha1.ProjectBindingKindProviderReference || projectIsDeploymentBinding(&env.Bindings[j])) {
				return &env.Bindings[j]
			}
		}
	}
	return nil
}

// getProjectPromotion is GET /api/projects/{project}/promotion — the portal
// polls it to enable the "Promote to Prod" button (promotable) and to show the
// image plan; the template's production input schema for the form comes from
// the infrastructure describe-template surface.
func (s *Server) getProjectPromotion(w http.ResponseWriter, r *http.Request) {
	c, id, p, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	check, err := s.checkProjectBuild(r.Context(), c, id, p)
	if err != nil {
		writeProjectPromoteError(w, err)
		return
	}
	template := ""
	if p.Spec.Template != nil {
		template = strings.TrimSpace(p.Spec.Template.Name)
	}
	resp := projectPromotionReadinessResponse{
		Template:   template,
		Instance:   projectTemplateProdInstanceName(p),
		Promotable: check.Status == "built",
		Build:      check,
	}
	if template != "" {
		info, templateErr := fetchProjectTemplate(r.Context(), c, template)
		if templateErr != nil {
			writeProjectPromoteError(w, templateErr)
			return
		}
		resp.ProductionSchema = info.ProductionSchema
		resp.ImmutableProductionInputs = projectProductionImmutableInputPaths(info)
	}
	// CI explains absent artifacts but never overrides the Package-based gate.
	// Keep lookup failure additive so a registry-ready release stays deployable.
	resp.Build.Run, resp.Build.RunError = s.observeProjectBuildRun(r.Context(), id, p, r, check.CommitSHA)
	// Artifact-mode (production) environments are not reported by the live
	// (development) environment status surface, so read the production
	// binding's status directly for its phase and serving URL.
	if prod := findProjectProductionBinding(p); prod != nil {
		populateProjectPromotionProduction(r.Context(), c, p, *prod, id, &resp)
	}
	writeJSON(w, http.StatusOK, resp)
}

// populateProjectPromotionProduction keeps desired binding state separate
// from provider observation. In particular, a status synthesized for a
// missing object must not make the portal believe that a Deployment exists.
func populateProjectPromotionProduction(ctx context.Context, c *asclient.Client, p *aiv1alpha1.Project, prod aiv1alpha1.ProjectProviderBindingSpec, id identity, resp *projectPromotionReadinessResponse) {
	if resp == nil {
		return
	}
	resp.ProductionValues = projectProductionConfiguration(&prod)
	resp.RequestedRolloutRevision = projectRequestedRedeployRevision(&prod)
	st := projectProviderBindingStatus(ctx, c, p, prod, id)
	resp.Production = &st
	// Read the provider instance's spec, not the desired Project binding,
	// so clients can distinguish the old Ready deployment from a rollout
	// revision the Project controller has actually delivered downstream.
	instance, observeErr := observeProjectProviderBinding(ctx, c, p, prod, id)
	if observeErr != nil {
		return
	}
	resp.ProductionObserved = true
	if projectProductionIsGitManaged(p) && projectIsDeploymentBinding(&prod) {
		if configuration, rolloutID := projectGitOpsObservedProduction(instance); configuration != nil {
			resp.ProductionValues = configuration
			if rolloutID != "" {
				resp.RequestedRolloutRevision = rolloutID
			}
		}
	}
	resp.ObservedRolloutRevision = projectObservedRedeployRevision(instance)
}

func projectGitOpsObservedProduction(instance *unstructured.Unstructured) (map[string]any, string) {
	if instance == nil {
		return nil, ""
	}
	configuration, found, err := unstructured.NestedMap(instance.Object, "spec", "configuration")
	if err != nil || !found {
		return nil, ""
	}
	rolloutID, _, _ := unstructured.NestedString(instance.Object, "spec", "rolloutID")
	return configuration, strings.TrimSpace(rolloutID)
}

func projectObservedRedeployRevision(instance *unstructured.Unstructured) string {
	if instance == nil {
		return ""
	}
	if instance.GetAPIVersion() == projectDeploymentAPIVersion && instance.GetKind() == projectDeploymentKind {
		revision, _, _ := unstructured.NestedString(instance.Object, "status", "observedRolloutID")
		return strings.TrimSpace(revision)
	}
	revision, _, _ := unstructured.NestedString(instance.Object, "spec", "values", projectRedeployRevisionField)
	return strings.TrimSpace(revision)
}

func writeProjectPromoteError(w http.ResponseWriter, err error) {
	var validationErr *ValidationError
	if errors.As(err, &validationErr) {
		writeStatus(w, http.StatusBadRequest, "BadRequest", err.Error())
		return
	}
	writeStatus(w, http.StatusBadGateway, "BadGateway", err.Error())
}
