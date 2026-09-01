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
// development sandbox: an artifact-mode ProjectEnvironment bound to a
// "<project>-prod" instance of the SAME template, provisioned with
// farosMode: production and each template imageInput set to the digest the
// per-component build recorded in git. The user promotes explicitly ("Promote
// to Prod") once the sandbox looks good and the build is green; promotion is
// repeatable (re-promote redeploys the latest digests). The dev sandbox keeps
// running untouched — see docs/app-studio-template-sandboxes.md.

package api

import (
	"context"
	"encoding/base64"
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

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	asclient "github.com/faroshq/provider-app-studio/client"
)

// projectRegistryPullSecretName is the tenant Secret (holding a ghcr
// dockerconfigjson) the infrastructure provider bridges into the runtime
// namespace for a production instance. Convention shared with infra:
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

	projectToolPromoteProject = "promote_project"
)

var projectPlatformOwnedProductionFields = map[string]struct{}{
	"name":                       {},
	"farosMode":                  {},
	projectRedeployRevisionField: {},
	"farosCluster":               {},
	"credentialsSecretName":      {},
	// Publishing is the only mutation boundary for template-native access.
	// Promotion preserves the current policy and starts new deployments private.
	accessValueField: {},
}

// Nested platform-owned fields need explicit paths because expose itself is a
// mixed-ownership object: hostnamePrefix is a first-deploy tenant input, while
// fqdn is stamped by the infrastructure controller.
var projectPlatformOwnedProductionPaths = map[string]struct{}{
	"expose.fqdn": {},
}

// projectPromoteRequest is the "Promote to Prod" form submission: optional
// release commit selection plus its server-derived release evidence ID, and
// the template's production inputs (ports, replicas, oidc, …). The instance
// name, farosMode, per-component image fields, and farosRedeployRevision are
// platform-owned and ignored if supplied — name/farosMode are deterministic,
// images come from the selected repository commit's Package evidence, and the
// revision is minted here.
type projectPromoteRequest struct {
	Values    map[string]any `json:"values,omitempty"`
	CommitSHA *string        `json:"commitSHA,omitempty"`
	ReleaseID *string        `json:"releaseID,omitempty"`
	// TemplateName (the preferred wire spelling) selects the production
	// Template independently from the development Template. Template is
	// accepted as a concise alias for callers that use the KRM field name.
	TemplateName      string                                   `json:"templateName,omitempty"`
	Template          string                                   `json:"template,omitempty"`
	ComponentMappings []aiv1alpha1.ProjectComponentMappingSpec `json:"componentMappings,omitempty"`
}

type projectPromoteResponse struct {
	Environment     string                       `json:"environment"`
	Instance        string                       `json:"instance"`
	RolloutRevision string                       `json:"rolloutRevision"`
	CommitSHA       string                       `json:"commitSHA,omitempty"`
	ReleaseID       string                       `json:"releaseID,omitempty"`
	Components      []projectBuildCheckComponent `json:"components,omitempty"`
	Project         json.RawMessage              `json:"project,omitempty"`
}

// newProjectRedeployRevision mints an opaque, non-secret rollout token. It is
// deliberately independent of the production instance identity: re-promoting
// must preserve the instance name/UID while still changing its workload pod
// template.
func newProjectRedeployRevision() string {
	return uuid.NewString()
}

// projectTemplateProdBinding is the compatibility wrapper for callers that
// already have image inputs in the target Template's schema. New promotion
// callers use projectTemplateProdBindingWithMappings so a production Template
// can diverge from the development Template without changing artifact
// identity.
func projectTemplateProdBinding(p *aiv1alpha1.Project, info projectTemplateInfo, images map[string]string, values map[string]any, rolloutRevisions ...string) (aiv1alpha1.ProjectProviderBindingSpec, error) {
	return projectTemplateProdBindingWithMappings(p, info, images, values, nil, rolloutRevisions...)
}

