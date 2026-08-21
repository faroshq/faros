/*
Copyright 2026 The Faros Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package api

// This file owns the App Studio side of the run sandbox protocol.  The
// infrastructure provider owns the worker implementation; App Studio only
// addresses an ordinary infrastructure Instance and uses its existing sync,
// exec, and data-plane paths.  Keeping this seam here makes the experimental
// path removable without changing the legacy development runtime.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	aiv1alpha1 "github.com/faroshq/provider-app-studio/apis/ai/v1alpha1"
	"github.com/faroshq/provider-app-studio/bindings"
	asclient "github.com/faroshq/provider-app-studio/client"
	"github.com/faroshq/provider-app-studio/store"
	"github.com/faroshq/provider-app-studio/tenant"
	"github.com/faroshq/provider-app-studio/workspace"
)

const (
	projectAssistantRunSandboxModeEnv         = "APP_STUDIO_RUN_SANDBOX_MODE"
	projectAssistantRunSandboxFlagEnv         = "APP_STUDIO_RUN_SANDBOX"
	projectAssistantDevelopmentModeEnv        = "APP_STUDIO_DEVELOPMENT_MODE"
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
	CodingSandboxModeOff     CodingSandboxMode = "off"
	CodingSandboxModeBYOOnly CodingSandboxMode = "byo-only"
	CodingSandboxModeForce   CodingSandboxMode = "force"
)

// CodingSandboxConfig is process-owned policy. DevelopmentMode is an explicit
// deployment assertion; it is never inferred from TLS, localhost, model
// optimization, or missing credentials.
type CodingSandboxConfig struct {
	Mode            CodingSandboxMode
	DevelopmentMode bool
	ReplicaCount    int
}

// CodingSandboxEligibility is reevaluated at every start and resume before any
// Infrastructure lookup. BYO resolution is intentionally fail-closed until a
// provider export/transport resolver exists.
type CodingSandboxEligibility struct {
	Eligible            bool   `json:"eligible"`
	Reason              string `json:"reason"`
	ProviderExportPath  string `json:"providerExportPath,omitempty"`
	TransportGeneration string `json:"transportGeneration,omitempty"`
}

type CodingSandboxEligibilityResolver func(context.Context, identity, workspace.Scope) (CodingSandboxEligibility, error)

const (
	projectAssistantPlatformInfrastructureExportPath = "root:faros:providers:infrastructure"
	projectAssistantSandboxTransportGeneration       = "hub-virtual-workspace-v1"
)

func parseSandboxBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on", "enabled", "codex_poc", "codex-poc":
		return true
	default:
		return false
	}
}

// ParseCodingSandboxConfig validates startup policy and reports compatibility
// warnings for legacy boolean flags. A true legacy flag maps only to byo-only;
// it never grants access to the platform Infrastructure provider.
func ParseCodingSandboxConfig(lookup func(string) string) (CodingSandboxConfig, []string, error) {
	if lookup == nil {
		lookup = func(string) string { return "" }
	}
	config := CodingSandboxConfig{
		Mode:            CodingSandboxModeOff,
		DevelopmentMode: parseSandboxBool(lookup(projectAssistantDevelopmentModeEnv)),
		ReplicaCount:    1,
	}
	if rawReplicas := strings.TrimSpace(lookup(projectAssistantReplicaCountEnv)); rawReplicas != "" {
		replicas, err := strconv.Atoi(rawReplicas)
		if err != nil || replicas < 1 {
			return CodingSandboxConfig{}, nil, fmt.Errorf("%s must be a positive integer", projectAssistantReplicaCountEnv)
		}
		config.ReplicaCount = replicas
	}
	rawMode := strings.ToLower(strings.TrimSpace(lookup(projectAssistantRunSandboxModeEnv)))
	if rawMode != "" {
		config.Mode = CodingSandboxMode(rawMode)
		switch config.Mode {
		case CodingSandboxModeOff, CodingSandboxModeBYOOnly, CodingSandboxModeForce:
		default:
			return CodingSandboxConfig{}, nil, fmt.Errorf("%s must be off, byo-only, or force", projectAssistantRunSandboxModeEnv)
		}
	} else {
		legacy := []string{}
		legacyEnabled := false
		for _, key := range []string{projectAssistantRunSandboxFlagEnv, "APP_STUDIO_CODEX_SANDBOX"} {
			if raw := strings.TrimSpace(lookup(key)); raw != "" {
				legacy = append(legacy, key)
				legacyEnabled = legacyEnabled || parseSandboxBool(raw)
			}
		}
		if len(legacy) > 0 {
			if legacyEnabled {
				config.Mode = CodingSandboxModeBYOOnly
			}
			return config, []string{fmt.Sprintf("legacy sandbox flag(s) %s map to %s; configure %s explicitly", strings.Join(legacy, ", "), config.Mode, projectAssistantRunSandboxModeEnv)}, nil
		}
	}
	if config.Mode == CodingSandboxModeForce && !config.DevelopmentMode {
		return CodingSandboxConfig{}, nil, fmt.Errorf("%s=force requires %s=true", projectAssistantRunSandboxModeEnv, projectAssistantDevelopmentModeEnv)
	}
	if config.Mode == CodingSandboxModeForce && config.ReplicaCount != 1 {
		return CodingSandboxConfig{}, nil, fmt.Errorf("%s=force requires %s=1 until sandbox claims have distributed CAS", projectAssistantRunSandboxModeEnv, projectAssistantReplicaCountEnv)
	}
	return config, nil, nil
}

func codingSandboxEligibility(config CodingSandboxConfig) CodingSandboxEligibility {
	switch config.Mode {
	case CodingSandboxModeForce:
		if !config.DevelopmentMode {
			return CodingSandboxEligibility{Reason: "force mode requires explicit development mode"}
		}
		if config.ReplicaCount != 1 {
			return CodingSandboxEligibility{Reason: "force mode requires a single App Studio replica"}
		}
		return CodingSandboxEligibility{
			Eligible:            true,
			Reason:              "explicit development force mode uses the platform Infrastructure export",
			ProviderExportPath:  projectAssistantPlatformInfrastructureExportPath,
			TransportGeneration: projectAssistantSandboxTransportGeneration,
		}
	case CodingSandboxModeBYOOnly:
		return CodingSandboxEligibility{Reason: "BYO coding sandbox resolver is not available yet"}
	default:
		return CodingSandboxEligibility{Reason: "coding sandbox mode is off"}
	}
}

func (s *Server) codingSandboxConfigSnapshot() (CodingSandboxConfig, error) {
	if s == nil {
		return CodingSandboxConfig{}, errors.New("App Studio server is unavailable")
	}
	s.mu.Lock()
	config, configured := s.runSandboxConfig, s.runSandboxConfigured
	s.mu.Unlock()
	if !configured {
		parsed, _, err := ParseCodingSandboxConfig(getenv)
		if err != nil {
			return CodingSandboxConfig{}, err
		}
		config = parsed
	}
	return config, nil
}

// ResolveCodingSandboxEligibility reevaluates policy for the exact caller and
// Project scope. Off and force are process-owned short circuits. BYO-only must
// resolve an organization binding through the installed server resolver and
// returns fail-closed when that resolver is absent or incomplete.
func (s *Server) ResolveCodingSandboxEligibility(ctx context.Context, id identity, scope workspace.Scope) CodingSandboxEligibility {
	config, err := s.codingSandboxConfigSnapshot()
	if err != nil {
		return CodingSandboxEligibility{Reason: err.Error()}
	}
	if config.Mode != CodingSandboxModeBYOOnly {
		return codingSandboxEligibility(config)
	}
	s.mu.Lock()
	resolver := s.codingSandboxResolver
	s.mu.Unlock()
	if resolver == nil {
		return CodingSandboxEligibility{Reason: "BYO coding sandbox resolver is not available yet"}
	}
	eligibility, err := resolver(ctx, id, scope)
	if err != nil {
		return CodingSandboxEligibility{Reason: "BYO coding sandbox resolution failed: " + err.Error()}
	}
	if !eligibility.Eligible {
		if strings.TrimSpace(eligibility.Reason) == "" {
			eligibility.Reason = "BYO coding sandbox binding is ineligible"
		}
		return eligibility
	}
	if strings.TrimSpace(eligibility.ProviderExportPath) == "" || strings.TrimSpace(eligibility.TransportGeneration) == "" {
		return CodingSandboxEligibility{Reason: "BYO coding sandbox resolver returned incomplete provider transport identity"}
	}
	return eligibility
}

var (
	errProjectAssistantRunSandboxConflict = errors.New("assistant run sandbox workspace conflict")
	errProjectAssistantRunSandboxClosed   = errors.New("assistant run sandbox is closed")
)

var runSandboxInstancesResource = tenant.Resource{
	GVR:  schema.GroupVersionResource{Group: "infrastructure.faros.sh", Version: "v1alpha1", Resource: projectAssistantRunSandboxResource},
	Kind: projectAssistantRunSandboxKind,
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
	config, _, err := ParseCodingSandboxConfig(getenv)
	return err == nil && config.Mode != CodingSandboxModeOff
}

// getenv is a small test seam.  Tests may replace it without mutating global
// process environment while concurrent assistant runs are active.
var getenv = func(key string) string { return os.Getenv(key) }

type projectAssistantSandboxManager struct {
	mu     sync.Mutex
	active map[string]map[string]string
}

func newProjectAssistantSandboxManager() *projectAssistantSandboxManager {
	return &projectAssistantSandboxManager{active: map[string]map[string]string{}}
}

// acquire serializes use of a cached project environment inside App Studio's
// enforced single-writer deployment. The Instance annotations are a durable
// recovery/eviction fence, but the production GraphQL apply path is not a
// compare-and-swap primitive; multi-replica cache ownership must move to the
// provider's durable execution-claim store before the deployment is scaled.
func (m *projectAssistantSandboxManager) acquire(tenantKey, cacheKey, runID string) (func(), error) {
	if m == nil {
		return func() {}, nil
	}
	tenantKey = strings.TrimSpace(tenantKey)
	cacheKey = strings.TrimSpace(cacheKey)
	runID = strings.TrimSpace(runID)
	if tenantKey == "" || cacheKey == "" || runID == "" {
		return nil, errors.New("assistant run sandbox tenant, cache, and run IDs are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active == nil {
		m.active = map[string]map[string]string{}
	}
	owned := m.active[tenantKey]
	if owned == nil {
		owned = map[string]string{}
		m.active[tenantKey] = owned
	}
	if owner := owned[cacheKey]; owner != "" && owner != runID {
		return nil, fmt.Errorf("project coding environment is already claimed by run %q", owner)
	}
	if _, ok := owned[cacheKey]; !ok && len(owned) >= projectAssistantRunSandboxMaxActive {
		return nil, fmt.Errorf("tenant already has %d active assistant run sandboxes", projectAssistantRunSandboxMaxActive)
	}
	owned[cacheKey] = runID
	released := false
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if released {
			return
		}
		released = true
		owned := m.active[tenantKey]
		if owned[cacheKey] == runID {
			delete(owned, cacheKey)
		}
		if len(owned) == 0 {
			delete(m.active, tenantKey)
		}
	}, nil
}

func (m *projectAssistantSandboxManager) claimed(cacheKey string) bool {
	if m == nil || strings.TrimSpace(cacheKey) == "" {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, owned := range m.active {
		if strings.TrimSpace(owned[cacheKey]) != "" {
			return true
		}
	}
	return false
}

func projectAssistantRunSandboxTenantKey(id identity, scope workspace.Scope) string {
	org := strings.TrimSpace(id.orgUUID)
	if org == "" {
		org = strings.TrimSpace(scope.OrgUUID)
	}
	ws := strings.TrimSpace(id.workspaceUUID)
	if ws == "" {
		ws = strings.TrimSpace(scope.WorkspaceUUID)
	}
	return org + "/" + ws
}

func projectAssistantRunSandboxName(scope workspace.Scope, project *aiv1alpha1.Project, runID string) string {
	// runID is intentionally ignored. The Instance is a project-scoped cache;
	// run ownership is a durable annotation claim, while this name keeps every
	// new chat/follow-up on the same workspace volume for up to the hard TTL.
	projectName := strings.TrimSpace(scope.ProjectName)
	projectUID := strings.TrimSpace(scope.ProjectUID)
	if project != nil {
		if projectName == "" {
			projectName = strings.TrimSpace(project.Name)
		}
		if projectUID == "" {
			projectUID = string(project.UID)
		}
	}
	material := strings.Join([]string{scope.OrgUUID, scope.WorkspaceUUID, projectName, projectUID}, "\x00")
	sum := sha256.Sum256([]byte(material))
	base := dnsSafeSandboxName(projectName)
	name := projectAssistantRunSandboxNamePrefix + base + "-" + hex.EncodeToString(sum[:projectAssistantRunSandboxHashBytes])
	// dnsSafeSandboxName already applies the downstream suffix budget. Keep a
	// defensive bound here so future edits to the name components cannot
	// silently reintroduce an invalid Instance name.
	if len(name) > projectAssistantRunSandboxNameMaxLength {
		name = name[:projectAssistantRunSandboxNameMaxLength]
		name = strings.TrimRight(name, "-")
	}
	return name
}

// ensureProjectAssistantRunSandboxOwner makes the cached coding environment a
// real child of its Project. Terminal turns deliberately retain this Instance,
// so run-lifecycle cleanup is no longer sufficient; the owner reference is the
// durable deletion contract for API, kubectl, and controller-driven Project
// deletion alike.
func ensureProjectAssistantRunSandboxOwner(instance *unstructured.Unstructured, project *aiv1alpha1.Project) (bool, error) {
	if instance == nil {
		return false, errors.New("run sandbox instance is nil")
	}
	owner := bindings.OwnerRef(project)
	if owner == nil {
		return false, errors.New("run sandbox Project owner identity is incomplete")
	}
	refs := instance.GetOwnerReferences()
	for _, ref := range refs {
		if ref.Controller != nil && *ref.Controller && ref.UID != owner.UID {
			return false, fmt.Errorf("%w: run sandbox instance already has another controller owner", errProjectAssistantRunSandboxConflict)
		}
		if ref.APIVersion == owner.APIVersion && ref.Kind == owner.Kind {
			if ref.Name != owner.Name || ref.UID != owner.UID {
				return false, fmt.Errorf("%w: run sandbox instance belongs to another Project", errProjectAssistantRunSandboxConflict)
			}
			return false, nil
		}
	}
	instance.SetOwnerReferences(append(refs, *owner))
	return true, nil
}

func dnsSafeSandboxName(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	value := strings.Trim(b.String(), "-")
	if value == "" {
		return "project"
	}
	if len(value) > projectAssistantRunSandboxNameMaxBase {
		value = strings.TrimRight(value[:projectAssistantRunSandboxNameMaxBase], "-")
	}
	return value
}

type projectAssistantSandboxWorkspaceChange struct {
	Path            string `json:"path"`
	Operation       string `json:"operation"`
	Content         string `json:"content,omitempty"`
	ExpectedVersion string `json:"expectedVersion,omitempty"`
}

type projectAssistantSandboxWorkspaceRequest struct {
	Action           string                                   `json:"action"`
	Files            []projectSandboxSyncFile                 `json:"files,omitempty"`
	Path             string                                   `json:"path,omitempty"`
	SourcePath       string                                   `json:"sourcePath,omitempty"`
	DestinationPath  string                                   `json:"destinationPath,omitempty"`
	Pattern          string                                   `json:"pattern,omitempty"`
	GrepPattern      string                                   `json:"grepPattern,omitempty"`
	Glob             string                                   `json:"glob,omitempty"`
	Offset           int                                      `json:"offset,omitempty"`
	Limit            int                                      `json:"limit,omitempty"`
	FileType         string                                   `json:"fileType,omitempty"`
	Content          string                                   `json:"content,omitempty"`
	OldString        string                                   `json:"oldString,omitempty"`
	NewString        string                                   `json:"newString,omitempty"`
	ReplaceAll       bool                                     `json:"replaceAll,omitempty"`
	ExpectedVersion  string                                   `json:"expectedVersion,omitempty"`
	Changes          []projectAssistantSandboxWorkspaceChange `json:"changes,omitempty"`
	SourceRevision   uint64                                   `json:"sourceRevision,omitempty"`
	SourceDigest     string                                   `json:"sourceDigest,omitempty"`
	BaselineFiles    []projectAssistantSandboxBaselineFile    `json:"baselineFiles,omitempty"`
	CheckpointID     string                                   `json:"checkpointID,omitempty"`
	CheckpointAction string                                   `json:"checkpointAction,omitempty"`
}

type projectAssistantSandboxBaselineFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type projectAssistantSandboxWorkspaceResponse struct {
	Status         string                                   `json:"status,omitempty"`
	File           workspace.FileContent                    `json:"file,omitempty"`
	Files          workspace.FileList                       `json:"files,omitempty"`
	Matches        []projectAssistantSandboxGrepMatch       `json:"matches,omitempty"`
	Mutation       workspace.MutationResult                 `json:"mutation,omitempty"`
	Changes        []projectAssistantSandboxWorkspaceChange `json:"changes,omitempty"`
	SourceRevision uint64                                   `json:"sourceRevision,omitempty"`
	SourceDigest   string                                   `json:"sourceDigest,omitempty"`
	Conflict       string                                   `json:"conflict,omitempty"`
	Entries        []projectAssistantSandboxListEntry       `json:"entries,omitempty"`
	CheckpointID   string                                   `json:"checkpointID,omitempty"`
}

type projectAssistantSandboxListEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size,omitempty"`
}

type projectAssistantSandboxGrepMatch struct {
	Content string `json:"content"`
	Path    string `json:"path"`
	Line    int    `json:"line"`
}

// These wire types intentionally mirror the Infrastructure dev-agent
// workspace contract without importing provider-infrastructure code.  The
// internal request/response above remains App Studio's stable seam for fakes
// and for the tool registry.
type projectAssistantSandboxListWireRequest struct {
	Path       string `json:"path,omitempty"`
	Recursive  bool   `json:"recursive,omitempty"`
	MaxEntries int    `json:"maxEntries,omitempty"`
}

type projectAssistantSandboxListWireEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size,omitempty"`
	Mode uint32 `json:"mode,omitempty"`
}

type projectAssistantSandboxListWireResponse struct {
	Path           string                                 `json:"path"`
	Entries        []projectAssistantSandboxListWireEntry `json:"entries"`
	SourceRevision uint64                                 `json:"sourceRevision,omitempty"`
	SourceDigest   string                                 `json:"sourceDigest,omitempty"`
}

type projectAssistantSandboxSeedWireResponse struct {
	Phase          string `json:"phase"`
	SourceRevision uint64 `json:"sourceRevision"`
	SourceDigest   string `json:"sourceDigest"`
}

type projectAssistantSandboxReadWireRequest struct {
	Paths    []string `json:"paths"`
	MaxBytes int      `json:"maxBytes,omitempty"`
}

type projectAssistantSandboxReadWireFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Bytes   int    `json:"bytes"`
	Digest  string `json:"digest"`
}

type projectAssistantSandboxReadWireResponse struct {
	Files          []projectAssistantSandboxReadWireFile `json:"files"`
	SourceRevision uint64                                `json:"sourceRevision,omitempty"`
	SourceDigest   string                                `json:"sourceDigest,omitempty"`
}

type projectAssistantSandboxMutateWireOperation struct {
	Operation string `json:"op"`
	Path      string `json:"path"`
	Content   string `json:"content,omitempty"`
}

type projectAssistantSandboxMutateWireRequest struct {
	ExpectedRevision uint64                                       `json:"expectedRevision"`
	ExpectedDigest   string                                       `json:"expectedDigest"`
	Operations       []projectAssistantSandboxMutateWireOperation `json:"operations"`
	Restart          string                                       `json:"restart,omitempty"`
}

type projectAssistantSandboxMutateWireResponse struct {
	Phase          string   `json:"phase"`
	Changed        []string `json:"changed,omitempty"`
	Deleted        []string `json:"deleted,omitempty"`
	Restarted      bool     `json:"restarted,omitempty"`
	SourceRevision uint64   `json:"sourceRevision"`
	SourceDigest   string   `json:"sourceDigest"`
}

type projectAssistantSandboxDiffWireRequest struct {
	CheckpointID     string `json:"checkpointID,omitempty"`
	ExpectedRevision uint64 `json:"expectedRevision,omitempty"`
	ExpectedDigest   string `json:"expectedDigest,omitempty"`
}

type projectAssistantSandboxDiffWireChange struct {
	Path         string `json:"path"`
	Kind         string `json:"kind"`
	BeforeDigest string `json:"beforeDigest,omitempty"`
	AfterDigest  string `json:"afterDigest,omitempty"`
	BeforeBytes  int    `json:"beforeBytes,omitempty"`
	AfterBytes   int    `json:"afterBytes,omitempty"`
}

type projectAssistantSandboxDiffWireResponse struct {
	BaseRevision   uint64                                  `json:"baseRevision,omitempty"`
	BaseDigest     string                                  `json:"baseDigest,omitempty"`
	SourceRevision uint64                                  `json:"sourceRevision"`
	SourceDigest   string                                  `json:"sourceDigest"`
	Changes        []projectAssistantSandboxDiffWireChange `json:"changes"`
}

type projectAssistantSandboxCheckpointWireRequest struct {
	Action           string `json:"action,omitempty"`
	ID               string `json:"id,omitempty"`
	Label            string `json:"label,omitempty"`
	ExpectedRevision uint64 `json:"expectedRevision,omitempty"`
	ExpectedDigest   string `json:"expectedDigest,omitempty"`
}

type projectAssistantSandboxCheckpointWireSummary struct {
	ID             string `json:"id"`
	Label          string `json:"label,omitempty"`
	SourceRevision uint64 `json:"sourceRevision"`
	SourceDigest   string `json:"sourceDigest"`
	FileCount      int    `json:"fileCount"`
}

type projectAssistantSandboxCheckpointWireResponse struct {
	Action         string                                        `json:"action"`
	Checkpoint     *projectAssistantSandboxCheckpointWireSummary `json:"checkpoint,omitempty"`
	SourceRevision uint64                                        `json:"sourceRevision,omitempty"`
	SourceDigest   string                                        `json:"sourceDigest,omitempty"`
}

// projectAssistantSandboxClient is the only worker-facing abstraction.  The
// implementation uses the existing infrastructure data-plane client, while
// tests and a future alternate transport can provide a focused fake.
type projectAssistantSandboxClient interface {
	Workspace(context.Context, identity, dataPlaneRef, projectAssistantSandboxWorkspaceRequest) (projectAssistantSandboxWorkspaceResponse, error)
	Exec(context.Context, identity, dataPlaneRef, projectSandboxExecRequest) (projectSandboxExecResponse, error)
}

type projectAssistantDataPlaneSandboxClient struct{ server *Server }

func (c projectAssistantDataPlaneSandboxClient) Workspace(ctx context.Context, id identity, ref dataPlaneRef, request projectAssistantSandboxWorkspaceRequest) (projectAssistantSandboxWorkspaceResponse, error) {
	if c.server == nil {
		return projectAssistantSandboxWorkspaceResponse{}, errors.New("assistant sandbox server is not configured")
	}
	switch strings.ToLower(strings.TrimSpace(request.Action)) {
	case "seed":
		return c.workspaceSeed(ctx, id, ref, request)
	case "list":
		return c.workspaceList(ctx, id, ref, request)
	case "read":
		return c.workspaceRead(ctx, id, ref, request)
	case "glob":
		return c.workspaceGlob(ctx, id, ref, request)
	case "grep":
		return c.workspaceGrep(ctx, id, ref, request)
	case "create", "replace", "edit", "delete", "move":
		return c.workspaceMutate(ctx, id, ref, request)
	case "checkpoint":
		if strings.EqualFold(strings.TrimSpace(request.CheckpointAction), "create") {
			return c.workspaceCheckpointCreate(ctx, id, ref, request)
		}
		return c.workspaceCheckpointDiff(ctx, id, ref, request)
	default:
		return projectAssistantSandboxWorkspaceResponse{}, fmt.Errorf("%w: unsupported workspace action %q", errProjectAssistantRunSandboxConflict, request.Action)
	}
}

func (c projectAssistantDataPlaneSandboxClient) workspaceSeed(ctx context.Context, id identity, ref dataPlaneRef, request projectAssistantSandboxWorkspaceRequest) (projectAssistantSandboxWorkspaceResponse, error) {
	// Deliberately omit sourceRevision/sourceDigest. The worker owns its
	// monotonic revision domain and advances the currently applied manifest
	// while recomputing the digest from this complete authoritative snapshot.
	body, status, err := c.workspaceCall(ctx, id, ref, "seed", struct {
		Files   []projectSandboxSyncFile `json:"files"`
		Restart string                   `json:"restart,omitempty"`
	}{Files: request.Files, Restart: "auto"}, 1<<20)
	if err != nil {
		return projectAssistantSandboxWorkspaceResponse{}, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return projectAssistantSandboxWorkspaceResponse{}, sandboxWorkspaceHTTPError("seed", status, body)
	}
	var wire projectAssistantSandboxSeedWireResponse
	if err := json.Unmarshal(body, &wire); err != nil {
		return projectAssistantSandboxWorkspaceResponse{}, fmt.Errorf("decode sandbox workspace seed response: %w", err)
	}
	return projectAssistantSandboxWorkspaceResponse{
		Status:         strings.ToLower(strings.TrimSpace(wire.Phase)),
		SourceRevision: wire.SourceRevision,
		SourceDigest:   sandboxSourceDigest(wire.SourceDigest),
	}, nil
}

func (c projectAssistantDataPlaneSandboxClient) workspaceCall(ctx context.Context, id identity, ref dataPlaneRef, operation string, payload any, maxBytes int64) ([]byte, int, error) {
	if c.server == nil {
		return nil, 0, errors.New("assistant sandbox server is not configured")
	}
	if strings.TrimSpace(operation) == "" {
		return nil, 0, errors.New("assistant sandbox workspace operation is required")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, fmt.Errorf("encode sandbox workspace %s request: %w", operation, err)
	}
	callCtx, cancel := context.WithTimeout(ctx, dataPlaneCallTimeout)
	defer cancel()
	req, err := c.server.newDataPlaneRequest(callCtx, http.MethodPost, id, ref, projectAssistantRunSandboxWorkspaceVerb, "/"+operation, bytes.NewReader(encoded))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.server.sandboxDataPlaneClient(dataPlaneCallTimeout).Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("sandbox workspace %s: %w", operation, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if maxBytes <= 0 {
		maxBytes = 16 << 20
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read sandbox workspace %s response: %w", operation, err)
	}
	if int64(len(body)) > maxBytes {
		return nil, resp.StatusCode, fmt.Errorf("sandbox workspace %s response exceeds %d bytes", operation, maxBytes)
	}
	return body, resp.StatusCode, nil
}

func sandboxWorkspaceHTTPError(operation string, status int, body []byte) error {
	message := strings.TrimSpace(truncateProjectToolInfo(string(body)))
	if status == http.StatusConflict {
		if message == "" {
			message = "remote workspace revision or digest no longer matches"
		}
		return fmt.Errorf("%w: %s", errProjectAssistantRunSandboxConflict, message)
	}
	if status == http.StatusBadGateway || status == http.StatusServiceUnavailable {
		return &projectDevelopmentSyncHTTPError{component: operation, status: status, detail: message}
	}
	return fmt.Errorf("sandbox workspace %s endpoint returned %d: %s", operation, status, truncateProjectToolInfo(string(body)))
}

func sandboxSourceDigest(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return "sha256:" + strings.TrimPrefix(raw, "sha256:")
}

func sandboxDigestEqual(left, right string) bool {
	return strings.TrimPrefix(strings.TrimSpace(left), "sha256:") == strings.TrimPrefix(strings.TrimSpace(right), "sha256:")
}

func sandboxFileVersion(rawDigest string) string {
	if strings.TrimSpace(rawDigest) == "" {
		return ""
	}
	return sandboxSourceDigest(rawDigest)
}

func sandboxWorkspacePath(raw string, directory bool) (string, error) {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if directory && (raw == "" || raw == "." || raw == "/") {
		return ".", nil
	}
	raw = strings.TrimPrefix(raw, "/")
	if raw == "" {
		return "", errors.New("workspace path cannot be empty")
	}
	return workspace.CleanProjectPath(raw)
}

func (c projectAssistantDataPlaneSandboxClient) workspaceList(ctx context.Context, id identity, ref dataPlaneRef, request projectAssistantSandboxWorkspaceRequest) (projectAssistantSandboxWorkspaceResponse, error) {
	base, err := sandboxWorkspacePath(request.Path, true)
	if err != nil {
		return projectAssistantSandboxWorkspaceResponse{}, err
	}
	limit := request.Limit
	if limit <= 0 || limit > workspace.MaxListLimit {
		limit = workspace.MaxListLimit
	}
	body, status, err := c.workspaceCall(ctx, id, ref, "list", projectAssistantSandboxListWireRequest{Path: base, Recursive: true, MaxEntries: limit}, 1<<20)
	if err != nil {
		return projectAssistantSandboxWorkspaceResponse{}, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return projectAssistantSandboxWorkspaceResponse{}, sandboxWorkspaceHTTPError("list", status, body)
	}
	var wire projectAssistantSandboxListWireResponse
	if err := json.Unmarshal(body, &wire); err != nil {
		return projectAssistantSandboxWorkspaceResponse{}, fmt.Errorf("decode sandbox workspace list response: %w", err)
	}
	response := projectAssistantSandboxWorkspaceResponse{
		Status:         "ok",
		SourceRevision: wire.SourceRevision,
		SourceDigest:   sandboxSourceDigest(wire.SourceDigest),
		Entries:        make([]projectAssistantSandboxListEntry, 0, len(wire.Entries)),
		Files:          workspace.FileList{Files: make([]workspace.FileInfo, 0, len(wire.Entries)), Limit: limit},
	}
	for _, entry := range wire.Entries {
		response.Entries = append(response.Entries, projectAssistantSandboxListEntry{Path: entry.Path, Type: entry.Type, Size: entry.Size})
		if strings.EqualFold(entry.Type, "file") {
			response.Files.Files = append(response.Files.Files, workspace.FileInfo{Path: entry.Path, Size: entry.Size})
		}
	}
	response.Files.Truncated = len(wire.Entries) >= limit
	return response, nil
}

func (c projectAssistantDataPlaneSandboxClient) workspaceReadFiles(ctx context.Context, id identity, ref dataPlaneRef, paths []string) (map[string]projectAssistantSandboxReadWireFile, uint64, string, int, error) {
	if len(paths) == 0 {
		return map[string]projectAssistantSandboxReadWireFile{}, 0, "", http.StatusOK, nil
	}
	cleanPaths := make([]string, 0, len(paths))
	seen := map[string]struct{}{}
	for _, raw := range paths {
		clean, err := sandboxWorkspacePath(raw, false)
		if err != nil {
			return nil, 0, "", 0, err
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		cleanPaths = append(cleanPaths, clean)
	}
	sort.Strings(cleanPaths)
	body, status, err := c.workspaceCall(ctx, id, ref, "read", projectAssistantSandboxReadWireRequest{Paths: cleanPaths, MaxBytes: 4 << 20}, 5<<20)
	if err != nil {
		return nil, 0, "", status, err
	}
	if status == http.StatusNotFound {
		return nil, 0, "", status, nil
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return nil, 0, "", status, sandboxWorkspaceHTTPError("read", status, body)
	}
	var wire projectAssistantSandboxReadWireResponse
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, 0, "", status, fmt.Errorf("decode sandbox workspace read response: %w", err)
	}
	files := make(map[string]projectAssistantSandboxReadWireFile, len(wire.Files))
	for _, file := range wire.Files {
		clean, err := sandboxWorkspacePath(file.Path, false)
		if err != nil {
			return nil, 0, "", status, err
		}
		files[clean] = file
	}
	return files, wire.SourceRevision, sandboxSourceDigest(wire.SourceDigest), status, nil
}

func (c projectAssistantDataPlaneSandboxClient) workspaceRead(ctx context.Context, id identity, ref dataPlaneRef, request projectAssistantSandboxWorkspaceRequest) (projectAssistantSandboxWorkspaceResponse, error) {
	clean, err := sandboxWorkspacePath(request.Path, false)
	if err != nil {
		return projectAssistantSandboxWorkspaceResponse{}, err
	}
	files, revision, digest, status, err := c.workspaceReadFiles(ctx, id, ref, []string{clean})
	if err != nil {
		return projectAssistantSandboxWorkspaceResponse{}, err
	}
	if status == http.StatusNotFound || len(files) == 0 {
		return projectAssistantSandboxWorkspaceResponse{}, fmt.Errorf("sandbox workspace file %q was not found", clean)
	}
	file, ok := files[clean]
	if !ok {
		return projectAssistantSandboxWorkspaceResponse{}, fmt.Errorf("sandbox workspace read omitted %q", clean)
	}
	return projectAssistantSandboxWorkspaceResponse{
		Status: "ok", SourceRevision: revision, SourceDigest: digest,
		File: workspace.FileContent{Path: clean, Content: file.Content, Size: int64(file.Bytes), Version: sandboxFileVersion(file.Digest)},
	}, nil
}

func sandboxGlobMatch(pattern, candidate string) bool {
	pattern = strings.TrimPrefix(strings.TrimSpace(pattern), "/")
	candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "/")
	if pattern == "" {
		return true
	}
	var expression strings.Builder
	expression.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					expression.WriteString("(?:.*/)?")
				} else {
					expression.WriteString(".*")
				}
			} else {
				expression.WriteString("[^/]*")
			}
		case '?':
			expression.WriteString("[^/]")
		case '[':
			end := strings.IndexByte(pattern[i+1:], ']')
			if end < 0 {
				return false
			}
			expression.WriteByte('[')
			expression.WriteString(pattern[i+1 : i+1+end])
			expression.WriteByte(']')
			i += end + 1
		default:
			expression.WriteString(regexp.QuoteMeta(string(pattern[i])))
		}
	}
	expression.WriteString("$")
	matched, err := regexp.MatchString(expression.String(), candidate)
	return err == nil && matched
}

