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

package provideractions

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	providersv1alpha1 "github.com/faroshq/faros-kedge/apis/providers/v1alpha1"
	"github.com/go-logr/logr"

	"github.com/faroshq/faros-kedge/pkg/hub/providers"
	"k8s.io/apimachinery/pkg/runtime"
)

type fakeTenantResolver func(*http.Request) (string, string, error)

func (f fakeTenantResolver) Resolve(r *http.Request) (string, string, error) {
	return f(r)
}

func TestForwardOnlyProviderAction(t *testing.T) {
	base, _ := url.Parse("https://provider.invalid/vw")
	reg := providers.NewRegistry()
	action := mustAction(t, "query_table/v1", "databricks.kedge.faros.sh/v1alpha1", "Table", "tables", `{"type":"object","properties":{"columns":{"type":"array"},"limit":{"type":"integer"}},"additionalProperties":false}`, `{"type":"object"}`, providersv1alpha1.ProviderActionIdempotencyInherent, providers.ProviderActionLimits{})
	reg.Upsert(providers.Provider{
		Name: "databricks", EndpointsValid: true, VirtualWorkspaceURL: base,
		Actions: []providers.ProviderAction{action},
	})
	var forwarded *http.Request
	var body []byte
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		forwarded = r
		body, _ = io.ReadAll(r.Body)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"actionVersion":"v1","tableRef":"taxi-trips","rows":[]}`))}, nil
	})}
	h := New(Options{
		Registry: reg, HTTPClient: client, Logger: logr.Discard(),
		TenantResolver:  fakeTenantResolver(func(*http.Request) (string, string, error) { return "caller", "root:kedge:tenants:org:ws", nil }),
		ClusterResolver: func(context.Context, string) (string, error) { return "cluster-a", nil },
		ActionTimeout:   5 * time.Second,
	})
	r := httptest.NewRequest(http.MethodPost, PathInvoke, strings.NewReader(fmt.Sprintf(`{"provider":"databricks","action":"query_table","actionVersion":"v1","schemaDigest":%q,"resourceRef":{"apiVersion":"databricks.kedge.faros.sh/v1alpha1","kind":"Table","resource":"tables","name":"taxi-trips"},"input":{"columns":["trip_distance"],"limit":25}}`, action.SchemaDigest)))
	r.Header.Set("Authorization", "Bearer caller-token")
	r.Header.Set("X-Kedge-Tenant", "attacker-tenant")
	r.Header.Set("X-Kedge-Cluster", "attacker-cluster")
	r.Header.Set("X-Kedge-User", "attacker-user")
	r.Header.Set("X-Request-ID", "request-1")
	r.Header.Set("Idempotency-Key", "idem-1")
	r.Header.Set("X-Kedge-Action-Deadline-Ms", "2000")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, r)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rw.Code, rw.Body.String())
	}
	if forwarded == nil {
		t.Fatal("provider was not called")
	}
	if forwarded.URL.Path != "/vw/actions/query_table/v1" {
		t.Fatalf("forward path = %q", forwarded.URL.Path)
	}
	if got := forwarded.Header.Get("Authorization"); got != "Bearer caller-token" {
		t.Fatalf("authorization = %q", got)
	}
	if got := forwarded.Header.Get("X-Kedge-Tenant"); got != "root:kedge:tenants:org:ws" {
		t.Fatalf("tenant = %q", got)
	}
	if got := forwarded.Header.Get("X-Kedge-Cluster"); got != "cluster-a" {
		t.Fatalf("cluster = %q", got)
	}
	if got := forwarded.Header.Get("X-Kedge-User"); got != "caller" {
		t.Fatalf("user = %q", got)
	}
	if got := forwarded.Header.Get("X-Request-ID"); got != "request-1" {
		t.Fatalf("request id = %q", got)
	}
	if got := forwarded.Header.Get("Idempotency-Key"); got != "idem-1" {
		t.Fatalf("idempotency key = %q", got)
	}
	if strings.Contains(string(body), `"provider"`) || strings.Contains(string(body), `"actionVersion"`) {
		t.Fatalf("forward body leaked hub envelope: %s", body)
	}
	var envelope map[string]any
	if err := json.Unmarshal(rw.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	for key, want := range map[string]string{
		"requestID":     "request-1",
		"provider":      "databricks",
		"action":        "query_table",
		"actionVersion": "v1",
	} {
		if got, _ := envelope[key].(string); got != want {
			t.Fatalf("envelope[%q] = %q, want %q; envelope=%#v", key, got, want, envelope)
		}
	}
	if _, ok := envelope["resourceRef"]; !ok {
		t.Fatalf("response envelope omitted resourceRef: %#v", envelope)
	}
	if _, ok := envelope["result"]; !ok {
		t.Fatalf("response envelope = %#v", envelope)
	}
}

func TestProviderActionRejectsSpoofedOrMissingIdentity(t *testing.T) {
	base, _ := url.Parse("https://provider.invalid/vw")
	reg := providers.NewRegistry()
	reg.Upsert(providers.Provider{Name: "db", EndpointsValid: true, VirtualWorkspaceURL: base, Actions: []providers.ProviderAction{mustAction(t, "a/v1", "g/v1", "Thing", "things", `{}`, `{}`, providersv1alpha1.ProviderActionIdempotencyInherent, providers.ProviderActionLimits{})}})
	h := New(Options{Registry: reg, Logger: logr.Discard()})
	requestBody := `{"provider":"db","action":"a","actionVersion":"v1","resourceRef":{"apiVersion":"g/v1","kind":"Thing","resource":"things","name":"one"},"input":{}}`
	for _, tc := range []struct {
		name      string
		auth      string
		expect    int
		retryable bool
	}{
		{name: "missing auth", expect: http.StatusUnauthorized},
		{name: "missing resolver", auth: "Bearer caller", expect: http.StatusServiceUnavailable, retryable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, PathInvoke, strings.NewReader(requestBody))
			if tc.auth != "" {
				r.Header.Set("Authorization", tc.auth)
			}
			rw := httptest.NewRecorder()
			h.ServeHTTP(rw, r)
			if rw.Code != tc.expect {
				t.Fatalf("status = %d, want %d; body=%s", rw.Code, tc.expect, rw.Body.String())
			}
			var body struct {
				Error struct {
					Code      string `json:"code"`
					Message   string `json:"message"`
					Retryable bool   `json:"retryable"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error envelope: %v", err)
			}
			if body.Error.Code == "" || body.Error.Message == "" {
				t.Fatalf("error envelope = %#v", body)
			}
			if body.Error.Retryable != tc.retryable {
				t.Fatalf("error retryable = %t, want %t: %#v", body.Error.Retryable, tc.retryable, body)
			}
		})
	}
}

