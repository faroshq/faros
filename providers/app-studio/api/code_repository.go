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
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	asclient "github.com/faroshq/provider-app-studio/client"
	"github.com/faroshq/provider-app-studio/tenant"
)

const (
	codeAPIGroup   = "code.faros.sh"
	codeAPIVersion = "v1alpha1"

	codeConditionReady     = "Ready"
	codeConditionValidated = "Validated"
	codeLabelRepository    = "code.faros.sh/repository"

	projectRepositoryProjectAnnotation = "app-studio.ai.faros.sh/project"

	projectRepositoryProjectLabel = "app-studio.ai.faros.sh/project"

	// projectRepositoryAdoptedAnnotation marks a Repository App Studio
	// adopted (repository import) rather than created — deleting the project
	// releases the claim but never deletes an adopted repository.
	projectRepositoryAdoptedAnnotation = "app-studio.ai.faros.sh/adopted"

	projectRepositoryStatusReady             = "Ready"
	projectRepositoryStatusProvisioning      = "Provisioning"
	projectRepositoryStatusFailed            = "Failed"
	projectRepositoryStatusRepositoryMissing = "RepositoryMissing"
	projectRepositoryStatusConnectionMissing = "ConnectionMissing"
	projectRepositoryStatusUnavailable       = "Unavailable"
)

var (
	codeSchemeGroupVersion   = schema.GroupVersion{Group: codeAPIGroup, Version: codeAPIVersion}
	codeConnectionsGVR       = codeSchemeGroupVersion.WithResource("connections")
	codeRepositoriesGVR      = codeSchemeGroupVersion.WithResource("repositories")
	codeRepositoryCommitsGVR = codeSchemeGroupVersion.WithResource("repositorycommits")
	codePackagesGVR          = codeSchemeGroupVersion.WithResource("packages")

	apiBindingAPIGroup   = "apis.kcp.io"
	apiBindingAPIVersion = "v1alpha2"
)

var apiBindingsResource = tenant.Resource{
	GVR:        schema.GroupVersion{Group: apiBindingAPIGroup, Version: apiBindingAPIVersion}.WithResource("apibindings"),
	Kind:       "APIBinding",
	Plural:     "APIBindings",
	Namespaced: false,
}

const (
	appStudioAPIExportName   = "ai.faros.sh"
	deploymentsAPIExportName = "deployments.faros.sh"
	appStudioAPIExportPath   = "root:faros:providers:app-studio"
	deploymentsAPIExportPath = "root:faros:providers:deployments"
)

type projectGitOpsRequiredClaim struct {
	Group    string
	Resource string
}

var (
	// Deployments fetches the desired-state directory through Code and applies
	// the concrete target objects. Infrastructure is one supported target, not
	// a deployment backend encoded into RepositorySync.
	deploymentsGitOpsClaims = []projectGitOpsRequiredClaim{
		{Group: "code.faros.sh", Resource: "repositorycheckouts"},
		{Group: "infrastructure.faros.sh", Resource: "instances"},
	}
	// App Studio creates the RepositorySync at project creation and later reads
	// the concrete target object's state during promotion and convergence.
	// Its existing Code and Infrastructure claims are included too: a GitOps
	// project still has to create its Repository and reconcile development
	// instances before Deployments can consume the source contract.
	appStudioGitOpsClaims = []projectGitOpsRequiredClaim{
		{Group: "code.faros.sh", Resource: "repositories"},
		{Group: "code.faros.sh", Resource: "changerequests"},
		{Group: "infrastructure.faros.sh", Resource: "instances"},
		{Group: "deployments.faros.sh", Resource: "repositorysyncs"},
	}
)

type projectRepositoryPlan struct {
	Ref           string
	Name          string
	ConnectionRef string
	Description   string

	// Adopted marks a plan built from an EXISTING Repository CR (repository
	// import): creation claims it instead of creating one, and cleanup
	// releases the claim instead of deleting the repository.
	Adopted bool
}

