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

// Package provideractions is the hub's forward-only Provider Actions
// transport. It authorizes against the provider catalog, derives tenant
// identity from the authenticated request, and forwards only to the provider's
// declared VirtualWorkspace action endpoint.
package provideractions

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/faroshq/faros-kedge/pkg/hub/providers"
)

const (
	PathInvoke = "/api/provider-actions/invoke"

	defaultActionTimeout = 90 * time.Second
	maxRequestBytes      = 1 << 20
	maxResponseBytes     = 8 << 20
	maxActionNameLength  = 128
	maxRequestIDLength   = 256
	maxHeaderValueLength = 512
	providerActionSync   = "sync"
	providerActionKeyed  = "keyed"
	providerActionNone   = "none"
)

// ProviderLookup is the minimal registry surface needed by the transport.
type ProviderLookup interface {
	Get(string) (providers.Provider, bool)
}

// ClusterResolver maps a validated tenant workspace path to its logical
// cluster ID. The ID is derived server-side and cannot be supplied by callers.
type ClusterResolver func(context.Context, string) (string, error)

// InvocationAuthorizer is the hub-side grant gate for a fully resolved
// invocation. Human callers are intentionally handled as a no-op by the
// production implementation; ServiceAccount-shaped callers must prove a
// Kedge workload identity and an exact Project action grant before routing.
// The declared action is the immutable registry snapshot selected by the
// handler, so an authorizer cannot authorize against caller-supplied metadata.
type InvocationAuthorizer interface {
	Authorize(context.Context, *http.Request, string, string, invokeRequest, providers.ProviderAction) error
}

// Options configures the action handler.
type Options struct {
	Registry             ProviderLookup
	TenantResolver       providers.TenantResolver
	ClusterResolver      ClusterResolver
	InvocationAuthorizer InvocationAuthorizer
	HTTPClient           *http.Client
	ActionTimeout        time.Duration
	Metrics              *Metrics
	Logger               logr.Logger
}

// Handler serves POST /api/provider-actions/invoke.
type Handler struct {
	registry             ProviderLookup
	tenantResolver       providers.TenantResolver
	clusterResolver      ClusterResolver
	invocationAuthorizer InvocationAuthorizer
	httpClient           *http.Client
	actionTimeout        time.Duration
	metrics              *Metrics
	log                  logr.Logger
}

type responseRecorder struct {
	http.ResponseWriter
	status    int
	bytes     int64
	errorCode string
}

type countingReadCloser struct {
	io.ReadCloser
	bytes int64
}

func (r *countingReadCloser) Read(value []byte) (int, error) {
	n, err := r.ReadCloser.Read(value)
	r.bytes += int64(n)
	return n, err
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.status != 0 {
		return
	}
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(value []byte) (int, error) {
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(value)
	r.bytes += int64(n)
	return n, err
}

// New creates a provider action handler from options.
func New(opts Options) *Handler {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	// Provider action requests carry the caller's bearer. Never follow a
	// provider redirect that could move that credential outside the declared
	// VirtualWorkspace origin. Preserve a caller-supplied redirect policy only
	// for its own deterministic rejection/error behavior.
	clientCopy := *client
	redirectPolicy := client.CheckRedirect
	clientCopy.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if redirectPolicy != nil {
			if err := redirectPolicy(req, via); err != nil {
				return err
			}
		}
		return http.ErrUseLastResponse
	}
	client = &clientCopy
	timeout := opts.ActionTimeout
	if timeout <= 0 {
		timeout = defaultActionTimeout
	} else if timeout > defaultActionTimeout {
		// A caller may shorten the bound for tests or deployment policy, but
		// cannot turn the hub into an unbounded provider proxy.
		timeout = defaultActionTimeout
	}
	metrics := opts.Metrics
	if metrics == nil {
		metrics = DefaultMetrics()
	}
	return &Handler{
		registry:             opts.Registry,
		tenantResolver:       opts.TenantResolver,
		clusterResolver:      opts.ClusterResolver,
		invocationAuthorizer: opts.InvocationAuthorizer,
		httpClient:           client,
		actionTimeout:        timeout,
		metrics:              metrics,
		log:                  opts.Logger,
	}
}