func (c projectAssistantDataPlaneSandboxClient) workspaceGlob(ctx context.Context, id identity, ref dataPlaneRef, request projectAssistantSandboxWorkspaceRequest) (projectAssistantSandboxWorkspaceResponse, error) {
	listed, err := c.workspaceList(ctx, id, ref, projectAssistantSandboxWorkspaceRequest{Path: request.Path, Limit: workspace.MaxListLimit})
	if err != nil {
		return projectAssistantSandboxWorkspaceResponse{}, err
	}
	filtered := projectAssistantSandboxWorkspaceResponse{Status: "ok", SourceRevision: listed.SourceRevision, SourceDigest: listed.SourceDigest, Files: workspace.FileList{Limit: workspace.MaxListLimit}}
	for _, entry := range listed.Entries {
		if !strings.EqualFold(entry.Type, "file") || !sandboxGlobMatch(request.Pattern, entry.Path) {
			continue
		}
		filtered.Entries = append(filtered.Entries, entry)
		filtered.Files.Files = append(filtered.Files.Files, workspace.FileInfo{Path: entry.Path, Size: entry.Size})
	}
	return filtered, nil
}

func sandboxFileTypeMatch(filePath, fileType string) bool {
	fileType = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(fileType)), ".")
	if fileType == "" {
		return true
	}
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(filePath)), ".")
	return ext == fileType
}

