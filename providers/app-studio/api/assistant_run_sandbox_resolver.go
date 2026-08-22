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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/faroshq/provider-app-studio/workspace"
)

const (
	codingSandboxBindingCallTimeout      = 15 * time.Second
	codingSandboxBindingMaxResponseBytes = 1 << 20
	codingSandboxInfrastructureGroup     = "infrastructure.faros.sh"
	codingSandboxInfrastructureResource  = "instances"
)

type enabledProviderBindingsResponse struct {
	BindingsByProvider map[string]enabledProviderBinding `json:"bindingsByProvider"`
}

type enabledProviderBinding struct {
	BindingName string `json:"bindingName"`
	ExportPath  string `json:"exportPath"`
	SelfHosted  bool   `json:"selfHosted"`
	// StaleClaimsKnown distinguishes a completed inspection with no
	// mismatches from an inspection that the hub could not perform. Older hubs
	// omit this field, so sandbox mode fails closed rather than assuming the
	// binding is safe.
	StaleClaimsKnown bool                        `json:"staleClaimsKnown"`
	StaleClaims      []enabledProviderStaleClaim `json:"staleClaims,omitempty"`
	Terminating      bool                        `json:"terminating,omitempty"`
	DeletionBlocked  string                      `json:"deletionBlocked,omitempty"`
}

type enabledProviderStaleClaim struct {
	Group    string `json:"group"`
	Resource string `json:"resource"`
}

func (s *Server) resolveCodingSandboxEligibilityFromHub(ctx context.Context, id identity, scope workspace.Scope) (CodingSandboxEligibility, error) {
	if strings.TrimSpace(scope.OrgUUID) == "" || strings.TrimSpace(scope.WorkspaceUUID) == "" {
		return CodingSandboxEligibility{}, errors.New("project scope is missing organization or workspace identity")
	}
	if id.orgUUID != scope.OrgUUID || id.workspaceUUID != scope.WorkspaceUUID {
		return CodingSandboxEligibility{}, errors.New("caller identity does not match the project workspace scope")
	}

	bindings, err := s.fetchEnabledProviderBindings(ctx, id, scope)
	if err != nil {
		return CodingSandboxEligibility{}, err
	}
	appStudio, appStudioEnabled := bindings.BindingsByProvider["app-studio"]
	infrastructure, infrastructureEnabled := bindings.BindingsByProvider["infrastructure"]
	if !appStudioEnabled || strings.TrimSpace(appStudio.BindingName) == "" {
		return CodingSandboxEligibility{Reason: "App Studio is not enabled in this workspace"}, nil
	}
	if !infrastructureEnabled || strings.TrimSpace(infrastructure.BindingName) == "" {
		return CodingSandboxEligibility{Reason: "Infrastructure is not enabled in this workspace"}, nil
	}
	if appStudio.Terminating || infrastructure.Terminating {
		return CodingSandboxEligibility{Reason: "App Studio or Infrastructure is being disabled in this workspace"}, nil
	}
	if !appStudio.StaleClaimsKnown || !infrastructure.StaleClaimsKnown {
		return CodingSandboxEligibility{Reason: "App Studio or Infrastructure stale claim inspection is unavailable"}, nil
	}
	if hasStaleInfrastructureClaim(appStudio.StaleClaims) || hasStaleInfrastructureClaim(infrastructure.StaleClaims) {
		return CodingSandboxEligibility{Reason: "App Studio has a stale Infrastructure instances claim"}, nil
	}

	appStudioExport := strings.TrimSpace(appStudio.ExportPath)
	infrastructureExport := strings.TrimSpace(infrastructure.ExportPath)
	orgProviderPrefix := "root:faros:tenants:" + scope.OrgUUID + ":providers:"
	orgAppStudioExport := orgProviderPrefix + "app-studio"
	orgInfrastructureExport := orgProviderPrefix + "infrastructure"

	switch {
	case !appStudio.SelfHosted && !infrastructure.SelfHosted &&
		appStudioExport == projectAssistantPlatformAppStudioExportPath &&
		infrastructureExport == projectAssistantPlatformInfrastructureExportPath:
		return eligibleCodingSandbox(infrastructureExport, "workspace uses platform App Studio and platform Infrastructure"), nil
	case appStudio.SelfHosted && infrastructure.SelfHosted &&
		appStudioExport == orgAppStudioExport && infrastructureExport == orgInfrastructureExport:
		return eligibleCodingSandbox(infrastructureExport, "workspace uses same-organization App Studio and Infrastructure"), nil
	case appStudio.SelfHosted != infrastructure.SelfHosted:
		return CodingSandboxEligibility{Reason: "App Studio and Infrastructure use mixed platform and self-hosted ownership"}, nil
	default:
		return CodingSandboxEligibility{Reason: "App Studio or Infrastructure binding points at an unexpected provider export"}, nil
	}
}