// NewHandler is a convenience constructor for direct tests and small
// embedders. Optional options override the registry when supplied.
func NewHandler(registry ProviderLookup, opts ...Options) *Handler {
	config := Options{Registry: registry}
	if len(opts) > 0 {
		provided := opts[0]
		if provided.Registry == nil {
			provided.Registry = registry
		}
		config = provided
	}
	return New(config)
}

// SetTenantResolver installs the server-side identity resolver.
func (h *Handler) SetTenantResolver(resolver providers.TenantResolver) {
	if h != nil {
		h.tenantResolver = resolver
	}
}

// SetClusterResolver installs the server-side logical-cluster resolver.
func (h *Handler) SetClusterResolver(resolver ClusterResolver) {
	if h != nil {
		h.clusterResolver = resolver
	}
}

// SetInvocationAuthorizer installs the server-side Project grant authorizer.
func (h *Handler) SetInvocationAuthorizer(authorizer InvocationAuthorizer) {
	if h != nil {
		h.invocationAuthorizer = authorizer
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	recorded := &responseRecorder{ResponseWriter: w}
	w = recorded
	requestBody := r.Body
	if requestBody == nil {
		requestBody = http.NoBody
	}
	countedBody := &countingReadCloser{ReadCloser: requestBody}
	r.Body = countedBody
	requestID := requestID(r.Header.Get("X-Request-ID"))
	var decodedRequest *invokeRequest
	defer func() {
		duration := time.Since(started)
		h.observeMetrics(decodedRequest, recorded.status, recorded.errorCode, countedBody.bytes, recorded.bytes, duration)
		h.logCompletion(requestID, decodedRequest, recorded.status, recorded.bytes, duration)
	}()
	w.Header().Set("X-Request-ID", requestID)
	if r.Method != http.MethodPost {
		h.writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", requestID)
		return
	}
	token, ok := bearer(r.Header.Get("Authorization"))
	if !ok {
		h.writeError(w, http.StatusUnauthorized, "unauthenticated", "a bearer credential is required", requestID)
		return
	}
	if h == nil || h.registry == nil {
		h.writeError(w, http.StatusServiceUnavailable, "action_unavailable", "provider action catalog is unavailable", requestID)
		return
	}
	if h.tenantResolver == nil || h.clusterResolver == nil {
		h.writeError(w, http.StatusServiceUnavailable, "tenant_unavailable", "tenant identity resolution is unavailable", requestID)
		return
	}
	user, tenantPath, err := h.tenantResolver.Resolve(r)
	if err != nil || strings.TrimSpace(user) == "" || strings.TrimSpace(tenantPath) == "" {
		h.writeError(w, http.StatusForbidden, "tenant_forbidden", "caller is not authorized for a tenant workspace", requestID)
		return
	}
	clusterID, err := h.clusterResolver(r.Context(), tenantPath)
	if err != nil || strings.TrimSpace(clusterID) == "" {
		h.writeError(w, http.StatusForbidden, "tenant_forbidden", "caller tenant workspace could not be resolved", requestID)
		return
	}
	req, inputValue, err := decodeInvokeRequest(w, r)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_request", err.Error(), requestID)
		return
	}
	decodedRequest = &req
	provider, ok := h.registry.Get(req.Provider)
	if !ok || !provider.Ready() {
		h.writeError(w, http.StatusNotFound, "provider_unavailable", "provider action is unavailable", requestID, &req)
		return
	}
	declared, ok := declaredAction(provider, req.Action, req.ActionVersion)
	if !ok {
		h.writeError(w, http.StatusNotFound, "action_not_declared", "provider action is not declared", requestID, &req)
		return
	}
	if !resourceMatches(declared.Resource, req.ResourceRef) {
		h.writeError(w, http.StatusBadRequest, "resource_mismatch", "resourceRef does not match the declared provider action", requestID, &req)
		return
	}
	if req.SchemaDigest != declared.SchemaDigest {
		h.writeError(w, http.StatusConflict, "action_schema_mismatch", "schemaDigest does not match the declared provider action", requestID, &req)
		return
	}
	if h.invocationAuthorizer != nil {
		if err := h.invocationAuthorizer.Authorize(r.Context(), r, user, tenantPath, req, declared); err != nil {
			h.writeError(w, http.StatusForbidden, "action_grant_denied", "caller is not authorized for this provider action", requestID, &req)
			return
		}
	}
	if declared.ExecutionMode != "" && declared.ExecutionMode != providerActionSync {
		h.writeError(w, http.StatusNotImplemented, "action_mode_unsupported", "asynchronous provider actions are not supported by this transport", requestID, &req)
		return
	}
	if declared.Limits.MaxInputBytes > 0 && int64(len(req.Input)) > declared.Limits.MaxInputBytes {
		h.writeError(w, http.StatusRequestEntityTooLarge, "input_too_large", "provider action input exceeds the declared byte limit", requestID, &req)
		return
	}
	if err := validateDeclaredSchema(declared.InputValidator, inputValue); err != nil {
		h.writeError(w, http.StatusBadRequest, "input_invalid", err.Error(), requestID, &req)
		return
	}
	if provider.VirtualWorkspaceURL == nil {
		h.writeError(w, http.StatusNotFound, "action_unavailable", "provider action endpoint is unavailable", requestID, &req)
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if len(idempotencyKey) > maxHeaderValueLength {
		h.writeError(w, http.StatusBadRequest, "invalid_request", "Idempotency-Key is too long", requestID, &req)
		return
	}
	switch declared.Idempotency {
	case providerActionKeyed:
		if idempotencyKey == "" {
			h.writeError(w, http.StatusBadRequest, "idempotency_key_required", "this provider action requires Idempotency-Key", requestID, &req)
			return
		}
		h.writeError(w, http.StatusNotImplemented, "action_idempotency_unsupported", "keyed provider action idempotency is not supported by this transport", requestID, &req)
		return
	case providerActionNone:
		if idempotencyKey != "" {
			h.writeError(w, http.StatusBadRequest, "idempotency_key_not_supported", "this provider action does not accept Idempotency-Key", requestID, &req)
			return
		}
	}
	maxActionDuration := h.actionTimeout
	if declared.Limits.TimeoutSeconds > 0 {
		declaredDuration := time.Duration(declared.Limits.TimeoutSeconds) * time.Second
		if declaredDuration < maxActionDuration {
			maxActionDuration = declaredDuration
		}
	}
	ctx, cancel, deadlineHeader, err := actionContext(r, maxActionDuration)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid_deadline", err.Error(), requestID, &req)
		return
	}
	defer cancel()
	body, err := json.Marshal(struct {
		ResourceRef resourceRef     `json:"resourceRef"`
		Input       json.RawMessage `json:"input"`
	}{ResourceRef: req.ResourceRef, Input: req.Input})
	if err != nil {
		h.writeError(w, http.StatusInternalServerError, "request_encode_failed", "could not encode provider action request", requestID, &req)
		return
	}
	target := actionURL(provider.VirtualWorkspaceURL, req.Action, req.ActionVersion)
	forward, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		h.writeError(w, http.StatusBadGateway, "provider_unavailable", "provider action endpoint is invalid", requestID, &req)
		return
	}
	forward.Header.Set("Accept", "application/json")
	forward.Header.Set("Content-Type", "application/json")
	forward.Header.Set("Authorization", token)
	forward.Header.Set("X-Kedge-User", user)
	forward.Header.Set("X-Kedge-Tenant", tenantPath)
	forward.Header.Set("X-Kedge-Cluster", clusterID)
	forward.Header.Set("X-Request-ID", requestID)
	if traceparent, tracestate := forwardedTraceHeaders(r.Header.Get("traceparent"), r.Header.Get("tracestate")); traceparent != "" {
		forward.Header.Set("traceparent", traceparent)
		if tracestate != "" {
			forward.Header.Set("tracestate", tracestate)
		}
	}
	if idempotencyKey != "" {
		forward.Header.Set("Idempotency-Key", idempotencyKey)
	}
	forward.Header.Set("X-Kedge-Action-Deadline-Ms", deadlineHeader)
	resp, err := h.httpClient.Do(forward)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			h.writeError(w, http.StatusGatewayTimeout, "action_timeout", "provider action timed out", requestID, &req)
			return
		}
		h.writeError(w, http.StatusBadGateway, "provider_unavailable", "provider action request failed", requestID, &req)
		return
	}
	defer resp.Body.Close()
	responseLimit := int64(maxResponseBytes)
	if declared.Limits.MaxOutputBytes > 0 && declared.Limits.MaxOutputBytes < responseLimit {
		responseLimit = declared.Limits.MaxOutputBytes
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, responseLimit+1))
	if readErr != nil || int64(len(responseBody)) > responseLimit {
		h.writeError(w, http.StatusBadGateway, "provider_invalid_response", "provider action response could not be read", requestID, &req)
		return
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		h.writeError(w, http.StatusBadGateway, "provider_action_failed", "provider action failed", requestID, &req)
		return
	}
	var result any
	if err := json.Unmarshal(responseBody, &result); err != nil {
		h.writeError(w, http.StatusBadGateway, "provider_invalid_response", "provider action returned invalid JSON", requestID, &req)
		return
	}
	if declared.Limits.MaxResultItems > 0 && resultExceedsItems(result, declared.Limits.MaxResultItems) {
		h.writeError(w, http.StatusBadGateway, "provider_result_too_large", "provider action result exceeds the declared item limit", requestID, &req)
		return
	}
	if err := validateDeclaredSchema(declared.OutputValidator, result); err != nil {
		h.writeError(w, http.StatusBadGateway, "provider_invalid_response", "provider action result did not match its declared schema", requestID, &req)
		return
	}
	h.writeSuccess(w, requestID, &req, result)
}