func (c projectAssistantDataPlaneSandboxClient) workspaceGrep(ctx context.Context, id identity, ref dataPlaneRef, request projectAssistantSandboxWorkspaceRequest) (projectAssistantSandboxWorkspaceResponse, error) {
	pattern := request.GrepPattern
	if strings.TrimSpace(pattern) == "" {
		return projectAssistantSandboxWorkspaceResponse{}, errors.New("grep pattern is required")
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return projectAssistantSandboxWorkspaceResponse{}, fmt.Errorf("invalid grep pattern: %w", err)
	}
	listed, err := c.workspaceList(ctx, id, ref, projectAssistantSandboxWorkspaceRequest{Path: request.Path, Limit: workspace.MaxListLimit})
	if err != nil {
		return projectAssistantSandboxWorkspaceResponse{}, err
	}
	paths := make([]string, 0, len(listed.Entries))
	for _, entry := range listed.Entries {
		if strings.EqualFold(entry.Type, "file") && sandboxFileTypeMatch(entry.Path, request.FileType) && sandboxGlobMatch(request.Glob, entry.Path) {
			paths = append(paths, entry.Path)
		}
	}
	files, revision, digest, _, err := c.workspaceReadFiles(ctx, id, ref, paths)
	if err != nil {
		return projectAssistantSandboxWorkspaceResponse{}, err
	}
	response := projectAssistantSandboxWorkspaceResponse{Status: "ok", SourceRevision: revision, SourceDigest: digest}
	for _, filePath := range paths {
		file, ok := files[filePath]
		if !ok {
			continue
		}
		for lineNumber, line := range strings.Split(file.Content, "\n") {
			if !re.MatchString(line) {
				continue
			}
			response.Matches = append(response.Matches, projectAssistantSandboxGrepMatch{Content: line, Path: filePath, Line: lineNumber + 1})
			if len(response.Matches) >= projectAssistantRunSandboxMaxChanges*8 {
				return response, nil
			}
		}
	}
	return response, nil
}

func (c projectAssistantDataPlaneSandboxClient) workspaceReadForMutation(ctx context.Context, id identity, ref dataPlaneRef, rawPath string) (projectAssistantSandboxReadWireFile, bool, error) {
	path, err := sandboxWorkspacePath(rawPath, false)
	if err != nil {
		return projectAssistantSandboxReadWireFile{}, false, err
	}
	files, _, _, status, err := c.workspaceReadFiles(ctx, id, ref, []string{path})
	if err != nil {
		return projectAssistantSandboxReadWireFile{}, false, err
	}
	if status == http.StatusNotFound {
		return projectAssistantSandboxReadWireFile{}, false, nil
	}
	file, ok := files[path]
	return file, ok, nil
}

func (c projectAssistantDataPlaneSandboxClient) workspaceMutate(ctx context.Context, id identity, ref dataPlaneRef, request projectAssistantSandboxWorkspaceRequest) (projectAssistantSandboxWorkspaceResponse, error) {
	if request.SourceRevision == 0 || strings.TrimSpace(request.SourceDigest) == "" {
		return projectAssistantSandboxWorkspaceResponse{}, fmt.Errorf("%w: remote source revision and digest are required", errProjectAssistantRunSandboxConflict)
	}
	operations := make([]projectAssistantSandboxMutateWireOperation, 0, 2)
	mutation := workspace.MutationResult{Operation: strings.TrimSpace(request.Action) + "_file", Changed: true}
	pathForResult := request.Path
	if request.Action == "move" {
		pathForResult = request.DestinationPath
	}
	cleanPath, err := sandboxWorkspacePath(pathForResult, false)
	if err != nil {
		return projectAssistantSandboxWorkspaceResponse{}, err
	}
	mutation.Path = cleanPath
	if request.Action == "move" {
		source, sourceExists, err := c.workspaceReadForMutation(ctx, id, ref, request.SourcePath)
		if err != nil {
			return projectAssistantSandboxWorkspaceResponse{}, err
		}
		if !sourceExists {
			return projectAssistantSandboxWorkspaceResponse{}, fmt.Errorf("workspace source %q does not exist", request.SourcePath)
		}
		if request.ExpectedVersion != "" && !sandboxDigestEqual(request.ExpectedVersion, source.Digest) {
			return projectAssistantSandboxWorkspaceResponse{}, fmt.Errorf("%w: workspace source %q changed", errProjectAssistantRunSandboxConflict, request.SourcePath)
		}
		_, destinationExists, err := c.workspaceReadForMutation(ctx, id, ref, request.DestinationPath)
		if err != nil {
			return projectAssistantSandboxWorkspaceResponse{}, err
		}
		if destinationExists {
			return projectAssistantSandboxWorkspaceResponse{}, fmt.Errorf("workspace destination %q already exists", request.DestinationPath)
		}
		destination, err := sandboxWorkspacePath(request.DestinationPath, false)
		if err != nil {
			return projectAssistantSandboxWorkspaceResponse{}, err
		}
		operations = append(operations,
			projectAssistantSandboxMutateWireOperation{Operation: "write", Path: destination, Content: source.Content},
			projectAssistantSandboxMutateWireOperation{Operation: "delete", Path: source.Path},
		)
		mutation.PreviousPath = source.Path
		mutation.Paths = []string{source.Path, destination}
	} else {
		current, exists, err := c.workspaceReadForMutation(ctx, id, ref, request.Path)
		if err != nil {
			return projectAssistantSandboxWorkspaceResponse{}, err
		}
		if request.Action == "create" && exists {
			return projectAssistantSandboxWorkspaceResponse{}, fmt.Errorf("workspace file %q already exists", request.Path)
		}
		if request.Action != "create" && !exists {
			return projectAssistantSandboxWorkspaceResponse{}, fmt.Errorf("workspace file %q does not exist", request.Path)
		}
		if request.ExpectedVersion != "" && (!exists || !sandboxDigestEqual(request.ExpectedVersion, current.Digest)) {
			return projectAssistantSandboxWorkspaceResponse{}, fmt.Errorf("%w: workspace file %q changed", errProjectAssistantRunSandboxConflict, request.Path)
		}
		cleanPath, err := sandboxWorkspacePath(request.Path, false)
		if err != nil {
			return projectAssistantSandboxWorkspaceResponse{}, err
		}
		content := request.Content
		if request.Action == "edit" {
			if request.OldString == "" {
				return projectAssistantSandboxWorkspaceResponse{}, errors.New("oldString cannot be empty")
			}
			count := strings.Count(current.Content, request.OldString)
			if count == 0 {
				return projectAssistantSandboxWorkspaceResponse{}, fmt.Errorf("workspace oldString was not found in %q", cleanPath)
			}
			if count > 1 && !request.ReplaceAll {
				return projectAssistantSandboxWorkspaceResponse{}, fmt.Errorf("workspace oldString matched %d times in %q", count, cleanPath)
			}
			if request.ReplaceAll {
				content = strings.ReplaceAll(current.Content, request.OldString, request.NewString)
			} else {
				content = strings.Replace(current.Content, request.OldString, request.NewString, 1)
			}
			mutation.Replacements = count
		}
		if request.Action == "delete" {
			operations = append(operations, projectAssistantSandboxMutateWireOperation{Operation: "delete", Path: cleanPath})
		} else {
			operations = append(operations, projectAssistantSandboxMutateWireOperation{Operation: "write", Path: cleanPath, Content: content})
		}
		mutation.Paths = []string{cleanPath}
		if request.Action == "delete" {
			mutation.Size = 0
		} else {
			mutation.Size = int64(len([]byte(content)))
		}
	}
	payload := projectAssistantSandboxMutateWireRequest{ExpectedRevision: request.SourceRevision, ExpectedDigest: request.SourceDigest, Operations: operations}
	body, status, err := c.workspaceCall(ctx, id, ref, "mutate", payload, 2<<20)
	if err != nil {
		return projectAssistantSandboxWorkspaceResponse{}, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return projectAssistantSandboxWorkspaceResponse{}, sandboxWorkspaceHTTPError("mutate", status, body)
	}
	var wire projectAssistantSandboxMutateWireResponse
	if err := json.Unmarshal(body, &wire); err != nil {
		return projectAssistantSandboxWorkspaceResponse{}, fmt.Errorf("decode sandbox workspace mutate response: %w", err)
	}
	mutation.Changed = len(wire.Changed) > 0 || len(wire.Deleted) > 0
	return projectAssistantSandboxWorkspaceResponse{
		Status: "ok", Mutation: mutation, SourceRevision: wire.SourceRevision, SourceDigest: sandboxSourceDigest(wire.SourceDigest),
	}, nil
}

func (c projectAssistantDataPlaneSandboxClient) workspaceCheckpointCreate(ctx context.Context, id identity, ref dataPlaneRef, request projectAssistantSandboxWorkspaceRequest) (projectAssistantSandboxWorkspaceResponse, error) {
	payload := projectAssistantSandboxCheckpointWireRequest{Action: "create", ID: request.CheckpointID, Label: "app-studio-run-sandbox", ExpectedRevision: request.SourceRevision, ExpectedDigest: request.SourceDigest}
	body, status, err := c.workspaceCall(ctx, id, ref, "checkpoint", payload, 1<<20)
	if err != nil {
		return projectAssistantSandboxWorkspaceResponse{}, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return projectAssistantSandboxWorkspaceResponse{}, sandboxWorkspaceHTTPError("checkpoint", status, body)
	}
	var wire projectAssistantSandboxCheckpointWireResponse
	if err := json.Unmarshal(body, &wire); err != nil {
		return projectAssistantSandboxWorkspaceResponse{}, fmt.Errorf("decode sandbox workspace checkpoint response: %w", err)
	}
	if wire.Checkpoint == nil || strings.TrimSpace(wire.Checkpoint.ID) == "" {
		return projectAssistantSandboxWorkspaceResponse{}, fmt.Errorf("%w: checkpoint endpoint returned no durable checkpoint ID", errProjectAssistantRunSandboxConflict)
	}
	revision := wire.SourceRevision
	if revision == 0 {
		revision = wire.Checkpoint.SourceRevision
	}
	digest := sandboxSourceDigest(wire.SourceDigest)
	if digest == "" {
		digest = sandboxSourceDigest(wire.Checkpoint.SourceDigest)
	}
	return projectAssistantSandboxWorkspaceResponse{Status: "ok", CheckpointID: wire.Checkpoint.ID, SourceRevision: revision, SourceDigest: digest}, nil
}

