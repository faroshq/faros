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

// This file owns run-sandbox policy: configuration, eligibility, identity, and
// metadata contracts. The infrastructure provider owns the worker
// implementation; App Studio only addresses an ordinary infrastructure
// Instance through the protocol and lifecycle helpers in the companion files.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	"github.com/faroshq/provider-app-studio/workspace"
)

const (
	projectAssistantRunSandboxModeEnv         = "APP_STUDIO_RUN_SANDBOX_MODE"
	projectAssistantReplicaCountEnv           = "APP_STUDIO_REPLICA_COUNT"
	projectAssistantRunSandboxTemplateEnv     = "APP_STUDIO_RUN_SANDBOX_TEMPLATE"
	projectAssistantRunSandboxDefaultTemplate = "universal-coding-sandbox"
	projectAssistantRunSandboxHardTTL         = 12 * time.Hour
	// Cached project sandboxes are retained until their hard lifetime. The
	// infrastructure Template and this coordinator use the same cap; an idle
	// cache may be evicted for quota pressure, but never merely after 30m.
	projectAssistantRunSandboxIdleTTL            = 12 * time.Hour
	projectAssistantRunSandboxMaxActive          = 2
	projectAssistantRunSandboxWorkspaceVerb      = "workspace"
	projectAssistantRunSandboxResource           = "instances"
	projectAssistantRunSandboxAPIVersion         = "infrastructure.faros.sh/v1alpha1"
	projectAssistantRunSandboxKind               = "Instance"
	projectAssistantRunSandboxEnvironment        = "assistant-run"
	projectAssistantRunSandboxBinding            = "assistant-run"
	projectAssistantRunSandboxNamePrefix         = "as-run-"
	projectAssistantRunSandboxMaxChanges         = 128
	projectAssistantRunSandboxMaxChangeBytes     = 8 << 20
	projectAssistantRunSandboxLabel              = "faros.sh/app-studio-run-sandbox"
	projectAssistantRunSandboxIdleExpiry         = "faros.sh/app-studio-run-sandbox-idle-expires-at"
	projectAssistantRunSandboxHardExpiry         = "faros.sh/app-studio-run-sandbox-hard-expires-at"
	projectAssistantRunSandboxClaimOwner         = "faros.sh/app-studio-run-sandbox-claim-owner"
	projectAssistantRunSandboxClaimExpiry        = "faros.sh/app-studio-run-sandbox-claim-expires-at"
	projectAssistantRunSandboxCacheGeneration    = "faros.sh/app-studio-run-sandbox-cache-generation"
	projectAssistantRunSandboxCacheState         = "faros.sh/app-studio-run-sandbox-cache-state"
	projectAssistantRunSandboxLastActivity       = "faros.sh/app-studio-run-sandbox-last-activity-at"
	projectAssistantRunSandboxCacheStateNew      = "provisioning"
	projectAssistantRunSandboxCacheStateActive   = "active"
	projectAssistantRunSandboxCacheStateCached   = "cached"
	projectAssistantRunSandboxCacheStateEvicting = "evicting"
	// The universal-coding-sandbox development overlay derives all of its
	// component-scoped child names from the Instance name (see
	// providers/infrastructure/backend/kro/devoverlay.go):
	//
	//   <instance>-dev-workspace
	//   <instance>-dev-workspace-platform-state
	//   <instance>-dev-workspace-actions-ca
	//   <instance>-dev-workspace-control
	//
	// The control Service is a DNS label and therefore limits the Instance
	// name to 63 characters. PVCs and ConfigMaps use Kubernetes DNS subdomain
	// names (up to 253 characters), so their longer suffixes do not reduce the
	// Instance budget. The instance-wide token Secret/ServiceAccount/Role/
	// RoleBinding/Job use shorter -dev-control or -dev-token suffixes.
	projectAssistantRunSandboxChildWorkspaceSuffix      = "-dev-workspace"
	projectAssistantRunSandboxChildPlatformStateSuffix  = "-dev-workspace-platform-state"
	projectAssistantRunSandboxChildActionsCASuffix      = "-dev-workspace-actions-ca"
	projectAssistantRunSandboxChildControlServiceSuffix = "-dev-workspace-control"
	projectAssistantRunSandboxChildControlSecretSuffix  = "-dev-control"
	projectAssistantRunSandboxChildTokenSuffix          = "-dev-token"
	projectAssistantRunSandboxChildServiceSuffix        = projectAssistantRunSandboxChildControlServiceSuffix
	projectAssistantRunSandboxDNSLabelMaxLength         = 63
	projectAssistantRunSandboxDNSSubdomainMaxLength     = 253
	projectAssistantRunSandboxNameMaxLength             = projectAssistantRunSandboxDNSLabelMaxLength - len(projectAssistantRunSandboxChildServiceSuffix)
	projectAssistantRunSandboxHashBytes                 = 6
	projectAssistantRunSandboxHashLength                = projectAssistantRunSandboxHashBytes * 2
	projectAssistantRunSandboxNameMaxBase               = projectAssistantRunSandboxNameMaxLength - len(projectAssistantRunSandboxNamePrefix) - 1 - projectAssistantRunSandboxHashLength
	// Instance creation is asynchronous: the ordinary Instance first becomes
	// visible, then its development overlay publishes the routing references
	// consumed by the data-plane resolver. Keep setup bounded while polling
	// the API rather than racing the first /sync request with a fixed sleep.
	projectAssistantRunSandboxReadyTimeout = 2 * time.Minute
	projectAssistantRunSandboxReadyPoll    = 250 * time.Millisecond
)