func (h *Handler) logCompletion(requestID string, req *invokeRequest, status int, responseBytes int64, duration time.Duration) {
	if h == nil || h.log.GetSink() == nil {
		return
	}
	if status == 0 {
		status = http.StatusInternalServerError
	}
	fields := []any{
		"requestID", requestID,
		"outcome", "completed",
		"status", status,
		"durationMs", duration.Milliseconds(),
		"responseBytes", responseBytes,
	}
	if req != nil {
		fields = append(fields,
			"provider", req.Provider,
			"action", req.Action,
			"actionVersion", req.ActionVersion,
			"resourceType", req.ResourceRef.Kind+"/"+req.ResourceRef.Resource,
			"resourceName", req.ResourceRef.Name,
			"inputBytes", len(req.Input),
		)
	}
	h.log.Info("provider action completed", fields...)
}

func (h *Handler) observeMetrics(req *invokeRequest, status int, errorCode string, requestBytes, responseBytes int64, duration time.Duration) {
	if h == nil || h.metrics == nil {
		return
	}
	if status == 0 {
		status = http.StatusInternalServerError
	}
	outcome := metricOutcomeSuccess
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		outcome = metricOutcomeError
	}
	if errorCode == "" {
		errorCode = metricNoError
		if outcome == metricOutcomeError {
			errorCode = metricUnknown
		}
	}

	providerName, actionName, actionVersion := metricUnknown, metricUnknown, metricUnknown
	if req != nil && h.registry != nil {
		if provider, ok := h.registry.Get(req.Provider); ok {
			if provider.Name != "" {
				providerName = metricLabel(provider.Name)
			}
			if declared, ok := declaredAction(provider, req.Action, req.ActionVersion); ok {
				actionName = metricLabel(declared.Name)
				actionVersion = metricLabel(declared.Version)
			}
		}
	}
	labels := []string{
		providerName,
		actionName,
		actionVersion,
		outcome,
		strconv.Itoa(status),
		metricLabel(errorCode),
	}
	h.metrics.observe(labels, duration.Seconds(), requestBytes, responseBytes)
}

func metricLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return metricUnknown
	}
	if len(value) > maxHeaderValueLength {
		return value[:maxHeaderValueLength]
	}
	return value
}

type invokeRequest struct {
	Provider      string          `json:"provider"`
	Action        string          `json:"action"`
	ActionVersion string          `json:"actionVersion"`
	SchemaDigest  string          `json:"schemaDigest"`
	ResourceRef   resourceRef     `json:"resourceRef"`
	Input         json.RawMessage `json:"input"`
}

type resourceRef struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Resource   string `json:"resource"`
	Name       string `json:"name"`
}

// actionEnvelope is the stable hub-facing response contract. The provider's
// result remains opaque JSON; the hub owns the invocation identity and error
// shape so callers do not need provider-specific response adapters.
type actionEnvelope struct {
	RequestID     string          `json:"requestID"`
	Provider      string          `json:"provider"`
	Action        string          `json:"action"`
	ActionVersion string          `json:"actionVersion"`
	ResourceRef   *resourceRef    `json:"resourceRef"`
	Result        json.RawMessage `json:"result,omitempty"`
	Error         *actionError    `json:"error,omitempty"`
}

type actionError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func decodeInvokeRequest(w http.ResponseWriter, r *http.Request) (invokeRequest, any, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	var req invokeRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return invokeRequest{}, nil, fmt.Errorf("decode request: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return invokeRequest{}, nil, fmt.Errorf("request must contain exactly one JSON value")
		}
		return invokeRequest{}, nil, fmt.Errorf("decode trailing request data: %w", err)
	}
	if strings.TrimSpace(req.Provider) == "" || strings.TrimSpace(req.Action) == "" || strings.TrimSpace(req.ActionVersion) == "" {
		return invokeRequest{}, nil, fmt.Errorf("provider, action, and actionVersion are required")
	}
	if strings.TrimSpace(req.SchemaDigest) == "" {
		return invokeRequest{}, nil, fmt.Errorf("schemaDigest is required")
	}
	if !validSchemaDigest(req.SchemaDigest) {
		return invokeRequest{}, nil, fmt.Errorf("schemaDigest must match sha256:<64 lowercase hex digits>")
	}
	if len(req.Action) > maxActionNameLength || len(req.ActionVersion) > maxActionNameLength {
		return invokeRequest{}, nil, fmt.Errorf("action name is too long")
	}
	if !validActionPart(req.Action) || !validActionPart(req.ActionVersion) {
		return invokeRequest{}, nil, fmt.Errorf("action and actionVersion contain invalid characters")
	}
	if req.ResourceRef.APIVersion == "" || req.ResourceRef.Kind == "" || req.ResourceRef.Resource == "" || req.ResourceRef.Name == "" {
		return invokeRequest{}, nil, fmt.Errorf("resourceRef must include apiVersion, kind, resource, and name")
	}
	if len(req.Input) == 0 || string(req.Input) == "null" {
		req.Input = json.RawMessage(`{}`)
	}
	var input any
	if err := json.Unmarshal(req.Input, &input); err != nil {
		return invokeRequest{}, nil, fmt.Errorf("input must be valid JSON: %w", err)
	}
	if input == nil {
		input = map[string]any{}
	}
	if _, ok := input.(map[string]any); !ok {
		return invokeRequest{}, nil, fmt.Errorf("input must be an object")
	}
	return req, input, nil
}