func (c projectAssistantDataPlaneSandboxClient) workspaceCheckpointDiff(ctx context.Context, id identity, ref dataPlaneRef, request projectAssistantSandboxWorkspaceRequest) (projectAssistantSandboxWorkspaceResponse, error) {
	if strings.TrimSpace(request.CheckpointID) == "" {
		return projectAssistantSandboxWorkspaceResponse{}, fmt.Errorf("%w: remote baseline checkpoint is missing", errProjectAssistantRunSandboxConflict)
	}
	body, status, err := c.workspaceCall(ctx, id, ref, "diff", projectAssistantSandboxDiffWireRequest{CheckpointID: request.CheckpointID, ExpectedRevision: request.SourceRevision, ExpectedDigest: request.SourceDigest}, 2<<20)
	if err != nil {
		return projectAssistantSandboxWorkspaceResponse{}, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return projectAssistantSandboxWorkspaceResponse{}, sandboxWorkspaceHTTPError("diff", status, body)
	}
	var wire projectAssistantSandboxDiffWireResponse
	if err := json.Unmarshal(body, &wire); err != nil {
		return projectAssistantSandboxWorkspaceResponse{}, fmt.Errorf("decode sandbox workspace diff response: %w", err)
	}
	if len(wire.Changes) > projectAssistantRunSandboxMaxChanges {
		return projectAssistantSandboxWorkspaceResponse{}, fmt.Errorf("%w: checkpoint contains too many files", errProjectAssistantRunSandboxConflict)
	}
	readPaths := make([]string, 0, len(wire.Changes))
	for _, change := range wire.Changes {
		if change.Kind == "added" || change.Kind == "modified" {
			readPaths = append(readPaths, change.Path)
		}
	}
	files := map[string]projectAssistantSandboxReadWireFile{}
	if len(readPaths) > 0 {
		var readRevision uint64
		var readDigest string
		files, readRevision, readDigest, _, err = c.workspaceReadFiles(ctx, id, ref, readPaths)
		if err != nil {
			return projectAssistantSandboxWorkspaceResponse{}, err
		}
		// Diff and content reads are separate worker calls. Import only content
		// proven to belong to the exact diff fence; never replace the first call's
		// evidence with a later read that may have observed another mutation.
		if readRevision != wire.SourceRevision || !sandboxDigestEqual(readDigest, wire.SourceDigest) {
			return projectAssistantSandboxWorkspaceResponse{}, fmt.Errorf("%w: workspace changed between checkpoint diff and content read", errProjectAssistantRunSandboxConflict)
		}
	}
	response := projectAssistantSandboxWorkspaceResponse{Status: "ok", CheckpointID: request.CheckpointID, SourceRevision: wire.SourceRevision, SourceDigest: sandboxSourceDigest(wire.SourceDigest)}
	for _, change := range wire.Changes {
		operation := string(workspace.ManagedFileReplace)
		content := ""
		expectedVersion := sandboxFileVersion(change.BeforeDigest)
		switch change.Kind {
		case "added":
			operation = string(workspace.ManagedFileCreate)
			file, ok := files[change.Path]
			if !ok || strings.TrimSpace(change.AfterDigest) == "" || !sandboxDigestEqual(file.Digest, change.AfterDigest) {
				return projectAssistantSandboxWorkspaceResponse{}, fmt.Errorf("%w: diff added file %q was not returned with the proven content digest", errProjectAssistantRunSandboxConflict, change.Path)
			}
			content = file.Content
		case "modified":
			file, ok := files[change.Path]
			if !ok || expectedVersion == "" || strings.TrimSpace(change.AfterDigest) == "" || !sandboxDigestEqual(file.Digest, change.AfterDigest) {
				return projectAssistantSandboxWorkspaceResponse{}, fmt.Errorf("%w: diff modified file %q is missing proven content or baseline evidence", errProjectAssistantRunSandboxConflict, change.Path)
			}
			content = file.Content
		case "deleted":
			operation = string(workspace.ManagedFileDelete)
			if expectedVersion == "" {
				return projectAssistantSandboxWorkspaceResponse{}, fmt.Errorf("%w: diff deleted file %q is missing baseline version", errProjectAssistantRunSandboxConflict, change.Path)
			}
		default:
			return projectAssistantSandboxWorkspaceResponse{}, fmt.Errorf("%w: unsupported remote diff kind %q", errProjectAssistantRunSandboxConflict, change.Kind)
		}
		response.Changes = append(response.Changes, projectAssistantSandboxWorkspaceChange{Path: change.Path, Operation: operation, Content: content, ExpectedVersion: expectedVersion})
	}
	return response, nil
}

func (c projectAssistantDataPlaneSandboxClient) Exec(ctx context.Context, id identity, ref dataPlaneRef, request projectSandboxExecRequest) (projectSandboxExecResponse, error) {
	if c.server == nil {
		return projectSandboxExecResponse{}, errors.New("assistant sandbox server is not configured")
	}
	return projectAssistantExecCall(ctx, c.server, id, ref, request)
}

type projectAssistantRunSandbox struct {
	server   *Server
	client   projectAssistantSandboxClient
	id       identity
	project  *aiv1alpha1.Project
	scope    workspace.Scope
	target   projectDevelopmentSyncTargetInfo
	instance projectAssistantSandboxInstance
	runState *projectEinoAssistantRunState
	mu       sync.Mutex
	metadata projectAssistantRunSandboxMetadata
	closed   bool
}

func projectAssistantRunSandboxForRequest(req projectAssistantToolCallRequest) *projectAssistantRunSandbox {
	if req.RunState == nil {
		return nil
	}
	return req.RunState.Sandbox()
}

func ensureProjectAssistantRunSandboxForRequest(ctx context.Context, req projectAssistantToolCallRequest) (*projectAssistantRunSandbox, error) {
	if req.RunState == nil || !req.RunState.SandboxRemoteEnabled() {
		if req.RunState == nil {
			return nil, nil
		}
		return req.RunState.Sandbox(), nil
	}
	return req.RunState.EnsureSandbox(ctx)
}

