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
	"regexp"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
)

const (
	providerCatalogPath             = "/api/providers"
	providerCatalogMaxResponseBytes = 4 << 20
	providerCatalogCallTimeout      = 15 * time.Second
)

var projectActionSchemaDigestRE = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// providerActionCatalogResolver is deliberately caller-scoped: a production
// resolver receives the identity whose bearer token must authorize the hub
// catalog request. Tests may inject a deterministic resolver without opening
// a second HTTP server, while production always uses fetchProviderActionCatalog.
type providerActionCatalogResolver func(context.Context, identity) ([]providerCatalogEntry, error)

type providerCatalogResponse struct {
	Items []providerCatalogEntry `json:"items"`
}

// These structs mirror the hub's /api/providers action metadata contract. The
// App Studio gateway only needs a subset for grant verification, but retaining
// the published fields makes decoding forward-compatible and keeps fixtures
// representative of the catalog wire shape.
type providerCatalogEntry struct {
	Name    string                  `json:"name"`
	Ready   bool                    `json:"ready"`
	Actions []providerCatalogAction `json:"actions"`
}

type providerCatalogAction struct {
	ID            string                       `json:"id"`
	DisplayName   string                       `json:"displayName"`
	Description   string                       `json:"description"`
	BoundResource providerCatalogBoundResource `json:"boundResource"`
	InputSchema   json.RawMessage              `json:"inputSchema"`
	OutputSchema  json.RawMessage              `json:"outputSchema"`
	SchemaDigest  string                       `json:"schemaDigest"`
	ExecutionMode string                       `json:"executionMode"`
	ReadOnly      bool                         `json:"readOnly"`
	Risk          string                       `json:"risk"`
	Idempotency   string                       `json:"idempotency"`
	Limits        providerCatalogActionLimits  `json:"limits"`
	Consent       providerCatalogActionConsent `json:"consent"`
	Deprecation   *providerCatalogDeprecation  `json:"deprecation,omitempty"`
}

type providerCatalogBoundResource struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Resource   string `json:"resource"`
}

type providerCatalogActionLimits struct {
	TimeoutSeconds int64 `json:"timeoutSeconds"`
	MaxInputBytes  int64 `json:"maxInputBytes"`
	MaxOutputBytes int64 `json:"maxOutputBytes"`
	MaxResultItems int64 `json:"maxResultItems"`
}

type providerCatalogActionConsent struct {
	Required bool   `json:"required"`
	Prompt   string `json:"prompt"`
	Scope    string `json:"scope"`
}

type providerCatalogDeprecation struct {
	Deprecated    bool         `json:"deprecated"`
	Message       string       `json:"message"`
	ReplacementID string       `json:"replacementID"`
	Sunset        *metav1.Time `json:"sunset"`
}

func (s *Server) verifyProjectActionGrants(ctx context.Context, id identity, provider string, ref *aiv1alpha1.ProjectProviderResourceReference, actions []aiv1alpha1.ProjectProviderActionSpec, consentAccepted bool) ([]aiv1alpha1.ProjectProviderActionSpec, error) {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return nil, newValidationError("provider is required")
	}
	if err := validateProviderReferenceRef(ref); err != nil {
		return nil, err
	}
	caller := strings.TrimSpace(id.user)
	if caller == "" {
		return nil, newValidationError("authenticated caller is required to grant provider actions")
	}
	catalog, err := s.providerActionCatalog(ctx, id)
	if err != nil {
		return nil, err
	}
	verified := make([]aiv1alpha1.ProjectProviderActionSpec, 0, len(actions))
	for _, grant := range actions {
		meta, err := findProviderCatalogAction(catalog, provider, ref, grant)
		if err != nil {
			return nil, err
		}
		if meta.Consent.Required && !consentAccepted {
			return nil, newValidationError(fmt.Sprintf("provider action %s requires explicit consentAccepted", grant.Name+"/"+grant.Version))
		}
		grant.GrantedBy = caller
		grantedAt := metav1.Now()
		grant.GrantedAt = &grantedAt
		grant.Revoked = false
		grant.RevokedBy = ""
		grant.RevokedAt = nil
		verified = append(verified, grant)
	}
	return verified, nil
}

