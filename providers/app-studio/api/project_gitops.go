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

package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	"github.com/faroshq/provider-app-studio/bindings"
	asclient "github.com/faroshq/provider-app-studio/client"
	"github.com/faroshq/provider-app-studio/tenant"
	"github.com/faroshq/provider-app-studio/workspace"
)

const (
	projectGitOpsEnvironmentName = "configuration"
	projectGitOpsBindingName     = "gitops"
	projectGitOpsProvider        = "deployments"
	projectGitOpsPath            = ".faros"
	projectGitOpsDefaultBranch   = "main"

	projectRepositorySyncAPIVersion = "deployments.faros.sh/v1alpha1"
	projectRepositorySyncKind       = "RepositorySync"
	projectRepositorySyncResource   = "repositorysyncs"
	projectChangeRequestAPIVersion  = "code.faros.sh/v1alpha1"
	projectChangeRequestKind        = "ChangeRequest"
	projectChangeRequestResource    = "changerequests"
)

var projectChangeRequestGVR = schema.GroupVersionResource{
	Group: "code.faros.sh", Version: "v1alpha1", Resource: projectChangeRequestResource,
}

type projectGitOpsSettings struct {
	Ref               string
	Path              string
	ChangePolicy      aiv1alpha1.ProjectGitOpsChangePolicy
	RequiredApprovals int32
}

func defaultProjectDelivery() *aiv1alpha1.ProjectDeliverySpec {
	return &aiv1alpha1.ProjectDeliverySpec{
		Development: aiv1alpha1.ProjectEnvironmentDeliverySpec{Mode: aiv1alpha1.ProjectDeliveryModeDirect},
		Production:  aiv1alpha1.ProjectEnvironmentDeliverySpec{Mode: aiv1alpha1.ProjectDeliveryModeGitOps},
		GitOps: &aiv1alpha1.ProjectGitOpsDeliverySpec{
			Ref:               projectGitOpsDefaultBranch,
			Path:              projectGitOpsPath,
			ChangePolicy:      aiv1alpha1.ProjectGitOpsChangePolicyPullRequest,
			RequiredApprovals: 1,
		},
	}
}

func directProjectDelivery() *aiv1alpha1.ProjectDeliverySpec {
	return &aiv1alpha1.ProjectDeliverySpec{
		Development: aiv1alpha1.ProjectEnvironmentDeliverySpec{Mode: aiv1alpha1.ProjectDeliveryModeDirect},
		Production:  aiv1alpha1.ProjectEnvironmentDeliverySpec{Mode: aiv1alpha1.ProjectDeliveryModeDirect},
	}
}

func projectDeliveryForCreate(requested *aiv1alpha1.ProjectDeliverySpec, adopted bool) (*aiv1alpha1.ProjectDeliverySpec, error) {
	if requested == nil {
		if adopted {
			return directProjectDelivery(), nil
		}
		return defaultProjectDelivery(), nil
	}
	delivery := requested.DeepCopy()
	for _, environment := range []struct {
		name string
		mode *aiv1alpha1.ProjectDeliveryMode
	}{
		{name: "development", mode: &delivery.Development.Mode},
		{name: "production", mode: &delivery.Production.Mode},
	} {
		*environment.mode = aiv1alpha1.ProjectDeliveryMode(strings.TrimSpace(string(*environment.mode)))
		switch *environment.mode {
		case aiv1alpha1.ProjectDeliveryModeDirect, aiv1alpha1.ProjectDeliveryModeGitOps:
		default:
			return nil, newValidationError(environment.name + " delivery mode must be Direct or GitOps")
		}
	}
	if adopted && projectDeliveryUsesGitOps(delivery) {
		return nil, newValidationError("GitOps delivery is not available when importing an existing repository; create it as Direct for every environment until a bootstrap migration is available")
	}
	if !projectDeliveryUsesGitOps(delivery) {
		delivery.GitOps = nil
		return delivery, nil
	}
	settings := projectGitOpsSettingsFromSpec(delivery.GitOps)
	delivery.GitOps = &aiv1alpha1.ProjectGitOpsDeliverySpec{
		Ref: settings.Ref, Path: settings.Path, ChangePolicy: settings.ChangePolicy,
		RequiredApprovals: settings.RequiredApprovals,
	}
	return delivery, nil
}

