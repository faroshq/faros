/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/gorilla/mux"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
)

const (
	projectIntegrationDefaultEnvironment = "development"
	projectIntegrationProviderDatabricks = "databricks"
	projectIntegrationActionQueryTable   = "query_table"
	projectIntegrationActionVersionV1    = "v1"

	databricksTableAPIVersion = "databricks.kedge.faros.sh/v1alpha1"
	databricksTableKind       = "Table"
	databricksTableResource   = "tables"

	projectIntegrationMaxLimit = 100
)

var projectIntegrationIdentifierRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,62}$`)

var databricksColumnIdentifierRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// projectIntegrationAddRequest is intentionally generic: the binding
// contract can point at resources owned by any provider. Only the Databricks
// query_table/v1 adapter is currently executable by the invocation gateway.
type projectIntegrationAddRequest struct {
	Environment    string                                       `json:"environment,omitempty"`
	Alias          string                                       `json:"alias"`
	Provider       string                                       `json:"provider"`
	Kind           aiv1alpha1.ProjectBindingKind                `json:"kind,omitempty"`
	ResourceRef    *aiv1alpha1.ProjectProviderResourceReference `json:"resourceRef"`
	AllowedActions []aiv1alpha1.ProjectProviderActionSpec       `json:"allowedActions,omitempty"`
	// Actions is accepted as a concise alias for clients that use the action
	// terminology directly. It is normalized into AllowedActions before the
	// Project is persisted.
	Actions []aiv1alpha1.ProjectProviderActionSpec `json:"actions,omitempty"`
}

type projectIntegrationPatchRequest struct {
	AllowedActions []aiv1alpha1.ProjectProviderActionSpec `json:"allowedActions,omitempty"`
	Actions        []aiv1alpha1.ProjectProviderActionSpec `json:"actions,omitempty"`
}

type projectIntegrationView struct {
	Environment    string                                       `json:"environment"`
	Alias          string                                       `json:"alias"`
	Provider       string                                       `json:"provider"`
	Kind           aiv1alpha1.ProjectBindingKind                `json:"kind"`
	ResourceRef    *aiv1alpha1.ProjectProviderResourceReference `json:"resourceRef,omitempty"`
	AllowedActions []aiv1alpha1.ProjectProviderActionSpec       `json:"allowedActions,omitempty"`
	Phase          string                                       `json:"phase,omitempty"`
}

type projectIntegrationInvokeRequest struct {
	Action        string          `json:"action"`
	ActionVersion string          `json:"actionVersion,omitempty"`
	Version       string          `json:"version,omitempty"`
	Input         json.RawMessage `json:"input,omitempty"`
}

type projectIntegrationInvokeResult struct {
	Integration   string `json:"integration"`
	Environment   string `json:"environment"`
	Action        string `json:"action"`
	ActionVersion string `json:"actionVersion"`
	Result        any    `json:"result"`
}

type projectIntegrationQueryInput struct {
	Columns []string `json:"columns,omitempty"`
	Limit   *int     `json:"limit,omitempty"`
}

func (s *Server) addProjectIntegration(w http.ResponseWriter, r *http.Request) {
	c, id, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	var req projectIntegrationAddRequest
	if !decodeStrictJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Environment) == "" {
		req.Environment = projectIntegrationDefaultEnvironment
	}
	if err := validateProjectIntegrationAlias(req.Alias); err != nil {
		writeError(w, err)
		return
	}
	if strings.TrimSpace(req.Provider) == "" {
		writeError(w, newValidationError("provider is required"))
		return
	}
	if req.Kind != "" && req.Kind != aiv1alpha1.ProjectBindingKindProviderReference {
		writeError(w, newValidationError("integrations must use kind providerReference"))
		return
	}
	if req.ResourceRef == nil {
		writeError(w, newValidationError("resourceRef is required"))
		return
	}
	ref := normalizeIntegrationResourceRef(req.Provider, req.ResourceRef)
	if err := validateProviderReferenceRef(ref); err != nil {
		writeError(w, err)
		return
	}
	if err := validateIntegrationProviderReference(req.Provider, ref); err != nil {
		writeError(w, err)
		return
	}
	actions, err := normalizeProjectIntegrationActions(append(req.AllowedActions, req.Actions...))
	if err != nil {
		writeError(w, err)
		return
	}
	if len(actions) == 0 {
		writeError(w, newValidationError("at least one allowed action is required"))
		return
	}
	if err := observeProjectProviderReference(r.Context(), c, aiv1alpha1.ProjectProviderBindingSpec{
		Name: req.Alias, Provider: req.Provider,
		Kind: aiv1alpha1.ProjectBindingKindProviderReference, ResourceRef: ref,
	}); err != nil {
		writeError(w, err)
		return
	}

	next := project.DeepCopy()
	env := ensureProjectIntegrationEnvironment(next, req.Environment)
	for _, existing := range env.Bindings {
		if strings.EqualFold(strings.TrimSpace(existing.Name), strings.TrimSpace(req.Alias)) {
			writeStatus(w, http.StatusConflict, "Conflict", fmt.Sprintf("project integration %q already exists", req.Alias))
			return
		}
	}
	env.Bindings = append(env.Bindings, aiv1alpha1.ProjectProviderBindingSpec{
		Name: req.Alias, Provider: strings.TrimSpace(req.Provider),
		Kind:        aiv1alpha1.ProjectBindingKindProviderReference,
		ResourceRef: ref, AllowedActions: actions,
	})
	newBinding := env.Bindings[len(env.Bindings)-1]
	if _, err := c.Projects().Update(r.Context(), next, metav1.UpdateOptions{}); err != nil {
		writeProjectError(w, err)
		return
	}
	phase := projectProviderBindingStatus(r.Context(), c, next, newBinding, id).Phase
	if phase == "" {
		phase = "Pending"
	}
	writeJSON(w, http.StatusCreated, projectIntegrationViewForBinding(req.Environment, newBinding, phase))
}

func (s *Server) listProjectIntegrations(w http.ResponseWriter, r *http.Request) {
	c, id, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	project = projectWithLiveBindingStatus(r.Context(), c, project, id)
	items := make([]projectIntegrationView, 0)
	statusByKey := map[string]string{}
	for _, envStatus := range project.Status.Environments {
		for _, bindingStatus := range envStatus.Bindings {
			statusByKey[envStatus.Name+"\x00"+bindingStatus.Name] = bindingStatus.Phase
		}
	}
	for _, env := range project.Spec.Environments {
		for _, binding := range env.Bindings {
			if binding.Kind != aiv1alpha1.ProjectBindingKindProviderReference {
				continue
			}
			items = append(items, projectIntegrationViewForBinding(env.Name, binding, statusByKey[env.Name+"\x00"+binding.Name]))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Environment != items[j].Environment {
			return items[i].Environment < items[j].Environment
		}
		return items[i].Alias < items[j].Alias
	})
	writeJSON(w, http.StatusOK, ListResponse[projectIntegrationView]{Items: items})
}

func (s *Server) removeProjectIntegration(w http.ResponseWriter, r *http.Request) {
	c, _, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	alias := strings.TrimSpace(muxVars(r)["integration"])
	if alias == "" {
		writeError(w, newValidationError("integration alias is required"))
		return
	}
	next := project.DeepCopy()
	removed := false
	for i := range next.Spec.Environments {
		env := &next.Spec.Environments[i]
		kept := env.Bindings[:0]
		for _, binding := range env.Bindings {
			if binding.Kind == aiv1alpha1.ProjectBindingKindProviderReference && strings.EqualFold(strings.TrimSpace(binding.Name), alias) {
				removed = true
				continue
			}
			kept = append(kept, binding)
		}
		env.Bindings = kept
	}
	if !removed {
		writeStatus(w, http.StatusNotFound, "NotFound", fmt.Sprintf("project integration %q was not found", alias))
		return
	}
	if _, err := c.Projects().Update(r.Context(), next, metav1.UpdateOptions{}); err != nil {
		writeProjectError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) patchProjectIntegration(w http.ResponseWriter, r *http.Request) {
	c, _, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	alias := strings.TrimSpace(muxVars(r)["integration"])
	var req projectIntegrationPatchRequest
	if !decodeStrictJSON(w, r, &req) {
		return
	}
	actions, err := normalizeProjectIntegrationActions(append(req.AllowedActions, req.Actions...))
	if err != nil {
		writeError(w, err)
		return
	}
	if len(actions) == 0 {
		writeError(w, newValidationError("at least one allowed action is required"))
		return
	}
	next := project.DeepCopy()
	for i := range next.Spec.Environments {
		for j := range next.Spec.Environments[i].Bindings {
			binding := &next.Spec.Environments[i].Bindings[j]
			if binding.Kind == aiv1alpha1.ProjectBindingKindProviderReference && strings.EqualFold(strings.TrimSpace(binding.Name), alias) {
				binding.AllowedActions = actions
				updated, updateErr := c.Projects().Update(r.Context(), next, metav1.UpdateOptions{})
				if updateErr != nil {
					writeProjectError(w, updateErr)
					return
				}
				writeJSON(w, http.StatusOK, projectIntegrationViewForBinding(next.Spec.Environments[i].Name, updated.Spec.Environments[i].Bindings[j], ""))
				return
			}
		}
	}
	writeStatus(w, http.StatusNotFound, "NotFound", fmt.Sprintf("project integration %q was not found", alias))
}

func (s *Server) invokeProjectIntegration(w http.ResponseWriter, r *http.Request) {
	c, id, project, ok := s.requireProjectWithClient(w, r)
	if !ok {
		return
	}
	alias := strings.TrimSpace(muxVars(r)["integration"])
	var req projectIntegrationInvokeRequest
	if !decodeStrictJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Action) == "" {
		// The action path form is useful for generated clients that keep the
		// action outside the JSON body. The body form remains canonical and can
		// carry an explicit actionVersion for version negotiation.
		req.Action = strings.TrimSpace(muxVars(r)["action"])
	}
	requestedVersion := req.ActionVersion
	if strings.TrimSpace(requestedVersion) == "" {
		requestedVersion = req.Version
	} else if strings.TrimSpace(req.Version) != "" && !strings.EqualFold(strings.TrimSpace(requestedVersion), strings.TrimSpace(req.Version)) {
		writeError(w, newValidationError("actionVersion and version do not match"))
		return
	}
	name, version, err := normalizeIntegrationAction(req.Action, requestedVersion)
	if err != nil {
		writeError(w, err)
		return
	}
	envName, binding, err := findProjectIntegration(project, alias)
	if err != nil {
		writeError(w, err)
		return
	}
	if strings.TrimSpace(binding.Provider) != projectIntegrationProviderDatabricks {
		writeError(w, newValidationError(fmt.Sprintf("integration %q provider %q has no action adapter", alias, binding.Provider)))
		return
	}
	if binding.Kind != aiv1alpha1.ProjectBindingKindProviderReference || binding.ResourceRef == nil {
		writeError(w, newValidationError(fmt.Sprintf("integration %q is not a provider reference", alias)))
		return
	}
	allowed := false
	revoked := false
	for _, declared := range binding.AllowedActions {
		if strings.EqualFold(strings.TrimSpace(declared.Name), name) && strings.EqualFold(strings.TrimSpace(declared.Version), version) {
			allowed = true
			revoked = declared.Revoked
			break
		}
	}
	if !allowed {
		writeStatus(w, http.StatusForbidden, "Forbidden", fmt.Sprintf("integration %q does not allow action %s/%s", alias, name, version))
		return
	}
	if revoked {
		writeStatus(w, http.StatusForbidden, "Forbidden", fmt.Sprintf("integration %q action %s/%s has been revoked", alias, name, version))
		return
	}
	if name != projectIntegrationActionQueryTable || version != projectIntegrationActionVersionV1 {
		writeError(w, newValidationError(fmt.Sprintf("action %s/%s is not implemented", name, version)))
		return
	}
	ref := normalizeIntegrationResourceRef(binding.Provider, binding.ResourceRef)
	if err := validateProviderReferenceRef(ref); err != nil {
		writeError(w, err)
		return
	}
	if err := validateIntegrationProviderReference(binding.Provider, ref); err != nil {
		writeError(w, err)
		return
	}
	gvr, gvrErr := projectProviderResourceGVR(ref)
	if gvrErr != nil {
		writeError(w, gvrErr)
		return
	}
	table, err := c.Resource(providerBindingResource(gvr, ref.Kind), "").Get(r.Context(), strings.TrimSpace(ref.Name), metav1.GetOptions{})
	if err != nil {
		writeError(w, err)
		return
	}
	args, err := projectIntegrationQueryArguments(req.Input, table)
	if err != nil {
		writeError(w, err)
		return
	}
	args["actionVersion"] = version
	// tableRef is injected exclusively from the server-side binding. The
	// caller cannot select a different Table, even when it knows another
	// resource name in the tenant.
	args["tableRef"] = strings.TrimSpace(ref.Name)
	result, err := callProjectMCPTool(r.Context(), s.mcpEndpoint(id.clusterID), r, id.tenantPath, s.mcpInsecureSkipTLSVerify, "databricks__"+name, args)
	if err != nil {
		writeError(w, err)
		return
	}
	var structured any
	if json.Valid([]byte(result)) {
		_ = json.Unmarshal([]byte(result), &structured)
	} else {
		structured = result
	}
	writeJSON(w, http.StatusOK, projectIntegrationInvokeResult{
		Integration: alias, Environment: envName, Action: name, ActionVersion: version, Result: structured,
	})
	_ = envName // environment is included in binding lookup for ambiguity checks
}

func validateProjectIntegrationAlias(alias string) error {
	if !projectIntegrationIdentifierRE.MatchString(strings.TrimSpace(alias)) {
		return newValidationError("integration alias must be 1-63 characters and contain only letters, numbers, '_' or '-'")
	}
	return nil
}

func normalizeIntegrationResourceRef(provider string, ref *aiv1alpha1.ProjectProviderResourceReference) *aiv1alpha1.ProjectProviderResourceReference {
	if ref == nil {
		return nil
	}
	out := *ref
	if strings.EqualFold(strings.TrimSpace(provider), projectIntegrationProviderDatabricks) {
		if strings.TrimSpace(out.APIVersion) == "" {
			out.APIVersion = databricksTableAPIVersion
		}
		if strings.TrimSpace(out.Kind) == "" {
			out.Kind = databricksTableKind
		}
		if strings.TrimSpace(out.Resource) == "" {
			out.Resource = databricksTableResource
		}
	}
	return &out
}

func validateProviderReferenceRef(ref *aiv1alpha1.ProjectProviderResourceReference) error {
	if ref == nil {
		return newValidationError("resourceRef is required")
	}
	if strings.TrimSpace(ref.Name) == "" {
		return newValidationError("resourceRef.name is required")
	}
	if strings.TrimSpace(ref.APIVersion) == "" || strings.TrimSpace(ref.Kind) == "" || strings.TrimSpace(ref.Resource) == "" {
		return newValidationError("resourceRef must include apiVersion, kind, resource, and name")
	}
	if _, err := projectProviderResourceGVR(ref); err != nil {
		return newValidationError("invalid resourceRef: " + err.Error())
	}
	return nil
}

func validateIntegrationProviderReference(provider string, ref *aiv1alpha1.ProjectProviderResourceReference) error {
	if !strings.EqualFold(strings.TrimSpace(provider), projectIntegrationProviderDatabricks) {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(ref.APIVersion), databricksTableAPIVersion) ||
		!strings.EqualFold(strings.TrimSpace(ref.Kind), databricksTableKind) ||
		!strings.EqualFold(strings.TrimSpace(ref.Resource), databricksTableResource) {
		return newValidationError("databricks integrations must reference a databricks.kedge.faros.sh/v1alpha1 Table resource")
	}
	return nil
}

func normalizeProjectIntegrationActions(actions []aiv1alpha1.ProjectProviderActionSpec) ([]aiv1alpha1.ProjectProviderActionSpec, error) {
	if len(actions) == 0 {
		return nil, nil
	}
	out := make([]aiv1alpha1.ProjectProviderActionSpec, 0, len(actions))
	seen := map[string]struct{}{}
	for _, action := range actions {
		name, version, err := normalizeIntegrationAction(action.Name, action.Version)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(name + "\x00" + version)
		if _, exists := seen[key]; exists {
			return nil, newValidationError(fmt.Sprintf("duplicate allowed action %s/%s", name, version))
		}
		seen[key] = struct{}{}
		out = append(out, aiv1alpha1.ProjectProviderActionSpec{Name: name, Version: version, Revoked: action.Revoked})
	}
	return out, nil
}

func normalizeIntegrationAction(action, explicitVersion string) (string, string, error) {
	action = strings.TrimSpace(action)
	explicitVersion = strings.TrimSpace(explicitVersion)
	if action == "" {
		return "", "", newValidationError("action is required")
	}
	name, version := action, explicitVersion
	if slash := strings.IndexByte(action, '/'); slash >= 0 {
		if strings.Count(action, "/") != 1 {
			return "", "", newValidationError("action must be name/version")
		}
		parsedVersion := action[slash+1:]
		name = action[:slash]
		if version != "" && !strings.EqualFold(version, parsedVersion) {
			return "", "", newValidationError("action version does not match action name/version")
		}
		version = parsedVersion
	}
	if !projectIntegrationIdentifierRE.MatchString(name) || !projectIntegrationIdentifierRE.MatchString(version) {
		return "", "", newValidationError("action name and version must be non-empty identifiers")
	}
	return name, version, nil
}

func findProjectIntegration(project *aiv1alpha1.Project, alias string) (string, aiv1alpha1.ProjectProviderBindingSpec, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return "", aiv1alpha1.ProjectProviderBindingSpec{}, newValidationError("integration alias is required")
	}
	var found *aiv1alpha1.ProjectProviderBindingSpec
	envName := ""
	for _, env := range project.Spec.Environments {
		for i := range env.Bindings {
			binding := &env.Bindings[i]
			if binding.Kind != aiv1alpha1.ProjectBindingKindProviderReference || !strings.EqualFold(strings.TrimSpace(binding.Name), alias) {
				continue
			}
			if found != nil {
				return "", aiv1alpha1.ProjectProviderBindingSpec{}, newValidationError(fmt.Sprintf("integration alias %q is ambiguous across environments", alias))
			}
			copy := binding.DeepCopy()
			found = copy
			envName = env.Name
		}
	}
	if found == nil {
		return "", aiv1alpha1.ProjectProviderBindingSpec{}, apierrors.NewNotFound(schema.GroupResource{Group: aiv1alpha1.GroupName, Resource: "project integrations"}, alias)
	}
	return envName, *found, nil
}

func ensureProjectIntegrationEnvironment(project *aiv1alpha1.Project, name string) *aiv1alpha1.ProjectEnvironmentSpec {
	for i := range project.Spec.Environments {
		if strings.EqualFold(strings.TrimSpace(project.Spec.Environments[i].Name), strings.TrimSpace(name)) {
			return &project.Spec.Environments[i]
		}
	}
	project.Spec.Environments = append(project.Spec.Environments, aiv1alpha1.ProjectEnvironmentSpec{
		Name: name, Mode: aiv1alpha1.ProjectEnvironmentModeLive, Promotion: aiv1alpha1.ProjectPromotionManual,
	})
	return &project.Spec.Environments[len(project.Spec.Environments)-1]
}

func projectIntegrationViewForBinding(environment string, binding aiv1alpha1.ProjectProviderBindingSpec, phase string) projectIntegrationView {
	actions := append([]aiv1alpha1.ProjectProviderActionSpec(nil), binding.AllowedActions...)
	return projectIntegrationView{
		Environment: environment, Alias: binding.Name, Provider: binding.Provider,
		Kind: binding.Kind, ResourceRef: binding.ResourceRef, AllowedActions: actions, Phase: phase,
	}
}

func projectIntegrationQueryArguments(raw json.RawMessage, table *unstructured.Unstructured) (map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		raw = []byte(`{}`)
	}
	var in projectIntegrationQueryInput
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		return nil, newValidationError("invalid query_table/v1 input: " + err.Error())
	}
	if in.Limit != nil && (*in.Limit < 1 || *in.Limit > projectIntegrationMaxLimit) {
		return nil, newValidationError(fmt.Sprintf("limit must be between 1 and %d", projectIntegrationMaxLimit))
	}
	seenColumns := map[string]struct{}{}
	for i, column := range in.Columns {
		column = strings.TrimSpace(column)
		if !databricksColumnIdentifierRE.MatchString(column) {
			return nil, newValidationError(fmt.Sprintf("column %q is not a valid column identifier", column))
		}
		if _, exists := seenColumns[column]; exists {
			return nil, newValidationError(fmt.Sprintf("column %q is duplicated", column))
		}
		seenColumns[column] = struct{}{}
		in.Columns[i] = column
	}
	if table != nil {
		if allowed, ok := tableColumnNames(table); ok && len(in.Columns) > 0 {
			for _, column := range in.Columns {
				if _, exists := allowed[strings.TrimSpace(column)]; !exists {
					return nil, newValidationError(fmt.Sprintf("column %q is not declared by the bound Table", column))
				}
			}
		}
	}
	args := map[string]any{}
	if len(in.Columns) > 0 {
		args["columns"] = in.Columns
	}
	if in.Limit != nil {
		args["limit"] = *in.Limit
	}
	return args, nil
}

func tableColumnNames(table *unstructured.Unstructured) (map[string]struct{}, bool) {
	columns, ok, _ := unstructured.NestedSlice(table.Object, "status", "columns")
	if !ok || len(columns) == 0 {
		return nil, false
	}
	out := map[string]struct{}{}
	for _, raw := range columns {
		obj, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, _ := obj["name"].(string)
		if name = strings.TrimSpace(name); name != "" {
			out[name] = struct{}{}
		}
	}
	return out, len(out) > 0
}

// muxVars is kept in one helper so handlers remain straightforward and tests
// can exercise them with a mux route exactly as production does.
func muxVars(r *http.Request) map[string]string {
	return mux.Vars(r)
}