type CodingSandboxMode string

const (
	CodingSandboxModeOff CodingSandboxMode = "off"
	CodingSandboxModeOn  CodingSandboxMode = "on"
)

// CodingSandboxConfig is process-owned policy. Mode is deliberately binary:
// off always uses the existing Template-backed development image, while on
// uses the universal sandbox only when the workspace's App Studio and
// Infrastructure bindings independently resolve to valid platform or
// same-organization exports.
type CodingSandboxConfig struct {
	Mode         CodingSandboxMode
	ReplicaCount int
}

// CodingSandboxEligibility is reevaluated at every start and resume before any
// Infrastructure lookup. Binding resolution is fail-closed when the hub cannot
// prove both provider bindings and the exact Infrastructure transport route.
type CodingSandboxEligibility struct {
	Eligible            bool   `json:"eligible"`
	Reason              string `json:"reason"`
	ProviderExportPath  string `json:"providerExportPath,omitempty"`
	TransportGeneration string `json:"transportGeneration,omitempty"`
}

type CodingSandboxEligibilityResolver func(context.Context, identity, workspace.Scope) (CodingSandboxEligibility, error)

const (
	projectAssistantPlatformAppStudioExportPath      = "root:faros:providers:app-studio"
	projectAssistantPlatformInfrastructureExportPath = "root:faros:providers:infrastructure"
	projectAssistantSandboxTransportGeneration       = "hub-virtual-workspace-v1"
)

// ParseCodingSandboxConfig validates the operator's binary startup policy.
// This feature is unreleased, so obsolete experimental modes are rejected
// instead of being migrated implicitly; obsolete boolean flags are no longer
// part of this parser.
func ParseCodingSandboxConfig(lookup func(string) string) (CodingSandboxConfig, error) {
	if lookup == nil {
		lookup = func(string) string { return "" }
	}
	config := CodingSandboxConfig{
		Mode:         CodingSandboxModeOff,
		ReplicaCount: 1,
	}
	if rawReplicas := strings.TrimSpace(lookup(projectAssistantReplicaCountEnv)); rawReplicas != "" {
		replicas, err := strconv.Atoi(rawReplicas)
		if err != nil || replicas < 1 {
			return CodingSandboxConfig{}, fmt.Errorf("%s must be a positive integer", projectAssistantReplicaCountEnv)
		}
		config.ReplicaCount = replicas
	}
	rawMode := strings.ToLower(strings.TrimSpace(lookup(projectAssistantRunSandboxModeEnv)))
	if rawMode != "" {
		config.Mode = CodingSandboxMode(rawMode)
		switch config.Mode {
		case CodingSandboxModeOff, CodingSandboxModeOn:
		default:
			return CodingSandboxConfig{}, fmt.Errorf("%s must be off or on", projectAssistantRunSandboxModeEnv)
		}
	}
	if config.Mode == CodingSandboxModeOn && config.ReplicaCount != 1 {
		return CodingSandboxConfig{}, fmt.Errorf("%s=on requires %s=1 until sandbox claims have distributed CAS", projectAssistantRunSandboxModeEnv, projectAssistantReplicaCountEnv)
	}
	return config, nil
}

func codingSandboxEligibility(config CodingSandboxConfig) CodingSandboxEligibility {
	if config.Mode == CodingSandboxModeOn && config.ReplicaCount != 1 {
		return CodingSandboxEligibility{Reason: "coding sandbox mode on requires a single App Studio replica"}
	}
	if config.Mode == CodingSandboxModeOff {
		return CodingSandboxEligibility{Reason: "coding sandbox mode is off"}
	}
	return CodingSandboxEligibility{Reason: "coding sandbox binding resolver is not available"}
}

func (s *Server) codingSandboxConfigSnapshot() (CodingSandboxConfig, error) {
	if s == nil {
		return CodingSandboxConfig{}, errors.New("App Studio server is unavailable")
	}
	s.mu.Lock()
	config, configured := s.runSandboxConfig, s.runSandboxConfigured
	s.mu.Unlock()
	if !configured {
		parsed, err := ParseCodingSandboxConfig(getenv)
		if err != nil {
			return CodingSandboxConfig{}, err
		}
		config = parsed
	}
	return config, nil
}