func effectiveProjectDeliverySpec(p *aiv1alpha1.Project) aiv1alpha1.ProjectDeliverySpec {
	if p == nil || p.Spec.Delivery == nil {
		return *directProjectDelivery()
	}
	effective := *p.Spec.Delivery.DeepCopy()
	settings := projectGitOpsDeliverySettings(p)
	if projectDeliveryUsesGitOps(&effective) {
		effective.GitOps = &aiv1alpha1.ProjectGitOpsDeliverySpec{
			Ref: settings.Ref, Path: settings.Path, ChangePolicy: settings.ChangePolicy,
			RequiredApprovals: settings.RequiredApprovals,
		}
	} else {
		effective.GitOps = nil
	}
	return effective
}

func projectDeliveryUsesGitOps(delivery *aiv1alpha1.ProjectDeliverySpec) bool {
	return delivery != nil && (delivery.Development.Mode == aiv1alpha1.ProjectDeliveryModeGitOps ||
		delivery.Production.Mode == aiv1alpha1.ProjectDeliveryModeGitOps)
}

func projectDevelopmentDeliveryMode(p *aiv1alpha1.Project) aiv1alpha1.ProjectDeliveryMode {
	return effectiveProjectDeliverySpec(p).Development.Mode
}

func projectProductionDeliveryMode(p *aiv1alpha1.Project) aiv1alpha1.ProjectDeliveryMode {
	return effectiveProjectDeliverySpec(p).Production.Mode
}

func projectHasGitOps(p *aiv1alpha1.Project) bool {
	effective := effectiveProjectDeliverySpec(p)
	return projectDeliveryUsesGitOps(&effective)
}

func projectDevelopmentIsGitManaged(p *aiv1alpha1.Project) bool {
	return projectDevelopmentDeliveryMode(p) == aiv1alpha1.ProjectDeliveryModeGitOps
}

func projectProductionIsGitManaged(p *aiv1alpha1.Project) bool {
	return projectProductionDeliveryMode(p) == aiv1alpha1.ProjectDeliveryModeGitOps
}

func projectGitOpsDeliverySettings(p *aiv1alpha1.Project) projectGitOpsSettings {
	if p == nil || p.Spec.Delivery == nil {
		return projectGitOpsSettingsFromSpec(nil)
	}
	return projectGitOpsSettingsFromSpec(p.Spec.Delivery.GitOps)
}

func projectGitOpsSettingsFromSpec(configured *aiv1alpha1.ProjectGitOpsDeliverySpec) projectGitOpsSettings {
	settings := projectGitOpsSettings{
		Ref:               projectGitOpsDefaultBranch,
		Path:              projectGitOpsPath,
		ChangePolicy:      aiv1alpha1.ProjectGitOpsChangePolicyPullRequest,
		RequiredApprovals: 1,
	}
	if configured == nil {
		return settings
	}
	if value := strings.TrimSpace(configured.Ref); value != "" {
		settings.Ref = value
	}
	if value := strings.Trim(strings.TrimSpace(configured.Path), "/"); value != "" {
		settings.Path = value
	}
	if configured.ChangePolicy != "" {
		settings.ChangePolicy = configured.ChangePolicy
	}
	if configured.RequiredApprovals > 0 {
		settings.RequiredApprovals = configured.RequiredApprovals
	}
	return settings
}