func findProviderCatalogAction(catalog []providerCatalogEntry, provider string, ref *aiv1alpha1.ProjectProviderResourceReference, grant aiv1alpha1.ProjectProviderActionSpec) (providerCatalogAction, error) {
	var entry *providerCatalogEntry
	for i := range catalog {
		if catalog[i].Name == provider {
			entry = &catalog[i]
			break
		}
	}
	if entry == nil {
		return providerCatalogAction{}, newValidationError(fmt.Sprintf("provider %q is not present in the action catalog", provider))
	}
	if !entry.Ready {
		return providerCatalogAction{}, newValidationError(fmt.Sprintf("provider %q is not ready for action grants", provider))
	}
	name, version, err := normalizeIntegrationAction(grant.Name, grant.Version)
	if err != nil {
		return providerCatalogAction{}, err
	}
	for _, action := range entry.Actions {
		catalogName, catalogVersion, ok := splitProviderCatalogActionID(action.ID)
		if !ok || catalogName != name || catalogVersion != version {
			continue
		}
		if action.BoundResource.APIVersion != strings.TrimSpace(ref.APIVersion) ||
			action.BoundResource.Kind != strings.TrimSpace(ref.Kind) ||
			action.BoundResource.Resource != strings.TrimSpace(ref.Resource) {
			return providerCatalogAction{}, newValidationError(fmt.Sprintf("provider action %s/%s is not bound to resource %s/%s/%s", name, version, ref.APIVersion, ref.Kind, ref.Resource))
		}
		if action.SchemaDigest == "" || !projectActionSchemaDigestRE.MatchString(action.SchemaDigest) {
			return providerCatalogAction{}, newValidationError(fmt.Sprintf("provider action %s/%s has an invalid schemaDigest", name, version))
		}
		if action.SchemaDigest != grant.SchemaDigest {
			return providerCatalogAction{}, newValidationError(fmt.Sprintf("provider action %s/%s schemaDigest does not match the catalog", name, version))
		}
		if action.Deprecation != nil && action.Deprecation.Deprecated {
			return providerCatalogAction{}, newValidationError(fmt.Sprintf("provider action %s/%s is deprecated", name, version))
		}
		return action, nil
	}
	return providerCatalogAction{}, newValidationError(fmt.Sprintf("provider action %s/%s is not available for the bound resource", name, version))
}

func splitProviderCatalogActionID(id string) (string, string, bool) {
	parts := strings.Split(strings.TrimSpace(id), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func (s *Server) providerActionCatalog(ctx context.Context, id identity) ([]providerCatalogEntry, error) {
	if s.providerActionCatalogResolver != nil {
		return s.providerActionCatalogResolver(ctx, id)
	}
	return s.fetchProviderActionCatalog(ctx, id)
}

func (s *Server) fetchProviderActionCatalog(ctx context.Context, id identity) ([]providerCatalogEntry, error) {
	base := strings.TrimRight(strings.TrimSpace(s.hubBase), "/")
	if base == "" {
		return nil, errors.New("provider action catalog hub endpoint is not configured")
	}
	endpoint := base + providerCatalogPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("new provider action catalog request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if id.token != "" {
		req.Header.Set("Authorization", "Bearer "+id.token)
	}
	if id.tenantPath != "" {
		req.Header.Set("X-Kedge-Tenant", id.tenantPath)
	}
	if id.clusterID != "" {
		req.Header.Set("X-Kedge-Cluster", id.clusterID)
	}
	if id.orgUUID != "" {
		req.Header.Set("X-Kedge-Org", id.orgUUID)
	}
	if id.workspaceUUID != "" {
		req.Header.Set("X-Kedge-Workspace", id.workspaceUUID)
	}
	if id.user != "" {
		req.Header.Set("X-Kedge-User", id.user)
	}
	client := &http.Client{
		Timeout: providerCatalogCallTimeout,
		// Catalog lookup uses the same explicitly configured local-hub TLS
		// setting as the MCP client. Provider Action invocation deliberately
		// uses projectProviderActionTransport instead, so this development
		// escape hatch cannot weaken the action gateway's certificate checks.
		Transport: projectMCPTransport(s.mcpInsecureSkipTLSVerify),
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("provider action catalog redirect rejected")
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, providerCatalogMaxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read provider action catalog response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s returned status %d", endpoint, resp.StatusCode)
	}
	var catalog providerCatalogResponse
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(&catalog); err != nil {
		return nil, fmt.Errorf("decode provider action catalog response: %w", err)
	}
	return catalog.Items, nil
}