func (b *projectAssistantRunSandbox) metadataSnapshot() projectAssistantRunSandboxMetadata {
	if b == nil {
		return projectAssistantRunSandboxMetadata{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.metadata
}

func (b *projectAssistantRunSandbox) touch() error {
	if b == nil {
		return errProjectAssistantRunSandboxClosed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || strings.EqualFold(b.metadata.Status, "closed") {
		return errProjectAssistantRunSandboxClosed
	}
	now := time.Now().UTC()
	if !b.metadata.HardExpiresAt.IsZero() && now.After(b.metadata.HardExpiresAt) {
		b.metadata.Status = "expired"
		return fmt.Errorf("%w: sandbox hard lifetime has expired", errProjectAssistantRunSandboxConflict)
	}
	if !b.metadata.IdleExpiresAt.IsZero() && now.After(b.metadata.IdleExpiresAt) {
		b.metadata.Status = "expired"
		return fmt.Errorf("%w: sandbox idle lifetime has expired", errProjectAssistantRunSandboxConflict)
	}
	b.metadata.LastActivityAt = now
	b.metadata.IdleExpiresAt = now.Add(projectAssistantRunSandboxIdleTTL)
	if !b.metadata.HardExpiresAt.IsZero() && b.metadata.IdleExpiresAt.After(b.metadata.HardExpiresAt) {
		b.metadata.IdleExpiresAt = b.metadata.HardExpiresAt
	}
	return nil
}

func (b *projectAssistantRunSandbox) request(ctx context.Context, req projectAssistantSandboxWorkspaceRequest) (projectAssistantSandboxWorkspaceResponse, error) {
	if b == nil || b.client == nil {
		return projectAssistantSandboxWorkspaceResponse{}, errors.New("assistant sandbox client is not configured")
	}
	if err := b.touch(); err != nil {
		return projectAssistantSandboxWorkspaceResponse{}, err
	}
	meta := b.metadataSnapshot()
	req.SourceRevision = meta.RemoteRevision
	if req.SourceRevision == 0 {
		req.SourceRevision = meta.SourceRevision
	}
	req.SourceDigest = meta.RemoteDigest
	if req.SourceDigest == "" {
		req.SourceDigest = meta.SourceDigest
	}
	response, err := b.client.Workspace(ctx, b.id, b.target.dataPlaneRefFor("workspace"), req)
	if err != nil {
		return response, err
	}
	b.mu.Lock()
	// Keep the durable FileStore fence separate from the remote worker fence;
	// every remote compare-and-swap advances the latter before checkpoint.
	if response.SourceRevision != 0 {
		b.metadata.RemoteRevision = response.SourceRevision
	}
	if strings.TrimSpace(response.SourceDigest) != "" {
		b.metadata.RemoteDigest = strings.TrimSpace(response.SourceDigest)
	}
	if strings.TrimSpace(response.CheckpointID) != "" {
		b.metadata.RemoteCheckpointID = strings.TrimSpace(response.CheckpointID)
	}
	b.metadata.LastActivityAt = time.Now().UTC()
	b.metadata.IdleExpiresAt = b.metadata.LastActivityAt.Add(projectAssistantRunSandboxIdleTTL)
	if !b.metadata.HardExpiresAt.IsZero() && b.metadata.IdleExpiresAt.After(b.metadata.HardExpiresAt) {
		b.metadata.IdleExpiresAt = b.metadata.HardExpiresAt
	}
	b.mu.Unlock()
	if b.runState != nil {
		b.runState.SetSandboxMetadata(b.metadataSnapshot())
	}
	return response, nil
}

func (b *projectAssistantRunSandbox) read(ctx context.Context, path string) (workspace.FileContent, error) {
	response, err := b.request(ctx, projectAssistantSandboxWorkspaceRequest{Action: "read", Path: path})
	return response.File, err
}

func (b *projectAssistantRunSandbox) list(ctx context.Context, path string, limit int) (projectAssistantSandboxWorkspaceResponse, error) {
	return b.request(ctx, projectAssistantSandboxWorkspaceRequest{Action: "list", Path: path, Limit: limit})
}

func (b *projectAssistantRunSandbox) mutate(ctx context.Context, request projectAssistantSandboxWorkspaceRequest) (workspace.MutationResult, error) {
	response, err := b.request(ctx, request)
	return response.Mutation, err
}

func (b *projectAssistantRunSandbox) exec(ctx context.Context, ref dataPlaneRef, request projectSandboxExecRequest) (projectSandboxExecResponse, error) {
	if b == nil || b.client == nil {
		return projectSandboxExecResponse{}, errors.New("assistant sandbox client is not configured")
	}
	if err := b.touch(); err != nil {
		return projectSandboxExecResponse{}, err
	}
	meta := b.metadataSnapshot()
	if strings.EqualFold(strings.TrimSpace(request.Action), "start") {
		request.SourceRevision = meta.RemoteRevision
		if request.SourceRevision == 0 {
			request.SourceRevision = meta.SourceRevision
		}
		request.SourceDigest = meta.RemoteDigest
		if request.SourceDigest == "" {
			request.SourceDigest = meta.SourceDigest
		}
	} else {
		// Poll/cancel identify an existing bounded process. They must not carry
		// a stale source fence from the start request.
		request.SourceRevision = 0
		request.SourceDigest = ""
	}
	var response projectSandboxExecResponse
	var err error
	if strings.EqualFold(strings.TrimSpace(request.Action), "start") {
		response, err = retryProjectAssistantExecStart(ctx, request, func(startCtx context.Context, startRequest projectSandboxExecRequest) (projectSandboxExecResponse, error) {
			return b.client.Exec(startCtx, b.id, ref, startRequest)
		})
	} else {
		// Poll and cancel are deliberately single-attempt operations. Retrying
		// either could duplicate lifecycle transitions against a live process.
		response, err = b.client.Exec(ctx, b.id, ref, request)
	}
	if b.runState != nil {
		b.runState.SetSandboxMetadata(b.metadataSnapshot())
	}
	return response, err
}

func projectAssistantSandboxRemoteFence(metadata projectAssistantRunSandboxMetadata) (uint64, string) {
	revision := metadata.RemoteRevision
	if revision == 0 {
		revision = metadata.SourceRevision
	}
	digest := metadata.RemoteDigest
	if digest == "" {
		digest = metadata.SourceDigest
	}
	return revision, digest
}

// projectAssistantRunSandboxDirty reports whether the worker has advanced past
// the last source baseline that App Studio checkpointed. The worker revision
// and digest are both part of the fence: either one changing is enough to make
// a preview/runtime observation stale. A missing remote fence is treated as
// clean because it means no worker mutation has been observed yet; the normal
// checkpoint path still fails closed when a dirty worker cannot provide a
// durable baseline.
func projectAssistantRunSandboxDirty(metadata projectAssistantRunSandboxMetadata) bool {
	remoteRevision, remoteDigest := projectAssistantSandboxRemoteFence(metadata)
	if remoteRevision == 0 && strings.TrimSpace(remoteDigest) == "" {
		return false
	}
	if remoteRevision != metadata.SourceRevision {
		return true
	}
	return !sandboxDigestEqual(remoteDigest, metadata.SourceDigest)
}

// checkpointIfDirty performs the one bounded remote-diff -> FileStore
// transaction used by same-turn verification. It intentionally does not
// checkpoint a debugging/read-only run: a sandbox may be retained across a
// permission interrupt or resume, but that does not grant the current run
// mutation authority. The bool reports whether a dirty sandbox was handled
// (including a failed attempt), so callers can distinguish a clean/no-op from
// a fail-closed checkpoint conflict.
func (b *projectAssistantRunSandbox) checkpointIfDirty(ctx context.Context, req projectAssistantRunRequest) (bool, error) {
	if b == nil {
		return false, nil
	}
	metadata := b.metadataSnapshot()
	if status := strings.TrimSpace(metadata.Status); status != "" && !strings.EqualFold(status, "active") {
		return false, nil
	}
	if b.runState != nil && !projectAssistantTurnProfileAllowsMutation(b.runState.TurnProfile()) {
		return false, nil
	}
	if !projectAssistantRunSandboxDirty(metadata) {
		return false, nil
	}
	if req.Workspace == nil && b.server != nil {
		req.Workspace = b.server.workspaces
	}
	if req.Workspace == nil {
		return true, fmt.Errorf("%w: project workspace store is not configured", errProjectAssistantRunSandboxConflict)
	}
	if b.runState == nil {
		return true, fmt.Errorf("%w: run mutation state is not configured", errProjectAssistantRunSandboxConflict)
	}
	if revision, _ := b.runState.SourceMutationRevisions(); revision == 0 {
		return true, fmt.Errorf("%w: source mutation revision is unavailable", errProjectAssistantRunSandboxConflict)
	}
	if err := b.checkpoint(ctx, req); err != nil {
		return true, err
	}
	return true, nil
}

// checkpointProjectAssistantRunSandboxIfDirty is the server-owned bridge used
// by preview and runtime tools. Tool requests intentionally do not carry a
// workspace store or credentials; the active sandbox supplies its immutable
// tenant/project scope, while Server supplies the authoritative FileStore.
func (s *Server) checkpointProjectAssistantRunSandboxIfDirty(ctx context.Context, runState *projectEinoAssistantRunState) (bool, error) {
	if s == nil || runState == nil {
		return false, nil
	}
	sandbox := runState.Sandbox()
	if sandbox == nil {
		return false, nil
	}
	return sandbox.checkpointIfDirty(ctx, projectAssistantRunRequest{
		Identity:       sandbox.id,
		Project:        sandbox.project,
		WorkspaceScope: sandbox.scope,
		Workspace:      s.workspaces,
	})
}

func projectAssistantRunSandboxCheckpointFailure(err error) string {
	if err == nil {
		return "the current workspace mutation could not be checkpointed into the run sandbox"
	}
	reason := strings.TrimSpace(err.Error())
	if errors.Is(err, errProjectAssistantRunSandboxConflict) {
		return "the current workspace mutation is not current because the run sandbox checkpoint conflicted: " + reason
	}
	return "the current workspace mutation is not current because the run sandbox checkpoint failed: " + reason
}

func projectAssistantSandboxTargetFromTemplate(info projectTemplateInfo, name string) projectDevelopmentSyncTargetInfo {
	return projectDevelopmentSyncTargetInfo{
		EnvironmentName:    projectAssistantRunSandboxEnvironment,
		BindingName:        projectAssistantRunSandboxBinding,
		Provider:           infraDataPlaneProvider,
		ResourceName:       name,
		Resource:           projectAssistantRunSandboxResource,
		Kind:               projectAssistantRunSandboxKind,
		APIVersion:         projectAssistantRunSandboxAPIVersion,
		Components:         info.Components,
		PreviewAccessModes: nil,
	}
}

func projectAssistantRunSandboxAnnotationTime(instance *unstructured.Unstructured, key string) (time.Time, bool) {
	if instance == nil {
		return time.Time{}, false
	}
	raw := strings.TrimSpace(instance.GetAnnotations()[key])
	if raw == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

// projectAssistantRunSandboxInstanceClaimed treats a claim without a valid
// expiry as held. A malformed/missing expiry must fail closed: evicting such
// an Instance could terminate a suspended run owned by another replica.
func projectAssistantRunSandboxInstanceClaimed(instance *unstructured.Unstructured, now time.Time) bool {
	if instance == nil {
		return false
	}
	owner := strings.TrimSpace(instance.GetAnnotations()[projectAssistantRunSandboxClaimOwner])
	if owner == "" {
		return false
	}
	expiresAt, ok := projectAssistantRunSandboxAnnotationTime(instance, projectAssistantRunSandboxClaimExpiry)
	return !ok || now.Before(expiresAt)
}

func projectAssistantRunSandboxInstanceLastActivity(instance *unstructured.Unstructured) time.Time {
	if last, ok := projectAssistantRunSandboxAnnotationTime(instance, projectAssistantRunSandboxLastActivity); ok {
		return last
	}
	if instance != nil {
		created := instance.GetCreationTimestamp()
		if !created.IsZero() {
			return created.Time
		}
	}
	return time.Time{}
}

func minProjectAssistantRunSandboxExpiry(left, right time.Time) time.Time {
	if left.IsZero() {
		return right
	}
	if right.IsZero() || left.Before(right) {
		return left
	}
	return right
}

func projectAssistantRunSandboxInstanceCached(instance *unstructured.Unstructured, now time.Time) bool {
	if instance == nil || instance.GetDeletionTimestamp() != nil || projectAssistantRunSandboxInstanceExpired(instance, now) {
		return false
	}
	if projectAssistantRunSandboxInstanceClaimed(instance, now) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(instance.GetAnnotations()[projectAssistantRunSandboxCacheState]), projectAssistantRunSandboxCacheStateCached)
}

// claimProjectAssistantRunSandboxInstance persists the active run identity for
// crash recovery, suspended-run reattachment, and safe quota eviction. The
// in-process sandbox manager provides exclusivity in the currently enforced
// single-writer deployment; GraphQL applyYaml is create-or-update rather than
// a compare-and-swap operation, so these annotations must not be described as
// a distributed lock.
func (s *Server) claimProjectAssistantRunSandboxInstance(ctx context.Context, c *asclient.Client, scope store.Scope, name, runID string) (time.Time, error) {
	if c == nil {
		return time.Time{}, errors.New("project client is not configured")
	}
	name = strings.TrimSpace(name)
	runID = strings.TrimSpace(runID)
	if name == "" || runID == "" {
		return time.Time{}, errors.New("run sandbox instance name and run ID are required")
	}
	resource := c.Resource(runSandboxInstancesResource, "")
	for attempt := 0; attempt < 3; attempt++ {
		instance, err := resource.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return time.Time{}, fmt.Errorf("get run sandbox instance %q for claim: %w", name, err)
		}
		now := time.Now().UTC()
		annotations := instance.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		} else {
			copy := make(map[string]string, len(annotations)+6)
			for key, value := range annotations {
				copy[key] = value
			}
			annotations = copy
		}
		if strings.EqualFold(strings.TrimSpace(annotations[projectAssistantRunSandboxCacheState]), projectAssistantRunSandboxCacheStateEvicting) {
			return time.Time{}, fmt.Errorf("%w: run sandbox instance %q is being evicted", errProjectAssistantRunSandboxConflict, name)
		}
		owner := strings.TrimSpace(annotations[projectAssistantRunSandboxClaimOwner])
		claimExpiry, claimExpiryOK := projectAssistantRunSandboxAnnotationTime(instance, projectAssistantRunSandboxClaimExpiry)
		if owner != "" && owner != runID && (!claimExpiryOK || now.Before(claimExpiry)) {
			reclaimable, reclaimErr := s.projectAssistantRunSandboxClaimOwnerReclaimable(ctx, scope, owner)
			if reclaimErr != nil {
				return time.Time{}, fmt.Errorf("verify run sandbox instance %q claim owner %q: %w", name, owner, reclaimErr)
			}
			if !reclaimable {
				return time.Time{}, fmt.Errorf("%w: run sandbox instance %q is claimed by another run", errProjectAssistantRunSandboxConflict, name)
			}
		}
		hardExpiry, hardExpiryOK := projectAssistantRunSandboxAnnotationTime(instance, projectAssistantRunSandboxHardExpiry)
		if !hardExpiryOK {
			hardExpiry = now.Add(projectAssistantRunSandboxHardTTL)
		}
		if !now.Before(hardExpiry) {
			return time.Time{}, fmt.Errorf("%w: sandbox hard lifetime has expired", errProjectAssistantRunSandboxConflict)
		}
		idleExpiry := now.Add(projectAssistantRunSandboxIdleTTL)
		if idleExpiry.After(hardExpiry) {
			idleExpiry = hardExpiry
		}
		annotations[projectAssistantRunSandboxClaimOwner] = runID
		annotations[projectAssistantRunSandboxClaimExpiry] = hardExpiry.Format(time.RFC3339Nano)
		annotations[projectAssistantRunSandboxCacheGeneration] = runID
		annotations[projectAssistantRunSandboxCacheState] = projectAssistantRunSandboxCacheStateActive
		annotations[projectAssistantRunSandboxLastActivity] = now.Format(time.RFC3339Nano)
		annotations[projectAssistantRunSandboxIdleExpiry] = idleExpiry.Format(time.RFC3339Nano)
		if !hardExpiryOK {
			annotations[projectAssistantRunSandboxHardExpiry] = hardExpiry.Format(time.RFC3339Nano)
		}
		instance.SetAnnotations(annotations)
		updated, err := resource.Update(ctx, instance, metav1.UpdateOptions{})
		if err != nil {
			if apierrors.IsConflict(err) {
				continue
			}
			return time.Time{}, fmt.Errorf("claim run sandbox instance %q: %w", name, err)
		}
		if strings.TrimSpace(updated.GetAnnotations()[projectAssistantRunSandboxClaimOwner]) != runID {
			return time.Time{}, fmt.Errorf("%w: run sandbox instance %q did not retain the requested claim", errProjectAssistantRunSandboxConflict, name)
		}
		return hardExpiry, nil
	}
	return time.Time{}, fmt.Errorf("%w: run sandbox instance %q claim raced with another writer", errProjectAssistantRunSandboxConflict, name)
}

// projectAssistantRunSandboxClaimOwnerReclaimable distinguishes an orphaned
// durable annotation from a live or suspended owner. A coordinator can exit
// after terminalizing a run but before it clears the Instance claim. On the
// next process, the in-memory lease map is empty, so the durable run row is the
// authoritative recovery fence: terminal (or already-retained-away) owners no
// longer have execution authority, while every non-terminal owner remains
// protected for resume.
func (s *Server) projectAssistantRunSandboxClaimOwnerReclaimable(ctx context.Context, scope store.Scope, owner string) (bool, error) {
	if s == nil || s.store == nil || strings.TrimSpace(owner) == "" {
		return false, nil
	}
	run, err := s.store.GetAssistantRun(ctx, scope, owner)
	if err == nil {
		return assistantRunTerminal(run.Status), nil
	}
	if errors.Is(err, store.ErrAssistantRunNotFound) {
		return true, nil
	}
	return false, err
}

func (s *Server) projectAssistantRunSandboxClient() projectAssistantSandboxClient {
	if s != nil && s.runSandboxClientFactory != nil {
		return s.runSandboxClientFactory(s)
	}
	return projectAssistantDataPlaneSandboxClient{server: s}
}

func (s *Server) setupProjectAssistantRunSandbox(ctx context.Context, req projectAssistantRunRequest, runState *projectEinoAssistantRunState, checkpoint *projectAssistantSandboxCheckpoint) (*projectAssistantRunSandbox, func(), error) {
	if s != nil && s.runSandboxSetupFactory != nil {
		return s.runSandboxSetupFactory(ctx, req, runState, checkpoint)
	}
	if checkpoint != nil {
		return s.attachProjectAssistantRunSandbox(ctx, req, runState, checkpoint)
	}
	return s.ensureProjectAssistantRunSandbox(ctx, req, runState)
}

func (s *Server) ensureProjectAssistantRunSandbox(
	ctx context.Context,
	req projectAssistantRunRequest,
	runState *projectEinoAssistantRunState,
) (*projectAssistantRunSandbox, func(), error) {
	eligibility := s.ResolveCodingSandboxEligibility(ctx, req.Identity, req.WorkspaceScope)
	if !eligibility.Eligible {
		return nil, func() {}, nil
	}
	if s == nil || req.Client == nil || req.Project == nil || req.Workspace == nil {
		return nil, nil, errors.New("run sandbox requires project client, project, and workspace store")
	}
	runID := projectAssistantRunID(req)
	if runID == "" {
		return nil, nil, errors.New("run sandbox requires a durable assistant run ID")
	}
	name := projectAssistantRunSandboxName(req.WorkspaceScope, req.Project, runID)
	manager := s.projectAssistantSandboxManager()
	release, err := manager.acquire(projectAssistantRunSandboxTenantKey(req.Identity, req.WorkspaceScope), name, runID)
	if err != nil {
		return nil, nil, err
	}
	rollback := func() { release() }
	templateName := strings.TrimSpace(getenv(projectAssistantRunSandboxTemplateEnv))
	if templateName == "" {
		templateName = projectAssistantRunSandboxDefaultTemplate
	}
	if templateName != projectAssistantRunSandboxDefaultTemplate {
		rollback()
		return nil, nil, fmt.Errorf("run sandbox requires template %q", projectAssistantRunSandboxDefaultTemplate)
	}
	info, err := fetchProjectTemplate(ctx, req.Client, templateName)
	if err != nil {
		rollback()
		return nil, nil, fmt.Errorf("resolve run sandbox template %q: %w", templateName, err)
	}
	if len(info.Components) == 0 {
		rollback()
		return nil, nil, fmt.Errorf("run sandbox template %q has no development components", templateName)
	}
	if err := s.enforceProjectAssistantRunSandboxQuota(ctx, req.Client, name); err != nil {
		rollback()
		return nil, nil, err
	}
	createdInstance, err := s.ensureProjectAssistantRunSandboxInstance(ctx, req.Client, req.Project, name, templateName)
	if err != nil {
		rollback()
		return nil, nil, err
	}
	// A reused project cache is not ours to delete until its durable claim has
	// succeeded. A newly created instance is safe to remove on an early claim
	// failure; an existing cache may belong to another run.
	cleanupInstance := createdInstance
	defer func() {
		if cleanupInstance {
			_ = req.Client.Resource(runSandboxInstancesResource, "").Delete(context.Background(), name, metav1.DeleteOptions{})
		}
	}()
	hardExpiry, err := s.claimProjectAssistantRunSandboxInstance(ctx, req.Client, req.MessageScope, name, runID)
	if err != nil {
		rollback()
		return nil, nil, err
	}
	cleanupInstance = true
	target := projectAssistantSandboxTargetFromTemplate(info, name)
	component, ok := target.Components["workspace"]
	if !ok || path.Clean(strings.TrimSpace(component.WorkspacePath)) != "." {
		rollback()
		return nil, nil, fmt.Errorf("run sandbox template %q does not declare the workspace component", templateName)
	}
	readyCtx, readyCancel := context.WithTimeout(ctx, projectAssistantRunSandboxReadyTimeout)
	defer readyCancel()
	if err := s.waitForProjectAssistantRunSandboxInstanceReady(readyCtx, req.Client, target); err != nil {
		rollback()
		return nil, nil, fmt.Errorf("wait for run sandbox instance %q: %w", name, err)
	}
	snapshot, err := s.projectWorkspaceSyncFiles(readyCtx, req.WorkspaceScope)
	if err != nil {
		rollback()
		return nil, nil, fmt.Errorf("snapshot run sandbox source: %w", err)
	}
	client := s.projectAssistantRunSandboxClient()
	seedDigest := projectSandboxSyncDigest(snapshot.Files)
	var remoteRevision uint64
	var remoteDigest string
	if err := retryProjectAssistantRunSandboxSeed(readyCtx, projectAssistantRunSandboxReadyTimeout, projectAssistantRunSandboxReadyPoll, func(seedCtx context.Context) error {
		var reconcileErr error
		remoteRevision, remoteDigest, reconcileErr = reconcileProjectAssistantRunSandboxSource(seedCtx, client, req.Identity, target.dataPlaneRefFor("workspace"), snapshot.Files, seedDigest)
		return reconcileErr
	}); err != nil {
		rollback()
		return nil, nil, fmt.Errorf("seed run sandbox: %w", err)
	}
	// The worker checkpoint is the durable remote baseline.  It survives
	// coordinator restarts on the sandbox workspace volume and lets /diff
	// return before-digests while App Studio reads complete after-bytes before
	// applying an atomic FileStore transaction.
	baseline, err := client.Workspace(readyCtx, req.Identity, target.dataPlaneRefFor("workspace"), projectAssistantSandboxWorkspaceRequest{
		Action: "checkpoint", CheckpointAction: "create",
		SourceRevision: remoteRevision, SourceDigest: remoteDigest,
	})
	if err != nil {
		rollback()
		return nil, nil, fmt.Errorf("create run sandbox baseline: %w", err)
	}
	if strings.TrimSpace(baseline.CheckpointID) == "" {
		rollback()
		return nil, nil, fmt.Errorf("%w: run sandbox baseline checkpoint ID is empty", errProjectAssistantRunSandboxConflict)
	}
	if baseline.SourceRevision != remoteRevision || !sandboxDigestEqual(baseline.SourceDigest, remoteDigest) {
		rollback()
		return nil, nil, fmt.Errorf("%w: run sandbox baseline does not match the reconciled remote source fence", errProjectAssistantRunSandboxConflict)
	}
	now := time.Now().UTC()
	sandbox := &projectAssistantRunSandbox{
		server: s, client: client, id: req.Identity,
		project: req.Project.DeepCopy(), scope: req.WorkspaceScope, target: target,
		instance: projectAssistantSandboxInstance{APIVersion: target.APIVersion, Kind: target.Kind, Resource: target.Resource, Name: target.ResourceName},
		runState: runState,
		metadata: projectAssistantRunSandboxMetadata{
			Version: 3, Status: "active", RunID: runID,
			OrgUUID: req.WorkspaceScope.OrgUUID, WorkspaceUUID: req.WorkspaceScope.WorkspaceUUID,
			ProjectName: req.WorkspaceScope.ProjectName, ProjectUID: req.WorkspaceScope.ProjectUID,
			Template:            templateName,
			ProviderExportPath:  eligibility.ProviderExportPath,
			TransportGeneration: eligibility.TransportGeneration,
			Instance:            projectAssistantSandboxInstance{APIVersion: target.APIVersion, Kind: target.Kind, Resource: target.Resource, Name: target.ResourceName},
			SourceRevision:      snapshot.SourceRevision,
			SourceDigest:        seedDigest,
			RemoteRevision:      remoteRevision,
			RemoteDigest:        remoteDigest,
			RemoteCheckpointID:  baseline.CheckpointID,
			CacheGeneration:     runID,
			CreatedAt:           now, LastActivityAt: now,
			IdleExpiresAt: minProjectAssistantRunSandboxExpiry(now.Add(projectAssistantRunSandboxIdleTTL), hardExpiry), HardExpiresAt: hardExpiry,
		},
	}
	if runState != nil {
		runState.SetSandbox(sandbox)
	}
	cleanupInstance = false
	return sandbox, release, nil
}

func (s *Server) attachProjectAssistantRunSandbox(
	ctx context.Context,
	req projectAssistantRunRequest,
	runState *projectEinoAssistantRunState,
	checkpoint *projectAssistantSandboxCheckpoint,
) (*projectAssistantRunSandbox, func(), error) {
	eligibility := s.ResolveCodingSandboxEligibility(ctx, req.Identity, req.WorkspaceScope)
	if !eligibility.Eligible {
		return nil, func() {}, nil
	}
	// Checkpoints created before run sandboxes were enabled intentionally have
	// no sandbox metadata. Resume them on the legacy execution path instead of
	// turning a rollout into an incompatibility for already-suspended runs.
	if checkpoint == nil {
		return nil, func() {}, nil
	}
	if s == nil || req.Client == nil || req.Project == nil {
		return nil, nil, errors.New("resuming a run sandbox requires project client, project, and checkpoint metadata")
	}
	metadata := checkpoint.Metadata
	if strings.TrimSpace(metadata.ProviderExportPath) == "" || strings.TrimSpace(metadata.TransportGeneration) == "" {
		return nil, nil, fmt.Errorf("%w: checkpoint does not contain provider transport identity", errProjectAssistantRunSandboxConflict)
	}
	if metadata.ProviderExportPath != eligibility.ProviderExportPath || metadata.TransportGeneration != eligibility.TransportGeneration {
		return nil, nil, fmt.Errorf("%w: coding sandbox provider export or transport generation changed", errProjectAssistantRunSandboxConflict)
	}
	if strings.TrimSpace(metadata.RunID) == "" || metadata.RunID != projectAssistantRunID(req) {
		return nil, nil, fmt.Errorf("%w: checkpoint run identity does not match", errProjectAssistantRunSandboxConflict)
	}
	if metadata.OrgUUID != req.WorkspaceScope.OrgUUID || metadata.WorkspaceUUID != req.WorkspaceScope.WorkspaceUUID || metadata.ProjectUID != req.WorkspaceScope.ProjectUID {
		return nil, nil, fmt.Errorf("%w: checkpoint tenant or project identity does not match", errProjectAssistantRunSandboxConflict)
	}
	if metadata.HardExpiresAt.IsZero() || time.Now().UTC().After(metadata.HardExpiresAt) {
		return nil, nil, fmt.Errorf("%w: sandbox hard lifetime has expired", errProjectAssistantRunSandboxConflict)
	}
	if !metadata.IdleExpiresAt.IsZero() && time.Now().UTC().After(metadata.IdleExpiresAt) {
		return nil, nil, fmt.Errorf("%w: sandbox idle lifetime has expired", errProjectAssistantRunSandboxConflict)
	}
	if strings.TrimSpace(metadata.RemoteCheckpointID) == "" {
		return nil, nil, fmt.Errorf("%w: checkpoint does not contain a durable remote workspace baseline", errProjectAssistantRunSandboxConflict)
	}
	if strings.TrimSpace(metadata.CacheGeneration) == "" {
		return nil, nil, fmt.Errorf("%w: checkpoint does not contain a cache generation fence", errProjectAssistantRunSandboxConflict)
	}
	templateName := strings.TrimSpace(metadata.Template)
	if templateName != projectAssistantRunSandboxDefaultTemplate {
		return nil, nil, fmt.Errorf("%w: checkpoint template must be %q", errProjectAssistantRunSandboxConflict, projectAssistantRunSandboxDefaultTemplate)
	}
	info, err := fetchProjectTemplate(ctx, req.Client, templateName)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve checkpoint sandbox template %q: %w", templateName, err)
	}
	target := projectAssistantSandboxTargetFromTemplate(info, metadata.Instance.Name)
	if target.ResourceName == "" {
		return nil, nil, fmt.Errorf("%w: checkpoint instance name is empty", errProjectAssistantRunSandboxConflict)
	}
	manager := s.projectAssistantSandboxManager()
	release, err := manager.acquire(projectAssistantRunSandboxTenantKey(req.Identity, req.WorkspaceScope), target.ResourceName, metadata.RunID)
	if err != nil {
		return nil, nil, err
	}
	rollback := func() { release() }
	component, ok := target.Components["workspace"]
	if !ok || path.Clean(strings.TrimSpace(component.WorkspacePath)) != "." {
		rollback()
		return nil, nil, fmt.Errorf("%w: checkpoint template does not declare the workspace component", errProjectAssistantRunSandboxConflict)
	}
	if err := s.enforceProjectAssistantRunSandboxQuota(ctx, req.Client, target.ResourceName); err != nil {
		rollback()
		return nil, nil, err
	}
	createdInstance, err := s.ensureProjectAssistantRunSandboxInstance(ctx, req.Client, req.Project, target.ResourceName, templateName)
	if err != nil {
		rollback()
		return nil, nil, err
	}
	cleanupInstance := createdInstance
	defer func() {
		if cleanupInstance {
			_ = req.Client.Resource(runSandboxInstancesResource, "").Delete(context.Background(), target.ResourceName, metav1.DeleteOptions{})
		}
	}()
	instance, err := req.Client.Resource(runSandboxInstancesResource, "").Get(ctx, target.ResourceName, metav1.GetOptions{})
	if err != nil {
		rollback()
		return nil, nil, fmt.Errorf("get checkpoint run sandbox instance %q: %w", target.ResourceName, err)
	}
	annotations := instance.GetAnnotations()
	if strings.TrimSpace(annotations[projectAssistantRunSandboxCacheGeneration]) != metadata.CacheGeneration || strings.TrimSpace(annotations[projectAssistantRunSandboxClaimOwner]) != metadata.RunID {
		rollback()
		return nil, nil, fmt.Errorf("%w: checkpoint cache generation or claim is no longer current", errProjectAssistantRunSandboxConflict)
	}
	hardExpiry, err := s.claimProjectAssistantRunSandboxInstance(ctx, req.Client, req.MessageScope, target.ResourceName, metadata.RunID)
	if err != nil {
		rollback()
		return nil, nil, err
	}
	cleanupInstance = true
	if err := s.waitForProjectAssistantRunSandboxInstanceReady(ctx, req.Client, target); err != nil {
		rollback()
		return nil, nil, fmt.Errorf("wait for checkpoint run sandbox instance %q: %w", target.ResourceName, err)
	}
	sandbox := &projectAssistantRunSandbox{
		server: s, client: s.projectAssistantRunSandboxClient(), id: req.Identity,
		project: req.Project.DeepCopy(), scope: req.WorkspaceScope, target: target,
		instance: projectAssistantSandboxInstance{APIVersion: target.APIVersion, Kind: target.Kind, Resource: target.Resource, Name: target.ResourceName}, runState: runState, metadata: metadata,
	}
	sandbox.metadata.HardExpiresAt = hardExpiry
	sandbox.metadata.IdleExpiresAt = minProjectAssistantRunSandboxExpiry(time.Now().UTC().Add(projectAssistantRunSandboxIdleTTL), hardExpiry)
	sandbox.metadata.LastActivityAt = time.Now().UTC()
	if runState != nil {
		runState.SetSandbox(sandbox)
	}
	cleanupInstance = false
	return sandbox, release, nil
}