type ProjectCreateReadinessView struct {
	GitConnection ProjectCreateGitConnectionReadiness `json:"gitConnection"`
	GitOps        ProjectCreateGitOpsReadiness        `json:"gitOps"`
}

type ProjectCreateGitConnectionReadiness struct {
	Ready         bool   `json:"ready"`
	ConnectionRef string `json:"connectionRef,omitempty"`
	Message       string `json:"message,omitempty"`
}

// ProjectCreateGitOpsReadiness is the authoritative tenant capability check
// for reviewed production. It intentionally reports access in addition to
// provider process health: a running Deployments provider is not sufficient if
// the tenant APIBindings have not accepted and applied its claims.
type ProjectCreateGitOpsReadiness struct {
	Available   bool                                  `json:"available"`
	Reason      string                                `json:"reason,omitempty"`
	Message     string                                `json:"message,omitempty"`
	Deployments ProjectCreateProviderBindingReadiness `json:"deployments"`
	AppStudio   ProjectCreateProviderBindingReadiness `json:"appStudio"`
}

type ProjectCreateProviderBindingReadiness struct {
	Bound         bool     `json:"bound"`
	Ready         bool     `json:"ready"`
	MissingClaims []string `json:"missingClaims,omitempty"`
}

type codeResourceGetter func(ctx context.Context, gvr schema.GroupVersionResource, name string) (*unstructured.Unstructured, error)
type codeResourceLister func(ctx context.Context, gvr schema.GroupVersionResource, opts metav1.ListOptions) (*unstructured.UnstructuredList, error)

func (p projectRepositoryPlan) projectBinding() *aiv1alpha1.ProjectRepositoryBinding {
	return &aiv1alpha1.ProjectRepositoryBinding{
		RepositoryRef: p.Ref,
		Name:          p.Name,
		ConnectionRef: p.ConnectionRef,
		Adopted:       p.Adopted,
	}
}

func (s *Server) prepareProjectRepository(ctx context.Context, c *asclient.Client, requestedConnection, requestedRepoName, displayName, description string) (projectRepositoryPlan, error) {
	connectionRef, err := selectCodeConnection(ctx, c, requestedConnection)
	if err != nil {
		return projectRepositoryPlan{}, err
	}
	repoName, err := repositoryName(ctx, c, requestedRepoName, displayName)
	if err != nil {
		return projectRepositoryPlan{}, err
	}
	if strings.TrimSpace(description) == "" {
		description = "Generated by App Studio for " + displayName
	}
	return projectRepositoryPlan{
		Ref:           repoName,
		Name:          repoName,
		ConnectionRef: connectionRef,
		Description:   description,
	}, nil
}

// adoptProjectRepository builds a repository plan from an EXISTING Repository
// CR (repository import). The repository must not already back another App
// Studio project.
func adoptProjectRepository(ctx context.Context, c *asclient.Client, repositoryRef string) (projectRepositoryPlan, error) {
	repositoryRef = strings.TrimSpace(repositoryRef)
	if repositoryRef == "" {
		return projectRepositoryPlan{}, newValidationError("existingRepositoryRef is empty")
	}
	repo, err := c.Resource(codeRepositoryResource, "").Get(ctx, repositoryRef, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return projectRepositoryPlan{}, newValidationError(fmt.Sprintf("Code repository %q not found", repositoryRef))
		}
		return projectRepositoryPlan{}, codeProviderRequestError("get Code repository", err)
	}
	if claimedBy := strings.TrimSpace(repo.GetLabels()[projectRepositoryProjectLabel]); claimedBy != "" {
		return projectRepositoryPlan{}, newValidationError(fmt.Sprintf("Code repository %q already backs App Studio project %q", repositoryRef, claimedBy))
	}
	name, _, _ := unstructured.NestedString(repo.Object, "spec", "name")
	connectionRef, _, _ := unstructured.NestedString(repo.Object, "spec", "connectionRef")
	if strings.TrimSpace(connectionRef) == "" {
		return projectRepositoryPlan{}, newValidationError(fmt.Sprintf("Code repository %q has no connectionRef", repositoryRef))
	}
	if strings.TrimSpace(name) == "" {
		name = repositoryRef
	}
	return projectRepositoryPlan{
		Ref:           repositoryRef,
		Name:          strings.TrimSpace(name),
		ConnectionRef: strings.TrimSpace(connectionRef),
		Adopted:       true,
	}, nil
}