// projectTemplateProdBindingWithMappings builds the production binding: an
// Instance named "<project>-prod", provisioned with farosMode: production,
// the user's production input values, and each target imageInput set to an
// immutable digest. Platform-owned fields (name, farosMode, image inputs, and
// the rollout revision) always win over anything in values.
func projectTemplateProdBindingWithMappings(p *aiv1alpha1.Project, info projectTemplateInfo, images map[string]string, values map[string]any, componentMappings []aiv1alpha1.ProjectComponentMappingSpec, rolloutRevisions ...string) (aiv1alpha1.ProjectProviderBindingSpec, error) {
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
	if projectTemplateSupportsAccess(info) {
		merged[accessValueField] = projectProductionAccessValue(p)
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
		TemplateRef: &aiv1alpha1.ProjectTemplateSpec{
			Name: info.Name,
		},
		ResourceRef: &aiv1alpha1.ProjectProviderResourceReference{
			Name:       name,
			APIVersion: info.APIVersion,
			Kind:       info.Kind,
			Resource:   info.Resource,
		},
		Values:            runtime.RawExtension{Raw: raw},
		ComponentMappings: normalizedProjectComponentMappings(componentMappings),
		Lifecycle: &aiv1alpha1.ProjectBindingLifecycleSpec{
			DeletionPolicy: projectTemplateDeletionPolicy(info),
		},
	}, nil
}

func projectTemplateDeletionPolicy(info projectTemplateInfo) aiv1alpha1.ProjectBindingDeletionPolicy {
	if strings.EqualFold(strings.TrimSpace(info.DefaultDeletionPolicy), "Retain") {
		return aiv1alpha1.ProjectBindingDeletionPolicyRetain
	}
	return aiv1alpha1.ProjectBindingDeletionPolicyDelete
}

func normalizedProjectComponentMappings(mappings []aiv1alpha1.ProjectComponentMappingSpec) []aiv1alpha1.ProjectComponentMappingSpec {
	if len(mappings) == 0 {
		return nil
	}
	out := append([]aiv1alpha1.ProjectComponentMappingSpec(nil), mappings...)
	for i := range out {
		out[i].ComponentRef = strings.TrimSpace(out[i].ComponentRef)
		out[i].TargetComponent = strings.TrimSpace(out[i].TargetComponent)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TargetComponent != out[j].TargetComponent {
			return out[i].TargetComponent < out[j].TargetComponent
		}
		return out[i].ComponentRef < out[j].ComponentRef
	})
	return out
}

// projectTemplateImageInputs returns the launchable production components and
// their image-input fields. Most Templates declare imageInput on each
// development component. A single-component legacy Template such as worker
// may omit it, so the conventional top-level "image" field is inferred only in
// that unambiguous case.
func projectTemplateImageInputs(info projectTemplateInfo) map[string]string {
	out := make(map[string]string)
	for name := range info.Components {
		if imageInput := projectTemplateImageInputForComponent(info, name); imageInput != "" {
			out[name] = imageInput
		}
	}
	return out
}

func projectTemplateSchemaImageInputs(info projectTemplateInfo) []string {
	properties, _ := info.ProductionSchema["properties"].(map[string]any)
	inputs := make([]string, 0)
	for name, raw := range properties {
		if !strings.EqualFold(name, "image") && !strings.HasSuffix(strings.ToLower(name), "image") {
			continue
		}
		field, _ := raw.(map[string]any)
		description, _ := field["description"].(string)
		if strings.HasPrefix(strings.TrimSpace(description), "Computed by the platform") {
			continue
		}
		inputs = append(inputs, name)
	}
	sort.Strings(inputs)
	return inputs
}