func (s *Server) ensureProjectAssistantRunSandboxInstance(ctx context.Context, c *asclient.Client, project *aiv1alpha1.Project, name, template string) (bool, error) {
	if c == nil {
		return false, errors.New("project client is not configured")
	}
	obj, err := c.Resource(runSandboxInstancesResource, "").Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		if obj.GetAnnotations()["faros.sh/app-studio-run-sandbox"] != "true" {
			return false, fmt.Errorf("run sandbox instance %q is not App Studio-owned", name)
		}
		observed, _, _ := unstructured.NestedString(obj.Object, "spec", "template")
		if strings.TrimSpace(observed) != template {
			return false, fmt.Errorf("run sandbox instance %q uses template %q, want %q", name, observed, template)
		}
		if projectAssistantRunSandboxInstanceExpired(obj, time.Now().UTC()) {
			if projectAssistantRunSandboxInstanceClaimed(obj, time.Now().UTC()) {
				return false, fmt.Errorf("run sandbox instance %q is expired but still claimed", name)
			}
			if deleteErr := c.Resource(runSandboxInstancesResource, "").Delete(ctx, name, metav1.DeleteOptions{}); deleteErr != nil && !apierrors.IsNotFound(deleteErr) {
				return false, fmt.Errorf("delete expired run sandbox instance %q: %w", name, deleteErr)
			}
			if err := waitForProjectAssistantRunSandboxInstanceDeleted(ctx, c, name); err != nil {
				return false, err
			}
			return s.ensureProjectAssistantRunSandboxInstance(ctx, c, project, name, template)
		}
		ownerChanged, ownerErr := ensureProjectAssistantRunSandboxOwner(obj, project)
		if ownerErr != nil {
			return false, ownerErr
		}
		if ownerChanged {
			if _, updateErr := c.Resource(runSandboxInstancesResource, "").Update(ctx, obj, metav1.UpdateOptions{}); updateErr != nil {
				return false, fmt.Errorf("attach Project owner to run sandbox instance %q: %w", name, updateErr)
			}
		}
		return false, nil
	}
	if !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("get run sandbox instance %q: %w", name, err)
	}
	now := time.Now().UTC()
	labels := map[string]string{projectAssistantRunSandboxLabel: "true"}
	if project != nil {
		labels["faros.sh/project"] = dnsSafeSandboxName(project.Name)
	}
	obj = &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": projectAssistantRunSandboxAPIVersion,
		"kind":       projectAssistantRunSandboxKind,
		"metadata": map[string]any{
			"name":   name,
			"labels": labels,
			"annotations": map[string]any{
				projectAssistantRunSandboxLabel:        "true",
				"faros.sh/app-studio-run-sandbox-idle": projectAssistantRunSandboxIdleTTL.String(),
				"faros.sh/app-studio-run-sandbox-hard": projectAssistantRunSandboxHardTTL.String(),
				projectAssistantRunSandboxIdleExpiry:   now.Add(projectAssistantRunSandboxIdleTTL).Format(time.RFC3339Nano),
				projectAssistantRunSandboxHardExpiry:   now.Add(projectAssistantRunSandboxHardTTL).Format(time.RFC3339Nano),
				projectAssistantRunSandboxCacheState:   projectAssistantRunSandboxCacheStateNew,
				projectAssistantRunSandboxLastActivity: now.Format(time.RFC3339Nano),
			},
		},
		"spec": map[string]any{
			"template": template,
			"values": map[string]any{
				"name":      name,
				"farosMode": "development",
			},
		},
	}}
	if _, err := ensureProjectAssistantRunSandboxOwner(obj, project); err != nil {
		return false, err
	}
	if _, err := c.Resource(runSandboxInstancesResource, "").Create(ctx, obj, metav1.CreateOptions{}); err != nil {
		return false, fmt.Errorf("create run sandbox instance %q: %w", name, err)
	}
	return true, nil
}

// deleteProjectAssistantRunSandboxCache removes the project-scoped coding
// environment before deleting the Project. The exact deterministic name also
// covers caches created before owner references were introduced.
func (s *Server) deleteProjectAssistantRunSandboxCache(ctx context.Context, c *asclient.Client, id identity, project *aiv1alpha1.Project) error {
	if c == nil || project == nil {
		return nil
	}
	name := projectAssistantRunSandboxName(projectWorkspaceScope(id, project), project, "")
	resource := c.Resource(runSandboxInstancesResource, "")
	instance, err := resource.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get project coding environment %q: %w", name, err)
	}
	if instance.GetAnnotations()[projectAssistantRunSandboxLabel] != "true" {
		return fmt.Errorf("%w: instance %q is not an App Studio coding environment", errProjectAssistantRunSandboxConflict, name)
	}
	if err := resource.Delete(ctx, name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete project coding environment %q: %w", name, err)
	}
	return nil
}