// claimProjectRepository stamps the project claim onto an adopted Repository.
func claimProjectRepository(ctx context.Context, c *asclient.Client, projectName string, plan projectRepositoryPlan) error {
	repo, err := c.Resource(codeRepositoryResource, "").Get(ctx, plan.Ref, metav1.GetOptions{})
	if err != nil {
		return codeProviderRequestError("get Code repository", err)
	}
	if claimedBy := strings.TrimSpace(repo.GetLabels()[projectRepositoryProjectLabel]); claimedBy != "" && claimedBy != projectName {
		return newValidationError(fmt.Sprintf("Code repository %q already backs App Studio project %q", plan.Ref, claimedBy))
	}
	labels := repo.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[projectRepositoryProjectLabel] = projectName
	repo.SetLabels(labels)
	annotations := repo.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[projectRepositoryProjectAnnotation] = projectName
	annotations[projectRepositoryAdoptedAnnotation] = "true"
	repo.SetAnnotations(annotations)
	if _, err := c.Resource(codeRepositoryResource, "").Update(ctx, repo, metav1.UpdateOptions{}); err != nil {
		return codeProviderRequestError("claim Code repository", err)
	}
	return nil
}

// releaseProjectRepository removes the project claim from an adopted
// Repository (project cleanup/deletion). Best-effort semantics at call sites.
func releaseProjectRepository(ctx context.Context, c *asclient.Client, repositoryRef string) error {
	repo, err := c.Resource(codeRepositoryResource, "").Get(ctx, repositoryRef, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	labels := repo.GetLabels()
	delete(labels, projectRepositoryProjectLabel)
	repo.SetLabels(labels)
	annotations := repo.GetAnnotations()
	delete(annotations, projectRepositoryProjectAnnotation)
	delete(annotations, projectRepositoryAdoptedAnnotation)
	repo.SetAnnotations(annotations)
	_, err = c.Resource(codeRepositoryResource, "").Update(ctx, repo, metav1.UpdateOptions{})
	return err
}

// repositoryAdopted reports whether the Repository carries the adopted marker.
func repositoryAdopted(repo *unstructured.Unstructured) bool {
	return repo != nil && strings.EqualFold(strings.TrimSpace(repo.GetAnnotations()[projectRepositoryAdoptedAnnotation]), "true")
}

func projectCreateReadiness(ctx context.Context, c *asclient.Client) (ProjectCreateReadinessView, error) {
	gitOps, gitOpsErr := projectGitOpsReadiness(ctx, c)
	if gitOpsErr != nil {
		gitOps = ProjectCreateGitOpsReadiness{
			Reason:  "could not inspect tenant APIBindings",
			Message: gitOpsErr.Error(),
		}
	}
	connectionRef, err := selectCodeConnection(ctx, c, "")
	if err != nil {
		var validationErr *ValidationError
		if errors.As(err, &validationErr) {
			return ProjectCreateReadinessView{
				GitConnection: ProjectCreateGitConnectionReadiness{
					Ready:   false,
					Message: err.Error(),
				},
				GitOps: gitOps,
			}, nil
		}
		return ProjectCreateReadinessView{}, err
	}
	return ProjectCreateReadinessView{
		GitConnection: ProjectCreateGitConnectionReadiness{
			Ready:         true,
			ConnectionRef: connectionRef,
		},
		GitOps: gitOps,
	}, nil
}

// ensureProjectGitOpsReadiness is called before any Project resource is
// created. Direct delivery deliberately bypasses it: users can create a
// development-only/direct project while Deployments is disabled.
func ensureProjectGitOpsReadiness(ctx context.Context, c *asclient.Client) error {
	readiness, err := projectGitOpsReadiness(ctx, c)
	if err != nil {
		return newProjectGitOpsUnavailableError("could not inspect tenant APIBindings: " + err.Error())
	}
	if readiness.Available {
		return nil
	}
	reason := strings.TrimSpace(readiness.Reason)
	if reason == "" {
		reason = strings.TrimSpace(readiness.Message)
	}
	if reason == "" {
		reason = "required Deployments or App Studio APIBinding claims are not applied"
	}
	return newProjectGitOpsUnavailableError(reason + "; enable Deployments and update App Studio access, or choose Direct delivery")
}

type projectGitOpsUnavailableError struct{ message string }

func (e *projectGitOpsUnavailableError) Error() string {
	return "GitOps delivery is unavailable: " + e.message
}

func newProjectGitOpsUnavailableError(message string) error {
	return &projectGitOpsUnavailableError{message: strings.TrimSpace(message)}
}

func projectGitOpsReadiness(ctx context.Context, c *asclient.Client) (ProjectCreateGitOpsReadiness, error) {
	deployments, err := projectProviderBindingReadiness(ctx, c, deploymentsAPIExportPath, deploymentsAPIExportName, deploymentsGitOpsClaims)
	if err != nil {
		return ProjectCreateGitOpsReadiness{}, err
	}
	appStudio, err := projectProviderBindingReadiness(ctx, c, appStudioAPIExportPath, appStudioAPIExportName, appStudioGitOpsClaims)
	if err != nil {
		return ProjectCreateGitOpsReadiness{}, err
	}

	readiness := ProjectCreateGitOpsReadiness{
		Deployments: deployments,
		AppStudio:   appStudio,
		Available:   deployments.Ready && appStudio.Ready,
	}
	if readiness.Available {
		return readiness, nil
	}

	reasons := make([]string, 0, 4)
	if !deployments.Bound {
		reasons = append(reasons, "Deployments APIBinding is not Bound")
	} else if len(deployments.MissingClaims) > 0 {
		reasons = append(reasons, "Deployments APIBinding is missing applied claims: "+strings.Join(deployments.MissingClaims, ", "))
	}
	if !appStudio.Bound {
		reasons = append(reasons, "App Studio APIBinding is not Bound")
	} else if len(appStudio.MissingClaims) > 0 {
		reasons = append(reasons, "App Studio APIBinding is missing applied claims: "+strings.Join(appStudio.MissingClaims, ", "))
	}
	readiness.Reason = strings.Join(reasons, "; ")
	readiness.Message = "Open Providers, enable Deployments, and update provider access to approve the listed source and target claims"
	return readiness, nil
}

func projectProviderBindingReadiness(ctx context.Context, c *asclient.Client, exportPath, exportName string, required []projectGitOpsRequiredClaim) (ProjectCreateProviderBindingReadiness, error) {
	list, err := c.Resource(apiBindingsResource, "").List(ctx, metav1.ListOptions{})
	if err != nil {
		return ProjectCreateProviderBindingReadiness{}, fmt.Errorf("list APIBindings: %w", err)
	}

	var binding *unstructured.Unstructured
	for i := range list.Items {
		candidate := &list.Items[i]
		path, _, _ := unstructured.NestedString(candidate.Object, "spec", "reference", "export", "path")
		name, _, _ := unstructured.NestedString(candidate.Object, "spec", "reference", "export", "name")
		if strings.TrimSpace(path) != exportPath || strings.TrimSpace(name) != exportName {
			continue
		}
		// There should be one binding for each provider export. Prefer the
		// first exact export-name match and retain the ambiguity as a not-ready
		// state rather than accidentally trusting a binding from another path.
		if binding != nil {
			return ProjectCreateProviderBindingReadiness{}, fmt.Errorf("multiple APIBindings reference APIExport %q", exportName)
		}
		binding = candidate
	}

	readiness := ProjectCreateProviderBindingReadiness{}
	if binding == nil {
		readiness.MissingClaims = projectGitOpsClaimNames(required)
		return readiness, nil
	}

	phase, _, _ := unstructured.NestedString(binding.Object, "status", "phase")
	readiness.Bound = strings.EqualFold(strings.TrimSpace(phase), "Bound")
	applied := make(map[string]struct{})
	claims, _, _ := unstructured.NestedSlice(binding.Object, "status", "appliedPermissionClaims")
	for _, raw := range claims {
		claim, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		group, _ := claim["group"].(string)
		resource, _ := claim["resource"].(string)
		if group != "" && resource != "" {
			applied[projectGitOpsClaimName(projectGitOpsRequiredClaim{Group: group, Resource: resource})] = struct{}{}
		}
	}
	for _, claim := range required {
		if _, ok := applied[projectGitOpsClaimName(claim)]; !ok {
			readiness.MissingClaims = append(readiness.MissingClaims, projectGitOpsClaimName(claim))
		}
	}
	readiness.Ready = readiness.Bound && len(readiness.MissingClaims) == 0
	return readiness, nil
}

func projectGitOpsClaimName(claim projectGitOpsRequiredClaim) string {
	return claim.Group + "/" + claim.Resource
}

func projectGitOpsClaimNames(claims []projectGitOpsRequiredClaim) []string {
	names := make([]string, 0, len(claims))
	for _, claim := range claims {
		names = append(names, projectGitOpsClaimName(claim))
	}
	return names
}

func selectCodeConnection(ctx context.Context, c *asclient.Client, requested string) (string, error) {
	if requested != "" {
		conn, err := c.Resource(codeConnectionResource, "").Get(ctx, requested, metav1.GetOptions{})
		if err != nil {
			return "", codeProviderRequestError("get Code connection", err)
		}
		if !unstructuredConditionTrue(conn, codeConditionValidated) {
			return "", newValidationError(fmt.Sprintf("Code connection %q is not validated yet", requested))
		}
		return requested, nil
	}

	list, err := c.Resource(codeConnectionResource, "").List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", codeProviderRequestError("list Code connections", err)
	}
	sort.Slice(list.Items, func(i, j int) bool {
		return list.Items[i].GetName() < list.Items[j].GetName()
	})
	for i := range list.Items {
		if unstructuredConditionTrue(&list.Items[i], codeConditionValidated) {
			return list.Items[i].GetName(), nil
		}
	}
	if len(list.Items) == 0 {
		return "", newValidationError("You need to connect to a Git account before you can continue")
	}
	return "", newValidationError("wait for a Code connection to validate before creating an App Studio project")
}