// projectProductionComponentMappings resolves stable Project components to the
// selected production Template. Explicit mappings are required whenever names
// or source paths are not enough to identify a target. Existing production
// binding mappings are reused on re-promote; otherwise name/path inference is
// deliberately narrow and fails closed when ambiguous.
func projectProductionComponentMappings(p *aiv1alpha1.Project, info projectTemplateInfo, build []projectBuildCheckComponent, requested []aiv1alpha1.ProjectComponentMappingSpec) ([]aiv1alpha1.ProjectComponentMappingSpec, map[string]string, error) {
	requested = normalizedProjectComponentMappings(requested)
	targetInputs := projectTemplateImageInputs(info)
	if len(targetInputs) == 0 && len(requested) > 0 {
		for _, mapping := range requested {
			if input := projectTemplateImageInputForComponent(info, mapping.TargetComponent); input != "" {
				targetInputs[mapping.TargetComponent] = input
			}
		}
	}
	if len(targetInputs) == 0 && len(info.Components) == 0 && len(requested) == 0 && len(build) == 1 {
		// A production-only single-image Template has no component metadata. Its
		// stable source component is the only safe target identity available.
		if inputs := projectTemplateSchemaImageInputs(info); len(inputs) == 1 {
			targetInputs[build[0].Name] = inputs[0]
		}
	}
	if len(targetInputs) == 0 {
		return nil, nil, newValidationError(fmt.Sprintf("production Template %q declares no launchable image inputs", info.Name))
	}
	seenInputs := make(map[string]string, len(targetInputs))
	for targetName, imageInput := range targetInputs {
		imageInput = strings.TrimSpace(imageInput)
		if imageInput == "" {
			return nil, nil, newValidationError(fmt.Sprintf("production Template component %q has an empty image input", targetName))
		}
		if priorTarget, found := seenInputs[imageInput]; found {
			return nil, nil, newValidationError(fmt.Sprintf("production Template components %q and %q share image input %q", priorTarget, targetName, imageInput))
		}
		seenInputs[imageInput] = targetName
	}

	// A re-promotion with no mapping payload keeps the binding's established
	// mapping when it is still targeting this Template.
	if len(requested) == 0 {
		if existing := findProjectProductionBinding(p); existing != nil && existing.TemplateRef != nil && strings.TrimSpace(existing.TemplateRef.Name) == strings.TrimSpace(info.Name) {
			requested = normalizedProjectComponentMappings(existing.ComponentMappings)
		}
	}

	buildByName := make(map[string]projectBuildCheckComponent, len(build))
	for _, component := range build {
		buildByName[strings.TrimSpace(component.Name)] = component
	}
	projectByName := make(map[string]aiv1alpha1.ProjectComponentSpec)
	if p != nil {
		for _, component := range p.Spec.Components {
			projectByName[strings.TrimSpace(component.Name)] = component
		}
	}

	resolved := make(map[string]string, len(targetInputs))
	if len(requested) > 0 {
		seenProjects := map[string]struct{}{}
		seenTargets := map[string]struct{}{}
		for _, mapping := range requested {
			projectName := strings.TrimSpace(mapping.ComponentRef)
			targetName := strings.TrimSpace(mapping.TargetComponent)
			if projectName == "" || targetName == "" {
				return nil, nil, newValidationError("component mappings require componentRef and targetComponent")
			}
			if _, duplicate := seenProjects[projectName]; duplicate {
				return nil, nil, newValidationError(fmt.Sprintf("project component %q is mapped more than once", projectName))
			}
			if _, duplicate := seenTargets[targetName]; duplicate {
				return nil, nil, newValidationError(fmt.Sprintf("production Template component %q is mapped more than once", targetName))
			}
			if _, found := targetInputs[targetName]; !found {
				return nil, nil, newValidationError(fmt.Sprintf("production Template component %q has no usable image input", targetName))
			}
			if len(projectByName) > 0 {
				if _, found := projectByName[projectName]; !found {
					return nil, nil, newValidationError(fmt.Sprintf("component mapping references unknown Project component %q", projectName))
				}
			}
			if _, found := buildByName[projectName]; !found {
				return nil, nil, newValidationError(fmt.Sprintf("component mapping references Project component %q without build evidence", projectName))
			}
			seenProjects[projectName] = struct{}{}
			seenTargets[targetName] = struct{}{}
			resolved[targetName] = projectName
		}
	} else {
		for targetName := range targetInputs {
			sourceName := ""
			if _, found := buildByName[targetName]; found {
				sourceName = targetName
			} else if component, found := info.Components[targetName]; found && len(projectByName) > 0 {
				for projectName, projectComponent := range projectByName {
					if strings.TrimSpace(projectComponent.SourcePath) == strings.TrimSpace(component.WorkspacePath) {
						if sourceName != "" {
							return nil, nil, newValidationError(fmt.Sprintf("production Template component %q matches multiple Project source paths", targetName))
						}
						sourceName = projectName
					}
				}
			}
			if sourceName == "" && len(targetInputs) == 1 && len(buildByName) == 1 {
				for projectName := range buildByName {
					sourceName = projectName
				}
			}
			if sourceName == "" {
				return nil, nil, newValidationError(fmt.Sprintf("production Template component %q needs an explicit component mapping", targetName))
			}
			resolved[targetName] = sourceName
		}
	}

	if len(resolved) != len(targetInputs) {
		return nil, nil, newValidationError("every launchable production Template component must be mapped to a built Project component")
	}
	mappings := make([]aiv1alpha1.ProjectComponentMappingSpec, 0, len(resolved))
	images := make(map[string]string, len(resolved))
	for targetName, projectName := range resolved {
		row, found := buildByName[projectName]
		if !found || !row.Built || strings.TrimSpace(row.Image) == "" {
			return nil, nil, newValidationError(fmt.Sprintf("Project component %q has no immutable image evidence", projectName))
		}
		mappings = append(mappings, aiv1alpha1.ProjectComponentMappingSpec{ComponentRef: projectName, TargetComponent: targetName})
		images[targetInputs[targetName]] = strings.TrimSpace(row.Image)
	}
	return normalizedProjectComponentMappings(mappings), images, nil
}