func validActionPart(value string) bool {
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != '-' && r != '.' {
			return false
		}
	}
	return true
}

func validSchemaDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, r := range value[len("sha256:"):] {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func declaredAction(provider providers.Provider, name, version string) (providers.ProviderAction, bool) {
	for _, action := range provider.Actions {
		if action.Name == name && action.Version == version {
			return action, true
		}
	}
	return providers.ProviderAction{}, false
}

func resourceMatches(declared providers.ProviderActionResource, ref resourceRef) bool {
	return declared.APIVersion == ref.APIVersion && declared.Kind == ref.Kind && declared.Resource == ref.Resource
}

func actionURL(base *url.URL, action, version string) *url.URL {
	u := *base
	u.Path = strings.TrimRight(base.Path, "/") + "/actions/" + url.PathEscape(action) + "/" + url.PathEscape(version)
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return &u
}

func actionContext(r *http.Request, max time.Duration) (context.Context, context.CancelFunc, string, error) {
	if max <= 0 {
		max = defaultActionTimeout
	}
	requested := max
	if raw := strings.TrimSpace(r.Header.Get("X-Kedge-Action-Deadline-Ms")); raw != "" {
		millis, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || millis < 1 {
			return nil, nil, "", fmt.Errorf("X-Kedge-Action-Deadline-Ms must be a positive integer")
		}
		if millis >= max.Milliseconds() {
			requested = max
		} else {
			requested = time.Duration(millis) * time.Millisecond
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), requested)
	deadline, ok := ctx.Deadline()
	if !ok {
		return ctx, cancel, strconv.FormatInt(requested.Milliseconds(), 10), nil
	}
	remaining := time.Until(deadline).Milliseconds()
	if remaining < 1 {
		remaining = 1
	}
	return ctx, cancel, strconv.FormatInt(remaining, 10), nil
}

func bearer(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) <= len("Bearer ") || !strings.EqualFold(trimmed[:len("Bearer ")], "Bearer ") {
		return "", false
	}
	token := strings.TrimSpace(trimmed[len("Bearer "):])
	if token == "" {
		return "", false
	}
	return "Bearer " + token, true
}