func repositoryName(ctx context.Context, c *asclient.Client, requested, displayName string) (string, error) {
	base := dns1123Label(requested)
	if base == "" {
		base = dns1123Label(displayName)
	}
	if base == "" {
		base = "app"
	}
	for i := 0; i < 5; i++ {
		name := base
		if i > 0 {
			name = dns1123LabelWithSuffix(base, uuid.NewString()[:6])
		}
		if _, err := c.Resource(codeRepositoryResource, "").Get(ctx, name, metav1.GetOptions{}); apierrors.IsNotFound(err) {
			return name, nil
		} else if err != nil {
			return "", codeProviderRequestError("get Code repository", err)
		}
	}
	return dns1123LabelWithSuffix(base, uuid.NewString()[:8]), nil
}

func projectRepositoryView(ctx context.Context, c *asclient.Client, p *aiv1alpha1.Project) *ProjectRepositoryView {
	var get codeResourceGetter
	var list codeResourceLister
	if c != nil {
		get = func(ctx context.Context, gvr schema.GroupVersionResource, name string) (*unstructured.Unstructured, error) {
			return c.Resource(codeResourceFor(gvr), "").Get(ctx, name, metav1.GetOptions{})
		}
		list = func(ctx context.Context, gvr schema.GroupVersionResource, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
			return c.Resource(codeResourceFor(gvr), "").List(ctx, opts)
		}
	}
	return projectRepositoryViewFromResources(ctx, p, get, list)
}