func projectTemplateSupportsAccess(info projectTemplateInfo) bool {
	properties, _ := info.ProductionSchema["properties"].(map[string]any)
	_, supported := properties[accessValueField]
	return supported
}

func projectProductionAccessValue(p *aiv1alpha1.Project) string {
	if binding := findProjectProductionBinding(p); binding != nil {
		if values := projectBindingValues(binding); values != nil {
			if access, _ := values[accessValueField].(string); access == accessPublic || access == accessPrivate {
				return access
			}
		}
		// Older production bindings omitted access and were interpreted as public
		// by the runtime. Preserve that policy when redeploying them.
		return accessPublic
	}
	return accessPrivate
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
	previous := projectBindingValues(existing)
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

func projectRequestedRedeployRevision(binding *aiv1alpha1.ProjectProviderBindingSpec) string {
	values := projectBindingValues(binding)
	revision, _ := values[projectRedeployRevisionField].(string)
	return strings.TrimSpace(revision)
}

// promoteProject re-deploys an existing production target from the current
// build evidence. First-time promotion must use the target-aware path with an
// explicit Template; this compatibility wrapper may only reuse a production
// binding already persisted on the Project.
func (s *Server) promoteProject(ctx context.Context, c *asclient.Client, id identity, p *aiv1alpha1.Project, httpReq *http.Request, values map[string]any) (*aiv1alpha1.Project, projectPromoteResponse, error) {
	return s.promoteProjectWithSelectionAndTarget(ctx, c, id, p, httpReq, values, "", false, "", nil)
}

// promoteProjectWithSelection is the common promotion path for latest and
// historical releases. An explicit commit is validated before artifact lookup
// and bypasses the development dirty-workspace guard: its package digests are
// immutable evidence independent of the current sandbox contents.
func (s *Server) promoteProjectWithSelection(ctx context.Context, c *asclient.Client, id identity, p *aiv1alpha1.Project, httpReq *http.Request, values map[string]any, selectedCommitSHA string, commitSelected bool, selectedReleaseIDs ...string) (*aiv1alpha1.Project, projectPromoteResponse, error) {
	return s.promoteProjectWithSelectionAndTarget(ctx, c, id, p, httpReq, values, selectedCommitSHA, commitSelected, "", nil, selectedReleaseIDs...)
}

// promoteProjectWithSelectionAndTarget is the target-aware promotion path.
// The target Template is independent from the development Template. An empty
// target may reuse an existing production binding, but it never infers the
// development Template.
func (s *Server) promoteProjectWithSelectionAndTarget(ctx context.Context, c *asclient.Client, id identity, p *aiv1alpha1.Project, httpReq *http.Request, values map[string]any, selectedCommitSHA string, commitSelected bool, targetTemplate string, componentMappings []aiv1alpha1.ProjectComponentMappingSpec, selectedReleaseIDs ...string) (*aiv1alpha1.Project, projectPromoteResponse, error) {
	releaseIDEvidenceProvided := len(selectedReleaseIDs) > 0
	selectedReleaseID := ""
	if releaseIDEvidenceProvided {
		selectedReleaseID = strings.TrimSpace(selectedReleaseIDs[0])
	}
	if commitSelected && !releaseIDEvidenceProvided {
		return nil, projectPromoteResponse{}, newValidationError("releaseID is required when selecting a commit; refresh release history and retry")
	}
	if releaseIDEvidenceProvided && !commitSelected {
		return nil, projectPromoteResponse{}, newValidationError("releaseID requires commitSHA")
	}
	if p == nil {
		return nil, projectPromoteResponse{}, newValidationError("project is required")
	}
	targetTemplate, err := projectProductionTemplateName(p, targetTemplate)
	if err != nil {
		return nil, projectPromoteResponse{}, err
	}

	// The digest tether (vibe promote semantics): refuse to ship while the
	// workspace holds uncommitted changes — the built images were made from
	// git, and promoting over a dirty workspace would run production on code
	// the user is no longer looking at. The Project reconciler's commit
	// convergence clears this on its own once the project is idle.
	if !commitSelected && s.workspaces != nil {
		if dirty, err := s.workspaces.UncommittedPaths(ctx, projectWorkspaceScope(id, p)); err == nil && len(dirty) > 0 {
			return nil, projectPromoteResponse{}, newValidationError(fmt.Sprintf(
				"the workspace has %d uncommitted file(s); commit them (or wait for the automatic sync) and rebuild before promoting", len(dirty)))
		}
	}

	var check projectBuildCheckResult
	if commitSelected {
		selectedCommitSHA = strings.TrimSpace(selectedCommitSHA)
		if _, err = projectRepositoryCommitForSHA(ctx, c, projectLinkedRepositoryRef(p), selectedCommitSHA); err != nil {
			return nil, projectPromoteResponse{}, err
		}
		check, err = s.checkProjectBuildAtCommit(ctx, c, p, selectedCommitSHA)
	} else {
		check, err = s.checkProjectBuild(ctx, c, id, p)
	}
	if err != nil {
		return nil, projectPromoteResponse{}, err
	}
	if check.Status != "built" {
		return nil, projectPromoteResponse{}, newValidationError("project is not ready to promote: " + check.Note)
	}

	info, err := fetchProjectTemplate(ctx, c, targetTemplate)
	if err != nil {
		return nil, projectPromoteResponse{}, err
	}

	mappings, images, err := projectProductionComponentMappings(p, info, check.Components, componentMappings)
	if err != nil {
		return nil, projectPromoteResponse{}, err
	}
	if len(images) == 0 {
		return nil, projectPromoteResponse{}, newValidationError("no built component images recorded for this project")
	}
	releaseID := projectReleaseID(projectLinkedRepositoryRef(p), check.CommitSHA, check.Components)
	if releaseIDEvidenceProvided && (releaseID == "" || selectedReleaseID != releaseID) {
		return nil, projectPromoteResponse{}, newValidationError("release evidence is stale or does not match the current immutable package digests; refresh release history and retry")
	}

	rolloutRevision := newProjectRedeployRevision()
	promotionValues := projectPromotionValues(p, values)
	binding, err := projectTemplateProdBindingWithMappings(p, info, images, promotionValues, mappings, rolloutRevision)
	if err != nil {
		return nil, projectPromoteResponse{}, err
	}

	next := p.DeepCopy()
	upsertProjectProductionBinding(next, binding)

	// Mint a ghcr image-pull credential (from the Code connection's token) as a
	// tenant Secret so the infrastructure provider can bridge it into the
	// runtime namespace — production images are private packages the runtime
	// cluster cannot otherwise pull. Best-effort: a public image needs none, so
	// a failure here must not block promotion.
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
		CommitSHA:       check.CommitSHA,
		ReleaseID:       releaseID,
		Components:      check.Components,
		Project:         raw,
	}, nil
}