func TestProviderActionRejectsCatalogMismatch(t *testing.T) {
	base, _ := url.Parse("https://provider.invalid/vw")
	reg := providers.NewRegistry()
	action := mustAction(t, "a/v1", "g/v1", "Thing", "things", `{}`, `{}`, providersv1alpha1.ProviderActionIdempotencyInherent, providers.ProviderActionLimits{})
	reg.Upsert(providers.Provider{Name: "db", EndpointsValid: true, VirtualWorkspaceURL: base, Actions: []providers.ProviderAction{action}})
	h := New(Options{
		Registry: reg, Logger: logr.Discard(),
		TenantResolver:  fakeTenantResolver(func(*http.Request) (string, string, error) { return "u", "t", nil }),
		ClusterResolver: func(context.Context, string) (string, error) { return "c", nil },
	})
	r := httptest.NewRequest(http.MethodPost, PathInvoke, strings.NewReader(fmt.Sprintf(`{"provider":"db","action":"a","actionVersion":"v1","schemaDigest":%q,"resourceRef":{"apiVersion":"g/v1","kind":"Other","resource":"things","name":"one"},"input":{}}`, action.SchemaDigest)))
	r.Header.Set("Authorization", "Bearer caller")
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, r)
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", rw.Code, rw.Body.String())
	}
}