func requestID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 0 && len(value) <= maxRequestIDLength {
		return value
	}
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return hex.EncodeToString(buf[:])
	}
	return strconv.FormatInt(time.Now().UnixNano(), 10)
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (h *Handler) writeSuccess(w http.ResponseWriter, requestID string, req *invokeRequest, result any) {
	encoded, err := json.Marshal(result)
	if err != nil {
		h.writeError(w, http.StatusBadGateway, "provider_invalid_response", "provider action returned invalid JSON", requestID, req)
		return
	}
	envelope := actionEnvelope{RequestID: requestID, Result: encoded}
	if req != nil {
		envelope.Provider = req.Provider
		envelope.Action = req.Action
		envelope.ActionVersion = req.ActionVersion
		envelope.ResourceRef = resourceRefPointer(req.ResourceRef)
	}
	h.writeJSON(w, http.StatusOK, envelope)
}

func (h *Handler) writeError(w http.ResponseWriter, status int, code, message, requestID string, reqs ...*invokeRequest) {
	if recorded, ok := w.(*responseRecorder); ok {
		recorded.errorCode = code
	}
	envelope := actionEnvelope{
		RequestID: requestID,
		Error: &actionError{
			Code: code, Message: message, Retryable: status >= http.StatusInternalServerError,
		},
	}
	if len(reqs) > 0 && reqs[0] != nil {
		req := reqs[0]
		envelope.Provider = req.Provider
		envelope.Action = req.Action
		envelope.ActionVersion = req.ActionVersion
		envelope.ResourceRef = resourceRefPointer(req.ResourceRef)
	}
	h.writeJSON(w, status, envelope)
}

func resourceRefPointer(ref resourceRef) *resourceRef {
	return &resourceRef{APIVersion: ref.APIVersion, Kind: ref.Kind, Resource: ref.Resource, Name: ref.Name}
}

func validateDeclaredSchema(compiled *jsonschema.Schema, value any) error {
	if compiled == nil {
		return fmt.Errorf("declared action schema is invalid")
	}
	if err := compiled.Validate(value); err != nil {
		return fmt.Errorf("value does not match the declared schema")
	}
	return nil
}

func resultExceedsItems(value any, max int64) bool {
	if max < 1 {
		return false
	}
	switch current := value.(type) {
	case []any:
		if int64(len(current)) > max {
			return true
		}
		for _, item := range current {
			if resultExceedsItems(item, max) {
				return true
			}
		}
	case map[string]any:
		for _, item := range current {
			if resultExceedsItems(item, max) {
				return true
			}
		}
	}
	return false
}