func projectProductionTemplateName(p *aiv1alpha1.Project, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		return requested, nil
	}
	if binding := findProjectProductionBinding(p); binding != nil {
		if binding.TemplateRef != nil {
			name := strings.TrimSpace(binding.TemplateRef.Name)
			if name == "" {
				return "", newValidationError("production binding has an empty templateRef")
			}
			return name, nil
		}
	}
	return "", newValidationError("production template is required; select a target Template before promoting")
}

// projectPromotionValues starts from the current production binding and
// overlays the caller's optional form values. This keeps re-promotions (and
// historical release selection) from silently resetting production settings
// when the request only changes the release commit.
func projectPromotionValues(p *aiv1alpha1.Project, values map[string]any) map[string]any {
	base := map[string]any{}
	if binding := findProjectProductionBinding(p); binding != nil {
		if existing := projectBindingValues(binding); existing != nil {
			base = cloneProjectPromotionObject(existing)
		}
	}
	return mergeProjectPromotionObject(base, values)
}

func cloneProjectPromotionObject(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(value))
	for key, item := range value {
		if object, ok := item.(map[string]any); ok {
			cloned[key] = cloneProjectPromotionObject(object)
			continue
		}
		if list, ok := item.([]any); ok {
			clonedList := make([]any, len(list))
			for i, listItem := range list {
				if object, ok := listItem.(map[string]any); ok {
					clonedList[i] = cloneProjectPromotionObject(object)
				} else {
					clonedList[i] = listItem
				}
			}
			cloned[key] = clonedList
			continue
		}
		cloned[key] = item
	}
	return cloned
}