func waitForProjectAssistantRunSandboxInstanceDeleted(ctx context.Context, c *asclient.Client, name string) error {
	if c == nil {
		return errors.New("project client is not configured")
	}
	waitCtx, cancel := context.WithTimeout(ctx, projectAssistantRunSandboxReadyTimeout)
	defer cancel()
	ticker := time.NewTicker(projectAssistantRunSandboxReadyPoll)
	defer ticker.Stop()
	resource := c.Resource(runSandboxInstancesResource, "")
	for {
		_, err := resource.Get(waitCtx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get deleting run sandbox instance %q: %w", name, err)
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("wait for expired run sandbox instance %q deletion: %w", name, waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func (s *Server) enforceProjectAssistantRunSandboxQuota(ctx context.Context, c *asclient.Client, currentName string) error {
	if c == nil {
		return errors.New("project client is not configured")
	}
	list, err := c.ListInfrastructureInstances(ctx, metav1.ListOptions{LabelSelector: projectAssistantRunSandboxLabel + "=true"})
	if err != nil {
		return fmt.Errorf("list tenant run sandboxes for quota: %w", err)
	}
	if list == nil {
		return nil
	}
	now := time.Now().UTC()
	active := countProjectAssistantRunSandboxInstances(list, currentName, now)
	if active < projectAssistantRunSandboxMaxActive {
		return nil
	}
	// Successful terminals retain an unclaimed project cache. Make room for a
	// new project by evicting the least-recently-used cached Instance, but only
	// after a visible state transition to "evicting". The single-writer manager
	// excludes locally claimed caches; durable annotations exclude a suspended
	// cache recovered after coordinator restart.
	candidates := make([]*unstructured.Unstructured, 0, len(list.Items))
	for i := range list.Items {
		instance := &list.Items[i]
		if instance.GetName() == strings.TrimSpace(currentName) || s.projectAssistantSandboxManager().claimed(instance.GetName()) || !projectAssistantRunSandboxInstanceCached(instance, now) {
			continue
		}
		candidates = append(candidates, instance)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return projectAssistantRunSandboxInstanceLastActivity(candidates[i]).Before(projectAssistantRunSandboxInstanceLastActivity(candidates[j]))
	})
	var lastErr error
	for _, candidate := range candidates {
		evicted, err := s.evictProjectAssistantRunSandboxCache(ctx, c, candidate.GetName(), now)
		if err != nil {
			lastErr = err
			continue
		}
		if evicted {
			active--
			if active < projectAssistantRunSandboxMaxActive {
				return nil
			}
		}
	}
	if lastErr != nil {
		return fmt.Errorf("tenant already has %d active assistant run sandboxes; cache eviction failed: %w", active, lastErr)
	}
	return fmt.Errorf("tenant already has %d active assistant run sandboxes; no unclaimed cached sandbox is available for eviction", active)
}

func (s *Server) evictProjectAssistantRunSandboxCache(ctx context.Context, c *asclient.Client, name string, now time.Time) (bool, error) {
	if c == nil {
		return false, errors.New("project client is not configured")
	}
	resource := c.Resource(runSandboxInstancesResource, "")
	if s.projectAssistantSandboxManager().claimed(name) {
		return false, nil
	}
	instance, err := resource.Get(ctx, strings.TrimSpace(name), metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("get cached run sandbox %q for eviction: %w", name, err)
	}
	if !projectAssistantRunSandboxInstanceCached(instance, now) {
		return false, nil
	}
	annotations := instance.GetAnnotations()
	copy := make(map[string]string, len(annotations)+1)
	for key, value := range annotations {
		copy[key] = value
	}
	copy[projectAssistantRunSandboxCacheState] = projectAssistantRunSandboxCacheStateEvicting
	instance.SetAnnotations(copy)
	_, err = resource.Update(ctx, instance, metav1.UpdateOptions{})
	if apierrors.IsConflict(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("mark cached run sandbox %q for eviction: %w", name, err)
	}
	if s.projectAssistantSandboxManager().claimed(name) {
		return false, nil
	}
	if err := resource.Delete(ctx, strings.TrimSpace(name), metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		return false, fmt.Errorf("delete cached run sandbox %q: %w", name, err)
	}
	return true, nil
}

func countProjectAssistantRunSandboxInstances(list *unstructured.UnstructuredList, currentName string, now time.Time) int {
	if list == nil {
		return 0
	}
	active := 0
	for i := range list.Items {
		instance := &list.Items[i]
		if instance.GetName() == strings.TrimSpace(currentName) || instance.GetDeletionTimestamp() != nil || projectAssistantRunSandboxInstanceExpired(instance, now) {
			continue
		}
		active++
	}
	return active
}

func projectAssistantRunSandboxInstanceExpired(instance *unstructured.Unstructured, now time.Time) bool {
	if instance == nil {
		return true
	}
	annotations := instance.GetAnnotations()
	for _, key := range []string{projectAssistantRunSandboxIdleExpiry, projectAssistantRunSandboxHardExpiry} {
		if raw := strings.TrimSpace(annotations[key]); raw != "" {
			expiresAt, err := time.Parse(time.RFC3339Nano, raw)
			if err == nil && !now.Before(expiresAt) {
				return true
			}
		}
	}
	status, _, _ := unstructured.NestedString(instance.Object, "status", "status")
	if strings.EqualFold(strings.TrimSpace(status), "expired") {
		return true
	}
	phase, _, _ := unstructured.NestedString(instance.Object, "status", "phase")
	return strings.EqualFold(strings.TrimSpace(phase), "expired")
}

type projectAssistantRunSandboxInstanceStatusGetter func(context.Context) (*unstructured.Unstructured, error)

// projectAssistantRunSandboxInstanceReadiness mirrors the fields consumed by
// Infrastructure's data-plane resolver. An ordinary Instance may exist before
// its development overlay publishes these references, so refs are a readiness
// fence rather than an advisory status.
func projectAssistantRunSandboxInstanceReadiness(obj *unstructured.Unstructured, components map[string]projectTemplateComponent) (ready, terminal bool, reason string) {
	if obj == nil {
		return false, false, "instance has not been observed"
	}
	phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
	if strings.EqualFold(strings.TrimSpace(phase), "failed") || strings.EqualFold(strings.TrimSpace(phase), "error") {
		message, _, _ := unstructured.NestedString(obj.Object, "status", "message")
		if strings.TrimSpace(message) == "" {
			message = "instance reported a failed phase"
		}
		return false, true, message
	}
	readyConditionSeen, readyConditionTrue := false, false
	readyConditionReason := ""
	if conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions"); err == nil && found {
		for _, raw := range conditions {
			condition, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			typeName, _ := condition["type"].(string)
			status, _ := condition["status"].(string)
			reasonText, _ := condition["reason"].(string)
			message, _ := condition["message"].(string)
			detail := strings.TrimSpace(strings.Join([]string{strings.TrimSpace(reasonText), strings.TrimSpace(message)}, ": "))
			switch strings.ToLower(strings.TrimSpace(typeName)) {
			case "valid":
				if !strings.EqualFold(strings.TrimSpace(status), "true") {
					if detail == "" {
						detail = "instance values are not valid"
					}
					return false, true, detail
				}
			case "ready":
				readyConditionSeen = true
				readyConditionTrue = strings.EqualFold(strings.TrimSpace(status), "true")
				if !readyConditionTrue {
					if detail == "" {
						detail = "instance Ready condition is not true"
					}
					readyConditionReason = detail
				}
			}
		}
	}
	runtimeNamespace, _, _ := unstructured.NestedString(obj.Object, "status", "runtimeNamespace")
	if strings.TrimSpace(runtimeNamespace) == "" {
		return false, false, "status.runtimeNamespace is empty"
	}
	secretName, _, _ := unstructured.NestedString(obj.Object, "status", "controlSecretRef", "name")
	if strings.TrimSpace(secretName) == "" {
		return false, false, "status.controlSecretRef.name is empty"
	}
	componentNames := make([]string, 0, len(components))
	for name := range components {
		componentNames = append(componentNames, name)
	}
	sort.Strings(componentNames)
	for _, component := range componentNames {
		serviceName, _, _ := unstructured.NestedString(obj.Object, "status", "components", component, "controlServiceRef", "name")
		if strings.TrimSpace(serviceName) == "" {
			return false, false, fmt.Sprintf("status.components.%s.controlServiceRef.name is empty", component)
		}
		if value, found, err := unstructured.NestedFieldNoCopy(obj.Object, "status", "components", component, "ready"); err == nil && found {
			componentReady, ok := value.(bool)
			if !ok || !componentReady {
				return false, false, fmt.Sprintf("status.components.%s.ready is not true", component)
			}
		}
	}
	if readyConditionSeen && !readyConditionTrue {
		return false, false, readyConditionReason
	}
	return true, false, ""
}

func waitForProjectAssistantRunSandboxInstanceReady(ctx context.Context, timeout, poll time.Duration, components map[string]projectTemplateComponent, get projectAssistantRunSandboxInstanceStatusGetter) error {
	if get == nil {
		return errors.New("run sandbox readiness getter is not configured")
	}
	if timeout <= 0 {
		timeout = projectAssistantRunSandboxReadyTimeout
	}
	if poll <= 0 {
		poll = projectAssistantRunSandboxReadyPoll
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	lastReason := "instance is not ready"
	for {
		obj, err := get(waitCtx)
		if err == nil {
			ready, terminal, reason := projectAssistantRunSandboxInstanceReadiness(obj, components)
			if ready {
				return nil
			}
			if terminal {
				return fmt.Errorf("instance is not ready: %s", reason)
			}
			if strings.TrimSpace(reason) != "" {
				lastReason = reason
			}
		} else if apierrors.IsNotFound(err) {
			lastReason = "instance has not been observed"
		} else {
			if ctx.Err() != nil {
				return fmt.Errorf("wait for run sandbox instance: %w", ctx.Err())
			}
			if waitCtx.Err() != nil {
				return fmt.Errorf("instance did not become ready within %s: %s", timeout, lastReason)
			}
			return fmt.Errorf("get run sandbox instance status: %w", err)
		}
		select {
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return fmt.Errorf("wait for run sandbox instance: %w", ctx.Err())
			}
			return fmt.Errorf("instance did not become ready within %s: %s", timeout, lastReason)
		case <-ticker.C:
		}
	}
}

func (s *Server) waitForProjectAssistantRunSandboxInstanceReady(ctx context.Context, c *asclient.Client, target projectDevelopmentSyncTargetInfo) error {
	if c == nil {
		return errors.New("project client is not configured")
	}
	return waitForProjectAssistantRunSandboxInstanceReady(ctx, projectAssistantRunSandboxReadyTimeout, projectAssistantRunSandboxReadyPoll, target.Components, func(getCtx context.Context) (*unstructured.Unstructured, error) {
		return c.Resource(runSandboxInstancesResource, "").Get(getCtx, target.ResourceName, metav1.GetOptions{})
	})
}

// reconcileProjectAssistantRunSandboxSource keeps the FileStore revision and
// the worker's manifest revision in separate monotonic domains. A warm worker
// whose digest already matches needs no write; otherwise the worker advances
// its own current revision while applying the complete authoritative snapshot.
func reconcileProjectAssistantRunSandboxSource(
	ctx context.Context,
	client projectAssistantSandboxClient,
	id identity,
	ref dataPlaneRef,
	files []projectSandboxSyncFile,
	localDigest string,
) (uint64, string, error) {
	if client == nil {
		return 0, "", errors.New("run sandbox client is not configured")
	}
	listed, err := client.Workspace(ctx, id, ref, projectAssistantSandboxWorkspaceRequest{Action: "list", Path: ".", Limit: workspace.MaxListLimit})
	if err != nil {
		return 0, "", err
	}
	if listed.SourceRevision > 0 && strings.TrimSpace(listed.SourceDigest) != "" && sandboxDigestEqual(listed.SourceDigest, localDigest) {
		return listed.SourceRevision, sandboxSourceDigest(listed.SourceDigest), nil
	}
	seeded, err := client.Workspace(ctx, id, ref, projectAssistantSandboxWorkspaceRequest{Action: "seed", Files: append([]projectSandboxSyncFile(nil), files...)})
	if err != nil {
		return 0, "", err
	}
	if seeded.SourceRevision == 0 || strings.TrimSpace(seeded.SourceDigest) == "" {
		return 0, "", fmt.Errorf("%w: run sandbox seed returned no remote source fence", errProjectAssistantRunSandboxConflict)
	}
	if !sandboxDigestEqual(seeded.SourceDigest, localDigest) {
		return 0, "", fmt.Errorf("%w: run sandbox seed digest does not match the authoritative FileStore snapshot", errProjectAssistantRunSandboxConflict)
	}
	return seeded.SourceRevision, sandboxSourceDigest(seeded.SourceDigest), nil
}

func projectAssistantRunSandboxSeedRetryable(err error) bool {
	var statusErr *projectDevelopmentSyncHTTPError
	if !errors.As(err, &statusErr) {
		return false
	}
	switch statusErr.status {
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return true
	case http.StatusConflict:
		// A 409 is only a provisioning race when the worker explicitly says
		// its runtime routing state is not ready. Revision/content conflicts
		// must fail closed and never be retried by setup.
		detail := strings.ToLower(statusErr.detail)
		return strings.Contains(detail, "not ready") ||
			strings.Contains(detail, "runtime namespace") ||
			strings.Contains(detail, "controlserviceref") ||
			strings.Contains(detail, "control service")
	default:
		return false
	}
}

// retryProjectAssistantRunSandboxSeed closes the small gap between an
// Instance publishing its status refs and the component Service accepting
// traffic. Only explicit transient upstream statuses are retried; auth,
// validation, malformed payload, and other errors fail immediately.
func retryProjectAssistantRunSandboxSeed(ctx context.Context, timeout, poll time.Duration, seed func(context.Context) error) error {
	if seed == nil {
		return errors.New("run sandbox seed function is not configured")
	}
	if timeout <= 0 {
		timeout = projectAssistantRunSandboxReadyTimeout
	}
	if poll <= 0 {
		poll = projectAssistantRunSandboxReadyPoll
	}
	seedCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	var lastErr error
	for {
		err := seed(seedCtx)
		if err == nil {
			return nil
		}
		if !projectAssistantRunSandboxSeedRetryable(err) {
			return err
		}
		lastErr = err
		select {
		case <-seedCtx.Done():
			if ctx.Err() != nil {
				return fmt.Errorf("seed run sandbox: %w", ctx.Err())
			}
			return fmt.Errorf("run sandbox seed did not become reachable within %s: %w", timeout, lastErr)
		case <-ticker.C:
		}
	}
}

func (b *projectAssistantRunSandbox) close(ctx context.Context) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.metadata.Status = "closed"
	b.mu.Unlock()
	if b.runState != nil {
		b.runState.SetSandboxMetadata(b.metadataSnapshot())
	}
	return b.deleteInstance(ctx)
}

func (b *projectAssistantRunSandbox) deleteInstance(ctx context.Context) error {
	if b == nil || b.server == nil || (b.server.gql == nil && b.server.projectClientFor == nil) {
		return nil
	}
	client, err := b.server.clientFor(b.id)
	if err != nil {
		return err
	}
	resource := client.Resource(runSandboxInstancesResource, "")
	instance, err := resource.Get(ctx, b.instance.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	owner := strings.TrimSpace(instance.GetAnnotations()[projectAssistantRunSandboxClaimOwner])
	metadata := b.metadataSnapshot()
	if owner != "" && owner != strings.TrimSpace(metadata.RunID) {
		return fmt.Errorf("%w: refuse to delete run sandbox claimed by another run", errProjectAssistantRunSandboxConflict)
	}
	// The provider GraphQL delete path currently does not preserve Kubernetes
	// DeleteOptions preconditions. Ownership is therefore fenced by the durable
	// claim annotation plus App Studio's single-writer manager, not by pretending
	// a resource-version precondition reaches Infrastructure.
	err = resource.Delete(ctx, b.instance.Name, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// retain marks a successful terminal as an unclaimed project cache. The
// workspace volume and remote process survive, while a subsequent run must
// claim the same Instance and perform a fresh authoritative sync/baseline.
func (b *projectAssistantRunSandbox) retain(ctx context.Context) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	now := time.Now().UTC()
	b.closed = true
	b.metadata.Status = projectAssistantRunSandboxCacheStateCached
	b.metadata.LastActivityAt = now
	b.metadata.IdleExpiresAt = minProjectAssistantRunSandboxExpiry(now.Add(projectAssistantRunSandboxIdleTTL), b.metadata.HardExpiresAt)
	b.mu.Unlock()
	if b.runState != nil {
		b.runState.SetSandboxMetadata(b.metadataSnapshot())
	}
	if b.server == nil || (b.server.gql == nil && b.server.projectClientFor == nil) {
		return nil
	}
	client, err := b.server.clientFor(b.id)
	if err != nil {
		return err
	}
	resource := client.Resource(runSandboxInstancesResource, "")
	instance, err := resource.Get(ctx, b.instance.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	metadata := b.metadataSnapshot()
	annotations := instance.GetAnnotations()
	if strings.TrimSpace(annotations[projectAssistantRunSandboxClaimOwner]) != strings.TrimSpace(metadata.RunID) {
		return fmt.Errorf("%w: successful run no longer owns sandbox claim", errProjectAssistantRunSandboxConflict)
	}
	copy := make(map[string]string, len(annotations)+4)
	for key, value := range annotations {
		copy[key] = value
	}
	delete(copy, projectAssistantRunSandboxClaimOwner)
	delete(copy, projectAssistantRunSandboxClaimExpiry)
	copy[projectAssistantRunSandboxCacheState] = projectAssistantRunSandboxCacheStateCached
	copy[projectAssistantRunSandboxCacheGeneration] = metadata.CacheGeneration
	copy[projectAssistantRunSandboxLastActivity] = metadata.LastActivityAt.Format(time.RFC3339Nano)
	copy[projectAssistantRunSandboxIdleExpiry] = metadata.IdleExpiresAt.Format(time.RFC3339Nano)
	instance.SetAnnotations(copy)
	if _, err := resource.Update(ctx, instance, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("retain run sandbox cache: %w", err)
	}
	return nil
}

func finishProjectAssistantRunSandbox(ctx context.Context, sandbox *projectAssistantRunSandbox, release func(), runErr error, cacheSafe bool) error {
	if sandbox == nil {
		if release != nil {
			release()
		}
		return runErr
	}
	var permissionErr *projectAssistantPermissionRequiredError
	var inputErr *projectAssistantInputRequiredError
	if errors.As(runErr, &permissionErr) || errors.As(runErr, &inputErr) {
		// A suspended run keeps its instance and manager lease.  Its checkpoint
		// carries enough metadata for the resume path to reattach.
		return runErr
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// The worker context is canceled for an interrupted run. Retention/cleanup
	// must not inherit that cancellation: the remote exec cancel has already
	// been sent on its detached bounded context, and the Instance lifecycle
	// transition must still reach Infrastructure.
	closeCtx, cancelClose := context.WithTimeout(context.WithoutCancel(ctx), dataPlaneCallTimeout)
	defer cancelClose()
	var closeErr error
	if cacheSafe {
		closeErr = sandbox.retain(closeCtx)
		if closeErr != nil {
			// A failed retention update must not leave an untracked live worker.
			// deleteInstance re-checks ownership before deleting, so a concurrent
			// claimant cannot be torn down by this fallback.
			deleteErr := sandbox.deleteInstance(closeCtx)
			if deleteErr != nil {
				closeErr = errors.Join(closeErr, deleteErr)
			}
		}
	} else {
		closeErr = sandbox.close(closeCtx)
	}
	if release != nil {
		release()
	}
	if closeErr != nil && runErr != nil {
		return errors.Join(runErr, fmt.Errorf("close run sandbox: %w", closeErr))
	}
	if closeErr != nil {
		return fmt.Errorf("close run sandbox: %w", closeErr)
	}
	return runErr
}

// projectAssistantRunSandboxSetupGuard owns a sandbox from acquisition until
// the assistant turn reaches its single terminal finish path. Setup performs
// several fallible operations (skills, plan hydration, audit, and budget
// configuration); a direct return from any of them must not leak an Instance
// or the per-tenant lease. Once finish is called, the guard is inert so the
// deferred setup cleanup cannot close or release a sandbox twice.
type projectAssistantRunSandboxSetupGuard struct {
	sandbox *projectAssistantRunSandbox
	release func()
	done    bool
}

func newProjectAssistantRunSandboxSetupGuard(sandbox *projectAssistantRunSandbox, release func()) *projectAssistantRunSandboxSetupGuard {
	if sandbox == nil && release == nil {
		return nil
	}
	return &projectAssistantRunSandboxSetupGuard{sandbox: sandbox, release: release}
}

func (g *projectAssistantRunSandboxSetupGuard) cleanupSetup() {
	if g == nil || g.done {
		return
	}
	g.done = true
	_ = finishProjectAssistantRunSandbox(context.Background(), g.sandbox, g.release, errors.New("assistant run setup failed"), false)
}

func (g *projectAssistantRunSandboxSetupGuard) finish(ctx context.Context, runErr error, cacheSafe bool) error {
	if g == nil || g.done {
		return runErr
	}
	g.done = true
	return finishProjectAssistantRunSandbox(ctx, g.sandbox, g.release, runErr, cacheSafe)
}

func projectAssistantRunSandboxSuspended(runErr error) bool {
	var permissionErr *projectAssistantPermissionRequiredError
	var inputErr *projectAssistantInputRequiredError
	return errors.As(runErr, &permissionErr) || errors.As(runErr, &inputErr)
}

func projectAssistantSandboxChanges(changes []projectAssistantSandboxWorkspaceChange) ([]workspace.ManagedFileChange, error) {
	if len(changes) > projectAssistantRunSandboxMaxChanges {
		return nil, fmt.Errorf("%w: checkpoint contains too many files", errProjectAssistantRunSandboxConflict)
	}
	var bytes int
	out := make([]workspace.ManagedFileChange, 0, len(changes))
	seen := map[string]struct{}{}
	for _, change := range changes {
		path, err := workspace.CleanProjectPath(change.Path)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid checkpoint path", errProjectAssistantRunSandboxConflict)
		}
		if _, ok := seen[path]; ok {
			return nil, fmt.Errorf("%w: duplicate checkpoint path %q", errProjectAssistantRunSandboxConflict, path)
		}
		seen[path] = struct{}{}
		bytes += len([]byte(change.Content))
		if bytes > projectAssistantRunSandboxMaxChangeBytes {
			return nil, fmt.Errorf("%w: checkpoint content is too large", errProjectAssistantRunSandboxConflict)
		}
		op := workspace.ManagedFileOperation(change.Operation)
		switch op {
		case workspace.ManagedFileCreate, workspace.ManagedFileReplace, workspace.ManagedFileDelete:
		default:
			return nil, fmt.Errorf("%w: unsupported checkpoint operation %q", errProjectAssistantRunSandboxConflict, change.Operation)
		}
		out = append(out, workspace.ManagedFileChange{Path: path, Operation: op, Content: change.Content, ExpectedVersion: change.ExpectedVersion})
	}
	return out, nil
}

func projectAssistantSandboxChangesJSON(changes []workspace.ManagedFileChange) []projectAssistantSandboxWorkspaceChange {
	out := make([]projectAssistantSandboxWorkspaceChange, 0, len(changes))
	for _, change := range changes {
		out = append(out, projectAssistantSandboxWorkspaceChange{Path: change.Path, Operation: string(change.Operation), Content: change.Content, ExpectedVersion: change.ExpectedVersion})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// checkpoint atomically applies only worker-returned bounded changes.  The
// worker must provide expected versions from the seed; any local source drift
// or stale expected version rejects the whole transaction.
func (b *projectAssistantRunSandbox) checkpointForTerminalSettlement(ctx context.Context, req projectAssistantRunRequest) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() == nil {
		return b.checkpoint(ctx, req)
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		// An actual deadline is a settlement failure, not a user interruption.
		// Preserve the expired context so the caller deletes the uncertain cache.
		return b.checkpoint(ctx, req)
	}
	// A user interruption cancels the run context before the bounded executor
	// returns its terminal canceled result. The command has already settled at
	// this point, so use an independent bounded context to preserve any proven
	// workspace changes and retain a healthy warm cache. A real checkpoint or
	// fence failure still fails closed and deletes the Instance.
	checkpointCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dataPlaneCallTimeout)
	defer cancel()
	return b.checkpoint(checkpointCtx, req)
}

func (b *projectAssistantRunSandbox) checkpoint(ctx context.Context, req projectAssistantRunRequest) error {
	if b == nil || req.Workspace == nil {
		return fmt.Errorf("%w: project workspace store is not configured", errProjectAssistantRunSandboxConflict)
	}
	if b.runState != nil && !projectAssistantTurnProfileAllowsMutation(b.runState.TurnProfile()) {
		return nil
	}
	meta := b.metadataSnapshot()
	localRevision, err := req.Workspace.SourceRevision(ctx, req.WorkspaceScope)
	if err != nil {
		return err
	}
	if localRevision != meta.SourceRevision {
		return fmt.Errorf("%w: source revision changed from %d to %d", errProjectAssistantRunSandboxConflict, meta.SourceRevision, localRevision)
	}
	var localSnapshot projectWorkspaceSyncSnapshot
	if b.server != nil {
		localSnapshot, err = b.server.projectWorkspaceSyncFiles(ctx, req.WorkspaceScope)
		if err != nil {
			return err
		}
		if expected := strings.TrimSpace(meta.SourceDigest); expected != "" {
			observed := projectSandboxSyncDigest(localSnapshot.Files)
			if observed != expected {
				return fmt.Errorf("%w: source digest changed", errProjectAssistantRunSandboxConflict)
			}
		}
	}
	if strings.TrimSpace(meta.RemoteCheckpointID) == "" {
		return fmt.Errorf("%w: remote workspace baseline checkpoint is missing", errProjectAssistantRunSandboxConflict)
	}
	response, err := b.request(ctx, projectAssistantSandboxWorkspaceRequest{Action: "checkpoint", CheckpointID: meta.RemoteCheckpointID})
	if err != nil {
		return err
	}
	changes, err := projectAssistantSandboxChanges(response.Changes)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		b.mu.Lock()
		b.metadata.CheckpointRevision = localRevision
		b.metadata.CheckpointDigest = meta.SourceDigest
		b.mu.Unlock()
		if b.runState != nil {
			b.runState.SetSandboxMetadata(b.metadataSnapshot())
		}
		return nil
	}
	if _, err := req.Workspace.ApplyManagedTransaction(ctx, req.WorkspaceScope, changes); err != nil {
		return fmt.Errorf("%w: apply checkpoint: %v", errProjectAssistantRunSandboxConflict, err)
	}
	paths := make([]string, 0, len(changes))
	for _, change := range changes {
		paths = append(paths, change.Path)
	}
	if _, err := req.Workspace.AddUncommittedPaths(ctx, req.WorkspaceScope, paths); err != nil {
		return fmt.Errorf("%w: persist checkpoint dirty paths: %v", errProjectAssistantRunSandboxConflict, err)
	}
	newRevision, err := req.Workspace.SourceRevision(ctx, req.WorkspaceScope)
	if err != nil {
		return err
	}
	newDigest := ""
	if b.server != nil {
		// The source fence is the complete FileStore snapshot, not the digest of
		// only the changed paths. This remains comparable with the next full
		// seed/checkpoint and with the component-root worker digest.
		updated, snapshotErr := b.server.projectWorkspaceSyncFiles(ctx, req.WorkspaceScope)
		if snapshotErr != nil {
			return snapshotErr
		}
		newDigest = projectSandboxSyncDigest(updated.Files)
	} else {
		newDigest, err = req.Workspace.WorkspaceDigest(ctx, req.WorkspaceScope, paths)
		if err != nil {
			return err
		}
	}
	// Advance the worker's durable baseline only after the FileStore
	// transaction succeeds. If this call fails, the old metadata remains a
	// deliberate fail-closed fence rather than pretending the two stores agree.
	baseline, err := b.request(ctx, projectAssistantSandboxWorkspaceRequest{Action: "checkpoint", CheckpointAction: "create"})
	if err != nil {
		return fmt.Errorf("%w: advance remote workspace baseline: %v", errProjectAssistantRunSandboxConflict, err)
	}
	if strings.TrimSpace(baseline.CheckpointID) == "" {
		return fmt.Errorf("%w: remote workspace baseline returned no checkpoint ID", errProjectAssistantRunSandboxConflict)
	}
	b.mu.Lock()
	b.metadata.SourceRevision = newRevision
	b.metadata.SourceDigest = newDigest
	b.metadata.CheckpointRevision = newRevision
	b.metadata.CheckpointDigest = newDigest
	b.metadata.RemoteCheckpointID = baseline.CheckpointID
	b.mu.Unlock()
	if b.runState != nil {
		b.runState.SetSandboxMetadata(b.metadataSnapshot())
	}
	// FileStore writeback is authoritative and must not depend on a hosted
	// Project development environment. A template-less project can still use
	// the per-run universal sandbox; in that case there is no legacy preview
	// target to synchronize after the checkpoint.
	// req.Project is the shared, current project snapshot. b.project captures
	// only the state at sandbox creation and can be stale after this same turn
	// binds a hosted development template.
	if b.server != nil && projectAssistantDevelopmentTemplateBound(req.Project) {
		if b.runState != nil {
			syncRevision := b.runState.BeginDevelopmentSyncForCurrentMutation()
			if !b.server.scheduleDevelopmentSyncAfterMutationWithCompletion(
				b.id,
				req.Project,
				projectActionWorkspaceSync,
				func(syncErr error) { b.runState.CompleteDevelopmentSync(syncRevision, syncErr) },
			) {
				b.runState.CompleteDevelopmentSync(syncRevision, errors.New("workspace synchronization was not scheduled after sandbox checkpoint"))
			}
		} else {
			b.server.scheduleDevelopmentSyncAfterMutationWithCompletion(b.id, req.Project, projectActionWorkspaceSync, nil)
		}
	}
	return nil
}