func projectRepositoryViewFromGetter(ctx context.Context, p *aiv1alpha1.Project, get codeResourceGetter) *ProjectRepositoryView {
	return projectRepositoryViewFromResources(ctx, p, get, nil)
}

func projectRepositoryViewFromResources(ctx context.Context, p *aiv1alpha1.Project, get codeResourceGetter, list codeResourceLister) *ProjectRepositoryView {
	binding := p.Spec.Repository
	if binding == nil {
		return nil
	}
	ref := strings.TrimSpace(binding.RepositoryRef)
	if ref == "" {
		return nil
	}
	view := &ProjectRepositoryView{
		Ref:           ref,
		Name:          strings.TrimSpace(binding.Name),
		ConnectionRef: strings.TrimSpace(binding.ConnectionRef),
		Status:        projectRepositoryStatusProvisioning,
	}
	if get == nil {
		return view
	}
	repo, err := get(ctx, codeRepositoriesGVR, ref)
	if err != nil {
		if apierrors.IsNotFound(err) {
			view.Status = projectRepositoryStatusRepositoryMissing
			view.Message = fmt.Sprintf("Repository resource %q no longer exists.", ref)
			return view
		}
		view.Status = projectRepositoryStatusUnavailable
		view.Message = fmt.Sprintf("Could not read repository resource %q.", ref)
		return view
	}
	if name, _, _ := unstructured.NestedString(repo.Object, "spec", "name"); name != "" {
		view.Name = name
	}
	if connectionRef, _, _ := unstructured.NestedString(repo.Object, "spec", "connectionRef"); connectionRef != "" {
		view.ConnectionRef = connectionRef
	}
	view.HTMLURL, _, _ = unstructured.NestedString(repo.Object, "status", "htmlURL")
	if view.ConnectionRef == "" {
		view.Status = projectRepositoryStatusConnectionMissing
		view.Message = fmt.Sprintf("Repository resource %q does not reference a Code connection.", ref)
		return view
	}
	if _, err := get(ctx, codeConnectionsGVR, view.ConnectionRef); err != nil {
		if apierrors.IsNotFound(err) {
			view.Status = projectRepositoryStatusConnectionMissing
			view.Message = fmt.Sprintf("Connection resource %q no longer exists.", view.ConnectionRef)
			return view
		}
		view.Status = projectRepositoryStatusUnavailable
		view.Message = fmt.Sprintf("Could not read connection resource %q.", view.ConnectionRef)
		return view
	}
	readyStatus, _, readyMessage, readyFound := unstructuredCondition(repo, codeConditionReady)
	view.Ready = readyStatus == string(metav1.ConditionTrue)
	if view.Ready {
		view.Status = projectRepositoryStatusReady
	} else if readyFound && readyStatus == string(metav1.ConditionFalse) {
		view.Status = projectRepositoryStatusFailed
		view.Message = strings.TrimSpace(readyMessage)
		if view.Message == "" {
			view.Message = fmt.Sprintf("Repository resource %q failed to reconcile.", ref)
		}
	}
	view.Commits, view.commitsErr = projectRepositoryCommits(ctx, list, ref)
	return view
}