// enableProjectGitOps is a creation helper. Delivery policy, not the generated
// RepositorySync binding, is the durable source of truth for writer selection.
func enableProjectGitOps(p *aiv1alpha1.Project) error {
	if p == nil {
		return nil
	}
	if p.Spec.Repository == nil {
		return fmt.Errorf("project repository is required for GitOps")
	}
	if p.Spec.Repository.Adopted {
		return fmt.Errorf("GitOps delivery for adopted repositories requires an explicit bootstrap migration")
	}
	if p.Spec.Delivery == nil {
		p.Spec.Delivery = defaultProjectDelivery()
	}
	if !projectHasGitOps(p) {
		return fmt.Errorf("cannot create GitOps resources when every environment uses Direct delivery")
	}
	ref := strings.TrimSpace(p.Spec.Repository.RepositoryRef)
	if ref == "" {
		return fmt.Errorf("project repository is required for GitOps")
	}
	settings := projectGitOpsDeliverySettings(p)
	values, err := json.Marshal(map[string]any{
		"repositoryRef":   ref,
		"ref":             settings.Ref,
		"path":            settings.Path,
		"prune":           true,
		"intervalSeconds": int64(30),
	})
	if err != nil {
		return err
	}
	binding := aiv1alpha1.ProjectProviderBindingSpec{
		Name:     projectGitOpsBindingName,
		Provider: projectGitOpsProvider,
		Kind:     aiv1alpha1.ProjectBindingKindProviderResource,
		ResourceRef: &aiv1alpha1.ProjectProviderResourceReference{
			Name:       projectGitOpsResourceName(p),
			APIVersion: projectRepositorySyncAPIVersion,
			Kind:       projectRepositorySyncKind,
			Resource:   projectRepositorySyncResource,
		},
		Values: runtime.RawExtension{Raw: values},
	}
	for i := range p.Spec.Environments {
		environment := &p.Spec.Environments[i]
		if environment.Name != projectGitOpsEnvironmentName {
			continue
		}
		for j := range environment.Bindings {
			if environment.Bindings[j].Name == projectGitOpsBindingName {
				environment.Bindings[j] = binding
				return nil
			}
		}
		environment.Bindings = append(environment.Bindings, binding)
		return nil
	}
	p.Spec.Environments = append(p.Spec.Environments, aiv1alpha1.ProjectEnvironmentSpec{
		Name: projectGitOpsEnvironmentName, Mode: aiv1alpha1.ProjectEnvironmentModeArtifact,
		Bindings: []aiv1alpha1.ProjectProviderBindingSpec{binding},
	})
	return nil
}

func projectGitOpsResourceName(p *aiv1alpha1.Project) string {
	if p == nil {
		return ""
	}
	return dns1123LabelWithSuffix(p.Name, "gitops")
}

func projectGitOpsDevelopmentFiles(p *aiv1alpha1.Project, info projectTemplateInfo) ([]workspace.File, error) {
	if !projectDevelopmentIsGitManaged(p) {
		return nil, nil
	}
	configuration := map[string]any{
		"name":      projectTemplateInstanceName(p),
		"farosMode": "development",
	}
	if len(info.PreviewAccessModes) > 0 {
		configuration["access"] = effectiveProjectPreviewAccess(p.Spec.Sharing.Preview.Mode)
	}
	path := projectGitOpsDeliverySettings(p).Path
	instance := projectGitOpsTargetObject(p, info, projectTemplateInstanceName(p), configuration)
	return gitOpsYAMLFiles(map[string]any{
		path + "/environments/development/instance.yaml": instance,
	})
}

func projectGitOpsProductionFiles(p *aiv1alpha1.Project, info projectTemplateInfo, values map[string]any) ([]map[string]string, string, error) {
	path := projectGitOpsDeliverySettings(p).Path
	instance := projectGitOpsTargetObject(p, info, projectTemplateProdInstanceName(p), values)
	files, err := gitOpsYAMLFiles(map[string]any{
		path + "/environments/production/instance.yaml": instance,
	})
	if err != nil {
		return nil, "", err
	}
	commitFiles := make([]map[string]string, 0, len(files))
	hasher := sha256.New()
	for _, file := range files {
		commitFiles = append(commitFiles, map[string]string{"path": file.Path, "content": file.Content})
		_, _ = hasher.Write([]byte(file.Path))
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write([]byte(file.Content))
	}
	return commitFiles, hex.EncodeToString(hasher.Sum(nil))[:12], nil
}

// projectGitOpsTargetObject is the provider-neutral handoff: App Studio
// authors the concrete object selected by the Template, while RepositorySync
// only validates/applies it. Runtime interpretation and status remain owned by
// the target provider.
func projectGitOpsTargetObject(p *aiv1alpha1.Project, info projectTemplateInfo, name string, values map[string]any) map[string]any {
	return map[string]any{
		"apiVersion": info.APIVersion,
		"kind":       info.Kind,
		"metadata": map[string]any{
			"name": name,
			"labels": map[string]any{
				bindings.ProjectLabel:  p.Name,
				bindings.TemplateLabel: info.Name,
			},
		},
		"spec": map[string]any{
			"template": info.Name,
			"values":   values,
		},
	}
}