// ResolveCodingSandboxEligibility reevaluates policy for the exact caller and
// Project scope. Off is a process-owned short circuit. On must resolve the
// exact workspace bindings through the installed server resolver and returns
// fail-closed when the resolver is absent, errors, or returns incomplete data.
func (s *Server) ResolveCodingSandboxEligibility(ctx context.Context, id identity, scope workspace.Scope) CodingSandboxEligibility {
	config, err := s.codingSandboxConfigSnapshot()
	if err != nil {
		return CodingSandboxEligibility{Reason: err.Error()}
	}
	if config.Mode == CodingSandboxModeOff || config.ReplicaCount != 1 {
		return codingSandboxEligibility(config)
	}
	s.mu.Lock()
	resolver := s.codingSandboxResolver
	s.mu.Unlock()
	if resolver == nil {
		return CodingSandboxEligibility{Reason: "coding sandbox binding resolver is not available"}
	}
	eligibility, err := resolver(ctx, id, scope)
	if err != nil {
		return CodingSandboxEligibility{Reason: "coding sandbox binding resolution failed: " + err.Error()}
	}
	if !eligibility.Eligible {
		if strings.TrimSpace(eligibility.Reason) == "" {
			eligibility.Reason = "coding sandbox bindings are ineligible"
		}
		return eligibility
	}
	if strings.TrimSpace(eligibility.ProviderExportPath) == "" || strings.TrimSpace(eligibility.TransportGeneration) == "" {
		return CodingSandboxEligibility{Reason: "coding sandbox binding resolver returned incomplete provider transport identity"}
	}
	return eligibility
}

// projectAssistantDevelopmentTemplateBound reports whether the Project has a
// hosted development environment contract. The per-run universal sandbox can
// still author, execute, and checkpoint source when this is false; only the
// legacy Project development-preview synchronization is unavailable.
func projectAssistantDevelopmentTemplateBound(project *aiv1alpha1.Project) bool {
	return project != nil && project.Spec.Template != nil && strings.TrimSpace(project.Spec.Template.Name) != ""
}

// projectAssistantRunSandboxMetadata is deliberately persisted in the
// assistant checkpoint.  It is the recovery contract after a permission
// interrupt or replica restart, not merely an in-memory handle.
type projectAssistantRunSandboxMetadata struct {
	Version             int                             `json:"version"`
	Status              string                          `json:"status"`
	RunID               string                          `json:"runID"`
	OrgUUID             string                          `json:"orgUUID"`
	WorkspaceUUID       string                          `json:"workspaceUUID"`
	ProjectName         string                          `json:"projectName"`
	ProjectUID          string                          `json:"projectUID"`
	Template            string                          `json:"template"`
	ProviderExportPath  string                          `json:"providerExportPath"`
	TransportGeneration string                          `json:"transportGeneration"`
	Instance            projectAssistantSandboxInstance `json:"instance"`
	SourceRevision      uint64                          `json:"sourceRevision"`
	SourceDigest        string                          `json:"sourceDigest"`
	RemoteRevision      uint64                          `json:"remoteRevision,omitempty"`
	RemoteDigest        string                          `json:"remoteDigest,omitempty"`
	RemoteCheckpointID  string                          `json:"remoteCheckpointID,omitempty"`
	CheckpointRevision  uint64                          `json:"checkpointRevision,omitempty"`
	CheckpointDigest    string                          `json:"checkpointDigest,omitempty"`
	CacheGeneration     string                          `json:"cacheGeneration,omitempty"`
	CreatedAt           time.Time                       `json:"createdAt"`
	LastActivityAt      time.Time                       `json:"lastActivityAt"`
	IdleExpiresAt       time.Time                       `json:"idleExpiresAt"`
	HardExpiresAt       time.Time                       `json:"hardExpiresAt"`
	Conflict            string                          `json:"conflict,omitempty"`
}

type projectAssistantSandboxInstance struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Resource   string `json:"resource"`
	Name       string `json:"name"`
}

type projectAssistantSandboxCheckpoint struct {
	Metadata projectAssistantRunSandboxMetadata `json:"metadata"`
}

func cloneProjectAssistantSandboxCheckpoint(src *projectAssistantSandboxCheckpoint) *projectAssistantSandboxCheckpoint {
	if src == nil {
		return nil
	}
	out := *src
	return &out
}

// projectAssistantRunSandboxEnabled remains a narrow test seam. Runtime
// admission uses the server-owned CodingSandboxEligibility contract.
func projectAssistantRunSandboxEnabled() bool {
	config, err := ParseCodingSandboxConfig(getenv)
	return err == nil && config.Mode != CodingSandboxModeOff
}

// getenv is a small test seam.  Tests may replace it without mutating global
// process environment while concurrent assistant runs are active.
var getenv = func(key string) string { return os.Getenv(key) }