func projectRepositoryCommits(ctx context.Context, list codeResourceLister, repositoryRef string) ([]ProjectRepositoryCommitView, error) {
	if list == nil || strings.TrimSpace(repositoryRef) == "" {
		return nil, nil
	}
	selector := labels.SelectorFromSet(labels.Set{codeLabelRepository: repositoryRef}).String()
	items, err := list(ctx, codeRepositoryCommitsGVR, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, fmt.Errorf("list repository commits: %w", err)
	}
	commits := make([]ProjectRepositoryCommitView, 0, len(items.Items))
	for i := range items.Items {
		item := &items.Items[i]
		labelRef := strings.TrimSpace(item.GetLabels()[codeLabelRepository])
		specRef, _, _ := unstructured.NestedString(item.Object, "spec", "repositoryRef")
		if labelRef != repositoryRef || strings.TrimSpace(specRef) != repositoryRef {
			continue
		}
		view := projectRepositoryCommitView(item)
		if view.Name != "" {
			commits = append(commits, view)
		}
	}
	sort.Slice(commits, func(i, j int) bool {
		return commits[i].CreatedAt.After(commits[j].CreatedAt)
	})
	if len(commits) > 20 {
		commits = commits[:20]
	}
	return commits, nil
}

func projectRepositoryCommitView(obj *unstructured.Unstructured) ProjectRepositoryCommitView {
	view := ProjectRepositoryCommitView{
		Name:      obj.GetName(),
		CreatedAt: obj.GetCreationTimestamp().Time,
	}
	view.Phase, _, _ = unstructured.NestedString(obj.Object, "status", "phase")
	view.Branch, _, _ = unstructured.NestedString(obj.Object, "status", "branch")
	view.CommitSHA, _, _ = unstructured.NestedString(obj.Object, "status", "commitSHA")
	view.CommitURL, _, _ = unstructured.NestedString(obj.Object, "status", "commitURL")
	view.Message, _, _ = unstructured.NestedString(obj.Object, "spec", "message")
	if count, ok, _ := unstructured.NestedInt64(obj.Object, "status", "source", "fileCount"); ok {
		view.FileCount = count
	} else if files, ok, _ := unstructured.NestedSlice(obj.Object, "status", "files"); ok {
		view.FileCount = int64(len(files))
	}
	if completed, ok, _ := unstructured.NestedString(obj.Object, "status", "completedAt"); ok && completed != "" {
		if t, err := time.Parse(time.RFC3339, completed); err == nil {
			view.CompletedAt = &t
		}
	}
	return view
}