func TestProviderActionRequiresExactSchemaDigestBeforeProviderCall(t *testing.T) {
	base, _ := url.Parse("https://provider.invalid/vw")
	action := mustAction(t, "a/v1", "g/v1", "Thing", "things", `{}`, `{"type":"object"}`, providersv1alpha1.ProviderActionIdempotencyInherent, providers.ProviderActionLimits{})
	reg := providers.NewRegistry()
	reg.Upsert(providers.Provider{Name: "db", EndpointsValid: true, VirtualWorkspaceURL: base, Actions: []providers.ProviderAction{action}})
	calls := 0
	h := New(Options{
		Registry: reg, Logger: logr.Discard(),
		TenantResolver:  fakeTenantResolver(func(*http.Request) (string, string, error) { return "human", "root:kedge:tenants:org:ws", nil }),
		ClusterResolver: func(context.Context, string) (string, error) { return "cluster", nil },
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		})},
	})
	request := func(digestField string) *http.Request {
		body := fmt.Sprintf(`{"provider":"db","action":"a","actionVersion":"v1"%s,"resourceRef":{"apiVersion":"g/v1","kind":"Thing","resource":"things","name":"one"},"input":{}}`, digestField)
		r := httptest.NewRequest(http.MethodPost, PathInvoke, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer human")
		return r
	}
	missing := httptest.NewRecorder()
	h.ServeHTTP(missing, request(""))
	if missing.Code != http.StatusBadRequest || calls != 0 {
		t.Fatalf("missing digest status=%d calls=%d body=%s", missing.Code, calls, missing.Body.String())
	}
	wrong := httptest.NewRecorder()
	h.ServeHTTP(wrong, request(`,"schemaDigest":"sha256:0000000000000000000000000000000000000000000000000000000000000000"`))
	if wrong.Code != http.StatusConflict || !strings.Contains(wrong.Body.String(), "action_schema_mismatch") || calls != 0 {
		t.Fatalf("wrong digest status=%d calls=%d body=%s", wrong.Code, calls, wrong.Body.String())
	}
	correct := httptest.NewRecorder()
	h.ServeHTTP(correct, request(fmt.Sprintf(`,"schemaDigest":%q`, action.SchemaDigest)))
	if correct.Code != http.StatusOK || calls != 1 {
		t.Fatalf("correct digest status=%d calls=%d body=%s", correct.Code, calls, correct.Body.String())
	}
}

func TestProviderActionEnforcesCatalogIdempotencyAndLimits(t *testing.T) {
	base, _ := url.Parse("https://provider.invalid/vw")
	reg := providers.NewRegistry()
	action := mustAction(t, "a/v1", "g/v1", "Thing", "things", `{"type":"object","additionalProperties":false}`, `{"type":"object","required":["ok"]}`, providersv1alpha1.ProviderActionIdempotencyKeyed, providers.ProviderActionLimits{MaxInputBytes: 2, MaxOutputBytes: 1024, MaxResultItems: 10})
	reg.Upsert(providers.Provider{
		Name: "db", EndpointsValid: true, VirtualWorkspaceURL: base,
		Actions: []providers.ProviderAction{action},
	})
	calls := 0
	h := New(Options{
		Registry: reg, Logger: logr.Discard(),
		TenantResolver:  fakeTenantResolver(func(*http.Request) (string, string, error) { return "u", "t", nil }),
		ClusterResolver: func(context.Context, string) (string, error) { return "c", nil },
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		})},
	})
	requestBody := fmt.Sprintf(`{"provider":"db","action":"a","actionVersion":"v1","schemaDigest":%q,"resourceRef":{"apiVersion":"g/v1","kind":"Thing","resource":"things","name":"one"},"input":{}}`, action.SchemaDigest)
	newRequest := func(body string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, PathInvoke, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer caller")
		return r
	}
	missingKey := httptest.NewRecorder()
	h.ServeHTTP(missingKey, newRequest(requestBody))
	if missingKey.Code != http.StatusBadRequest || !strings.Contains(missingKey.Body.String(), "idempotency_key_required") {
		t.Fatalf("missing key response = %d %s", missingKey.Code, missingKey.Body.String())
	}
	tooLarge := httptest.NewRecorder()
	h.ServeHTTP(tooLarge, newRequest(strings.Replace(requestBody, `"input":{}`, `"input":{"x":1}`, 1)))
	if tooLarge.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too-large input status = %d body=%s", tooLarge.Code, tooLarge.Body.String())
	}
	invalidOutput := httptest.NewRecorder()
	r := newRequest(requestBody)
	r.Header.Set("Idempotency-Key", "key-1")
	h.ServeHTTP(invalidOutput, r)
	if invalidOutput.Code != http.StatusNotImplemented || !strings.Contains(invalidOutput.Body.String(), "action_idempotency_unsupported") {
		t.Fatalf("invalid output response = %d %s", invalidOutput.Code, invalidOutput.Body.String())
	}
	second := httptest.NewRecorder()
	r = newRequest(requestBody)
	r.Header.Set("Idempotency-Key", "key-1")
	h.ServeHTTP(second, r)
	if second.Code != http.StatusNotImplemented || calls != 0 {
		t.Fatalf("keyed invocation reached provider or changed status: status=%d calls=%d body=%s", second.Code, calls, second.Body.String())
	}
}