// projectGitOpsTargetRevision gives the target manifest a deterministic
// redeploy input before the promotion branch exists. It is derived from the
// reviewed source commit and the complete target intent, so a changed source,
// image digest, or production setting changes the workload revision without
// relying on a target-specific Deployments controller.
func projectGitOpsTargetRevision(p *aiv1alpha1.Project, info projectTemplateInfo, commitSHA string, images map[string]string, values map[string]any) (string, error) {
	targetValues := projectProductionInputValues(info, images, values)
	for imageInput, image := range images {
		targetValues[imageInput] = image
	}
	targetValues["name"] = projectTemplateProdInstanceName(p)
	targetValues["farosMode"] = "production"
	canonical, err := json.Marshal(map[string]any{
		"repositoryRef":  strings.TrimSpace(p.Spec.Repository.RepositoryRef),
		"sourceRevision": strings.TrimSpace(commitSHA),
		"target": map[string]any{
			"apiVersion": info.APIVersion,
			"kind":       info.Kind,
			"template":   info.Name,
		},
		"values": targetValues,
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return "git-" + hex.EncodeToString(digest[:])[:16], nil
}

func gitOpsYAMLFiles(objects map[string]any) ([]workspace.File, error) {
	paths := make([]string, 0, len(objects))
	for path := range objects {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	files := make([]workspace.File, 0, len(paths))
	for _, path := range paths {
		body, err := yaml.Marshal(objects[path])
		if err != nil {
			return nil, fmt.Errorf("marshal %s: %w", path, err)
		}
		files = append(files, workspace.File{Path: path, Content: string(body)})
	}
	return files, nil
}

func projectGitOpsProductionBinding(p *aiv1alpha1.Project, info projectTemplateInfo, values map[string]any) (aiv1alpha1.ProjectProviderBindingSpec, error) {
	raw, err := json.Marshal(values)
	if err != nil {
		return aiv1alpha1.ProjectProviderBindingSpec{}, err
	}
	return aiv1alpha1.ProjectProviderBindingSpec{
		Name:     projectProductionBindingName,
		Provider: projectInfrastructureProvider,
		Kind:     aiv1alpha1.ProjectBindingKindProviderReference,
		ResourceRef: &aiv1alpha1.ProjectProviderResourceReference{
			Name:       projectTemplateProdInstanceName(p),
			APIVersion: info.APIVersion,
			Kind:       info.Kind,
			Resource:   info.Resource,
		},
		// Values are an observed desired-state snapshot for the App Studio
		// form. providerReference means the Project reconciler never writes it;
		// RepositorySync applies the exact object committed to Git.
		Values: runtime.RawExtension{Raw: raw},
	}, nil
}

func (s *Server) proposeProjectGitOpsPromotion(ctx context.Context, c *asclient.Client, id identity, p *aiv1alpha1.Project, httpReq *http.Request, info projectTemplateInfo, values map[string]any) (aiv1alpha1.ProjectProviderBindingSpec, projectGitOpsPromotionView, error) {
	settings := projectGitOpsDeliverySettings(p)
	if settings.ChangePolicy != aiv1alpha1.ProjectGitOpsChangePolicyPullRequest {
		return aiv1alpha1.ProjectProviderBindingSpec{}, projectGitOpsPromotionView{}, fmt.Errorf("unsupported GitOps change policy %q", settings.ChangePolicy)
	}
	files, digest, err := projectGitOpsProductionFiles(p, info, values)
	if err != nil {
		return aiv1alpha1.ProjectProviderBindingSpec{}, projectGitOpsPromotionView{}, err
	}
	branch := "faros/promote-" + p.Name + "-" + digest[:6]
	baseBranch := projectRepositoryDefaultBranch(ctx, c, p)
	args := map[string]any{
		"repositoryRef": p.Spec.Repository.RepositoryRef,
		"files":         files,
		"message":       "Promote " + p.Name + " to production",
		"branch":        branch,
		"baseRef":       baseBranch,
	}
	if httpReq == nil {
		return aiv1alpha1.ProjectProviderBindingSpec{}, projectGitOpsPromotionView{}, fmt.Errorf("GitOps promotion requires an authenticated request")
	}
	if _, err := callProjectMCPTool(ctx, s.mcpEndpoint(id.clusterID), httpReq, id.tenantPath, s.mcpInsecureSkipTLSVerify, projectToolCodeCommitFiles, args); err != nil {
		return aiv1alpha1.ProjectProviderBindingSpec{}, projectGitOpsPromotionView{}, fmt.Errorf("commit production configuration: %w", err)
	}
	crName := dns1123LabelWithSuffix(p.Name, "prod-"+digest[:10])
	changeRequest := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": projectChangeRequestAPIVersion,
		"kind":       projectChangeRequestKind,
		"metadata": map[string]any{
			"name":   crName,
			"labels": map[string]any{"app-studio.ai.faros.sh/project": p.Name},
		},
		"spec": map[string]any{
			"repositoryRef":     p.Spec.Repository.RepositoryRef,
			"baseBranch":        baseBranch,
			"headBranch":        branch,
			"title":             "Promote " + p.Spec.DisplayName + " to production",
			"body":              "Generated by App Studio. Merge this change to deploy the reviewed release.",
			"mergePolicy":       "AfterApproval",
			"requiredApprovals": int64(settings.RequiredApprovals),
		},
	}}
	resource := c.Resource(tenant.Resource{GVR: projectChangeRequestGVR, Kind: projectChangeRequestKind, Plural: "ChangeRequests"}, "")
	existing, getErr := resource.Get(ctx, crName, metav1.GetOptions{})
	if apierrors.IsNotFound(getErr) {
		if _, err := resource.Create(ctx, changeRequest, metav1.CreateOptions{}); err != nil && !apierrors.IsAlreadyExists(err) {
			return aiv1alpha1.ProjectProviderBindingSpec{}, projectGitOpsPromotionView{}, err
		}
	} else if getErr != nil {
		return aiv1alpha1.ProjectProviderBindingSpec{}, projectGitOpsPromotionView{}, getErr
	} else {
		observed, _, _ := unstructured.NestedMap(existing.Object, "spec")
		desired, _, _ := unstructured.NestedMap(changeRequest.Object, "spec")
		if !mapsEqualJSON(observed, desired) {
			return aiv1alpha1.ProjectProviderBindingSpec{}, projectGitOpsPromotionView{}, fmt.Errorf("ChangeRequest %q already exists with different contents", crName)
		}
	}
	binding, err := projectGitOpsProductionBinding(p, info, values)
	return binding, projectGitOpsPromotionView{
		Phase: "PendingApproval", ChangeRequest: crName, Branch: branch,
	}, err
}

func projectRepositoryDefaultBranch(ctx context.Context, c *asclient.Client, p *aiv1alpha1.Project) string {
	if projectHasGitOps(p) {
		return projectGitOpsDeliverySettings(p).Ref
	}
	if c == nil || p == nil || p.Spec.Repository == nil {
		return projectGitOpsDefaultBranch
	}
	repositoryRef := strings.TrimSpace(p.Spec.Repository.RepositoryRef)
	if repositoryRef == "" {
		return projectGitOpsDefaultBranch
	}
	repository, err := c.Resource(codeRepositoryResource, "").Get(ctx, repositoryRef, metav1.GetOptions{})
	if err != nil {
		return projectGitOpsDefaultBranch
	}
	for _, path := range [][]string{{"status", "defaultBranch"}, {"spec", "defaultBranch"}} {
		branch, found, nestedErr := unstructured.NestedString(repository.Object, path...)
		if nestedErr == nil && found && strings.TrimSpace(branch) != "" {
			return strings.TrimSpace(branch)
		}
	}
	return projectGitOpsDefaultBranch
}

func mapsEqualJSON(a, b map[string]any) bool {
	aRaw, _ := json.Marshal(a)
	bRaw, _ := json.Marshal(b)
	return string(aRaw) == string(bRaw)
}