func codeProviderRequestError(op string, err error) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "server could not find the requested resource") ||
		strings.Contains(msg, "the server doesn't have a resource type") {
		return newValidationError("enable the Code provider before creating App Studio projects")
	}
	return fmt.Errorf("%s: %w", op, err)
}

func unstructuredConditionTrue(obj *unstructured.Unstructured, condType string) bool {
	status, _, _, found := unstructuredCondition(obj, condType)
	return found && status == string(metav1.ConditionTrue)
}

func unstructuredCondition(obj *unstructured.Unstructured, condType string) (status, reason, message string, found bool) {
	conds, found, _ := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if !found {
		return "", "", "", false
	}
	for _, raw := range conds {
		cond, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if cond["type"] == condType {
			status, _ := cond["status"].(string)
			reason, _ := cond["reason"].(string)
			message, _ := cond["message"].(string)
			return status, reason, message, true
		}
	}
	return "", "", "", false
}

func dns1123Label(str string) string {
	return slugifyProjectName(str)
}

func dns1123LabelWithSuffix(base, suffix string) string {
	suffix = dns1123Label(suffix)
	if suffix == "" {
		suffix = uuid.NewString()[:6]
	}
	maxBase := 63 - len(suffix) - 1
	if maxBase < 1 {
		maxBase = 1
	}
	if len(base) > maxBase {
		base = strings.Trim(base[:maxBase], "-")
	}
	if base == "" {
		base = "app"
	}
	return base + "-" + suffix
}