func mergeProjectPromotionObject(base, overlay map[string]any) map[string]any {
	if base == nil {
		base = map[string]any{}
	}
	for key, value := range overlay {
		if object, ok := value.(map[string]any); ok {
			prior, _ := base[key].(map[string]any)
			base[key] = mergeProjectPromotionObject(cloneProjectPromotionObject(prior), object)
			continue
		}
		base[key] = value
	}
	return base
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
			if strings.TrimSpace(b.Name) == projectProductionBindingName && b.Kind != aiv1alpha1.ProjectBindingKindProviderReference {
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
	commitSelected := req.CommitSHA != nil
	commitSHA := ""
	if req.CommitSHA != nil {
		commitSHA = *req.CommitSHA
	}
	var releaseIDs []string
	if req.ReleaseID != nil {
		releaseIDs = []string{*req.ReleaseID}
	}
	targetTemplate := strings.TrimSpace(req.TemplateName)
	if alias := strings.TrimSpace(req.Template); alias != "" {
		if targetTemplate != "" && targetTemplate != alias {
			writeStatus(w, http.StatusBadRequest, "BadRequest", "templateName and template must identify the same production Template")
			return
		}
		targetTemplate = alias
	}
	_, resp, err := s.promoteProjectWithSelectionAndTarget(r.Context(), c, id, p, r, req.Values, commitSHA, commitSelected, targetTemplate, req.ComponentMappings, releaseIDs...)
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
	Template                  string                                   `json:"template,omitempty"`
	Instance                  string                                   `json:"instance,omitempty"`
	TargetComponents          []projectProductionTemplateComponentView `json:"targetComponents,omitempty"`
	ComponentMappings         []aiv1alpha1.ProjectComponentMappingSpec `json:"componentMappings,omitempty"`
	ProductionSchema          map[string]any                           `json:"productionSchema,omitempty"`
	ImmutableProductionInputs []string                                 `json:"immutableProductionInputs,omitempty"`
	ProductionValues          map[string]any                           `json:"productionValues,omitempty"`
	RequestedRolloutRevision  string                                   `json:"requestedRolloutRevision,omitempty"`
	ObservedRolloutRevision   string                                   `json:"observedRolloutRevision,omitempty"`
	Promotable                bool                                     `json:"promotable"`
	Build                     projectBuildCheckResult                  `json:"build"`
	// Production reports the live production environment when the project has
	// been promoted at least once: its phase and, once serving, its URL. Nil
	// when the project has never been promoted.
	Production *aiv1alpha1.ProjectProviderBindingStatus `json:"production,omitempty"`
}

// findProjectProductionBinding returns the project's production binding spec,
// or nil when it has never been promoted.
func findProjectProductionBinding(p *aiv1alpha1.Project) *aiv1alpha1.ProjectProviderBindingSpec {
	if p == nil {
		return nil
	}
	for i := range p.Spec.Environments {
		env := &p.Spec.Environments[i]
		if strings.TrimSpace(env.Name) != projectProductionEnvironmentName {
			continue
		}
		for j := range env.Bindings {
			if strings.TrimSpace(env.Bindings[j].Name) == projectProductionBindingName && env.Bindings[j].Kind != aiv1alpha1.ProjectBindingKindProviderReference {
				return &env.Bindings[j]
			}
		}
	}
	return nil
}

func projectPromotionTemplateName(p *aiv1alpha1.Project, requested string) string {
	if requested = strings.TrimSpace(requested); requested != "" {
		return requested
	}
	if production := findProjectProductionBinding(p); production != nil && production.TemplateRef != nil {
		return strings.TrimSpace(production.TemplateRef.Name)
	}
	// Development runtime selection is intentionally not a production target.
	// Universal Projects have no development Template at all, and ordinary
	// Template-backed Projects must still make the production choice explicit.
	return ""
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
	template := projectPromotionTemplateName(p, r.URL.Query().Get("templateName"))
	productionBinding := findProjectProductionBinding(p)
	resp := projectPromotionReadinessResponse{
		Template:   template,
		Instance:   projectTemplateProdInstanceName(p),
		Promotable: template != "" && check.Status == "built",
		Build:      check,
	}
	if template != "" {
		info, templateErr := fetchProjectTemplate(r.Context(), c, template)
		if templateErr != nil {
			writeProjectPromoteError(w, templateErr)
			return
		}
		resp.TargetComponents = projectProductionTemplateComponents(info)
		resp.ProductionSchema = info.ProductionSchema
		resp.ImmutableProductionInputs = projectProductionImmutableInputPaths(info)
	}
	// CI explains absent artifacts but never overrides the Package-based gate.
	// Keep lookup failure additive so a registry-ready release stays deployable.
	resp.Build.Run, resp.Build.RunError = s.observeProjectBuildRun(r.Context(), id, p, r, check.CommitSHA)
	// Artifact-mode (production) environments are not reported by the live
	// (development) environment status surface, so read the production
	// binding's status directly for its phase and serving URL.
	if prod := productionBinding; prod != nil {
		selectedExistingTarget := prod.TemplateRef != nil && strings.TrimSpace(prod.TemplateRef.Name) == template
		if selectedExistingTarget {
			resp.ProductionValues = projectBindingValues(prod)
			resp.ComponentMappings = normalizedProjectComponentMappings(prod.ComponentMappings)
		}
		resp.RequestedRolloutRevision = projectRequestedRedeployRevision(prod)
		st := projectProviderBindingStatus(r.Context(), c, p, *prod, id)
		resp.Production = &st
		// Read the provider instance's spec, not the desired Project binding,
		// so clients can distinguish the old Ready deployment from a rollout
		// revision the Project controller has actually delivered downstream.
		if instance, observeErr := observeProjectProviderBinding(r.Context(), c, p, *prod, id); observeErr == nil {
			resp.ObservedRolloutRevision = projectObservedRedeployRevision(instance)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func projectObservedRedeployRevision(instance *unstructured.Unstructured) string {
	if instance == nil {
		return ""
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