func TestProviderActionUsesFullCompiledSchemaConstraints(t *testing.T) {
	base, _ := url.Parse("https://provider.invalid/vw")
	reg := providers.NewRegistry()
	action := mustAction(t, "a/v1", "g/v1", "Thing", "things",
		`{"type":"object","required":["profile"],"properties":{"profile":{"type":"object","required":["email","age"],"properties":{"email":{"type":"string","format":"email"},"age":{"type":"integer","minimum":18}},"additionalProperties":false}},"additionalProperties":false}`,
		`{"type":"object","required":["ok"],"properties":{"ok":{"type":"integer","minimum":2}},"additionalProperties":false}`,
		providersv1alpha1.ProviderActionIdempotencyInherent, providers.ProviderActionLimits{})
	reg.Upsert(providers.Provider{
		Name: "db", EndpointsValid: true, VirtualWorkspaceURL: base,
		Actions: []providers.ProviderAction{action},
	})
	calls := 0
	h := New(Options{
		Registry: reg, Logger: logr.Discard(),
		TenantResolver:  fakeTenantResolver(func(*http.Request) (string, string, error) { return "u", "t", nil }),
		ClusterResolver: func(context.Context, string) (string, error) { return "c", nil },
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"ok":1}`))}, nil
		})},
	})
	request := func(input string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, PathInvoke, strings.NewReader(fmt.Sprintf(`{"provider":"db","action":"a","actionVersion":"v1","schemaDigest":%q,"resourceRef":{"apiVersion":"g/v1","kind":"Thing","resource":"things","name":"one"},"input":%s}`, action.SchemaDigest, input)))
		r.Header.Set("Authorization", "Bearer caller")
		return r
	}
	badInput := httptest.NewRecorder()
	h.ServeHTTP(badInput, request(`{"profile":{"email":"not-an-email","age":17}}`))
	if badInput.Code != http.StatusBadRequest || !strings.Contains(badInput.Body.String(), "input_invalid") || calls != 0 {
		t.Fatalf("bad nested input response=%d calls=%d body=%s", badInput.Code, calls, badInput.Body.String())
	}
	validInput := httptest.NewRecorder()
	h.ServeHTTP(validInput, request(`{"profile":{"email":"user@example.com","age":21}}`))
	if validInput.Code != http.StatusBadGateway || !strings.Contains(validInput.Body.String(), "provider_invalid_response") || calls != 1 {
		t.Fatalf("bad nested output response=%d calls=%d body=%s", validInput.Code, calls, validInput.Body.String())
	}
}

func mustAction(t *testing.T, id, apiVersion, kind, resource, inputSchema, outputSchema string, idempotency providersv1alpha1.ProviderActionIdempotency, limits providers.ProviderActionLimits) providers.ProviderAction {
	t.Helper()
	spec := providersv1alpha1.ProviderActionSpec{
		ID: id, DisplayName: id,
		BoundResource: providersv1alpha1.ProviderActionBoundResource{APIVersion: apiVersion, Kind: kind, Resource: resource},
		InputSchema:   &runtime.RawExtension{Raw: json.RawMessage(inputSchema)},
		OutputSchema:  &runtime.RawExtension{Raw: json.RawMessage(outputSchema)},
		ExecutionMode: providersv1alpha1.ProviderActionExecutionSync,
		Risk:          providersv1alpha1.ProviderActionRiskLow,
		Idempotency:   idempotency,
		Limits:        providersv1alpha1.ProviderActionLimits{TimeoutSeconds: 45, MaxInputBytes: limits.MaxInputBytes, MaxOutputBytes: limits.MaxOutputBytes, MaxResultItems: limits.MaxResultItems},
	}
	digest, err := providersv1alpha1.ProviderActionSchemaDigest(spec)
	if err != nil {
		t.Fatalf("digest test action %q: %v", id, err)
	}
	spec.SchemaDigest = digest
	parsed, err := providers.ParseProviderActions([]providersv1alpha1.ProviderActionSpec{spec})
	if err != nil {
		t.Fatalf("parse test action %q: %v", id, err)
	}
	if limits.MaxInputBytes == 0 {
		parsed[0].Limits.MaxInputBytes = 0
	}
	if limits.MaxOutputBytes == 0 {
		parsed[0].Limits.MaxOutputBytes = 0
	}
	if limits.MaxResultItems == 0 {
		parsed[0].Limits.MaxResultItems = 0
	}
	return parsed[0]
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