func eligibleCodingSandbox(infrastructureExport, reason string) CodingSandboxEligibility {
	return CodingSandboxEligibility{
		Eligible:            true,
		Reason:              reason,
		ProviderExportPath:  infrastructureExport,
		TransportGeneration: projectAssistantSandboxTransportGeneration,
	}
}

func hasStaleInfrastructureClaim(claims []enabledProviderStaleClaim) bool {
	for _, claim := range claims {
		if claim.Group == codingSandboxInfrastructureGroup && claim.Resource == codingSandboxInfrastructureResource {
			return true
		}
	}
	return false
}

func (s *Server) fetchEnabledProviderBindings(ctx context.Context, id identity, scope workspace.Scope) (enabledProviderBindingsResponse, error) {
	base := strings.TrimRight(strings.TrimSpace(s.hubBase), "/")
	if base == "" {
		return enabledProviderBindingsResponse{}, errors.New("provider binding hub endpoint is not configured")
	}
	endpoint := fmt.Sprintf(
		"%s/api/orgs/%s/workspaces/%s/providers/enabled",
		base,
		url.PathEscape(scope.OrgUUID),
		url.PathEscape(scope.WorkspaceUUID),
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return enabledProviderBindingsResponse{}, fmt.Errorf("new provider binding request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	setHubCallerIdentityHeaders(req, id)
	client := &http.Client{
		Timeout:   codingSandboxBindingCallTimeout,
		Transport: projectMCPTransport(s.mcpInsecureSkipTLSVerify),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("provider binding redirect rejected")
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return enabledProviderBindingsResponse{}, fmt.Errorf("GET provider bindings: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, codingSandboxBindingMaxResponseBytes+1))
	if err != nil {
		return enabledProviderBindingsResponse{}, fmt.Errorf("read provider binding response: %w", err)
	}
	if len(body) > codingSandboxBindingMaxResponseBytes {
		return enabledProviderBindingsResponse{}, errors.New("provider binding response exceeds size limit")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return enabledProviderBindingsResponse{}, fmt.Errorf("GET provider bindings returned status %d", resp.StatusCode)
	}
	var bindings enabledProviderBindingsResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&bindings); err != nil {
		return enabledProviderBindingsResponse{}, fmt.Errorf("decode provider binding response: %w", err)
	}
	if bindings.BindingsByProvider == nil {
		bindings.BindingsByProvider = map[string]enabledProviderBinding{}
	}
	return bindings, nil
}

func setHubCallerIdentityHeaders(req *http.Request, id identity) {
	if id.token != "" {
		req.Header.Set("Authorization", "Bearer "+id.token)
	}
	if id.tenantPath != "" {
		req.Header.Set("X-Faros-Tenant", id.tenantPath)
	}
	if id.clusterID != "" {
		req.Header.Set("X-Faros-Cluster", id.clusterID)
	}
	if id.orgUUID != "" {
		req.Header.Set("X-Faros-Org", id.orgUUID)
	}
	if id.workspaceUUID != "" {
		req.Header.Set("X-Faros-Workspace", id.workspaceUUID)
	}
	if id.user != "" {
		req.Header.Set("X-Faros-User", id.user)
	}
}
