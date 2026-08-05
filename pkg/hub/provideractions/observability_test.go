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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	providersv1alpha1 "github.com/faroshq/faros-kedge/apis/providers/v1alpha1"
	"github.com/faroshq/faros-kedge/pkg/hub/providers"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestProviderActionMetricsRecordSuccessAndErrorWithoutPayloadLabels(t *testing.T) {
	base, _ := url.Parse("https://provider.invalid/vw")
	action := mustAction(t, "a/v1", "g/v1", "Thing", "things", `{}`, `{}`, providersv1alpha1.ProviderActionIdempotencyInherent, providers.ProviderActionLimits{})
	reg := providers.NewRegistry()
	reg.Upsert(providers.Provider{Name: "db", EndpointsValid: true, VirtualWorkspaceURL: base, Actions: []providers.ProviderAction{action}})
	metricsRegistry := prometheus.NewRegistry()
	metrics, err := NewMetrics(metricsRegistry)
	if err != nil {
		t.Fatalf("register metrics: %v", err)
	}
	h := newObservabilityHandler(reg, metrics, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
	})

	success := httptest.NewRecorder()
	h.ServeHTTP(success, observabilityRequest(action.SchemaDigest, `{"secret":"do-not-label"}`))
	if success.Code != http.StatusOK {
		t.Fatalf("success status = %d, body=%s", success.Code, success.Body.String())
	}
	errorResponse := httptest.NewRecorder()
	h.ServeHTTP(errorResponse, observabilityRequest("sha256:0000000000000000000000000000000000000000000000000000000000000000", `{}`))
	if errorResponse.Code != http.StatusConflict {
		t.Fatalf("error status = %d, body=%s", errorResponse.Code, errorResponse.Body.String())
	}

	assertMetricLabels(t, metricsRegistry, "kedge_provider_action_invocations_total", map[string]string{
		"provider": "db", "action": "a", "version": "v1", "outcome": "success", "status": "200", "error_code": "none",
	})
	assertMetricLabels(t, metricsRegistry, "kedge_provider_action_invocations_total", map[string]string{
		"provider": "db", "action": "a", "version": "v1", "outcome": "error", "status": "409", "error_code": "action_schema_mismatch",
	})
	for _, name := range []string{
		"kedge_provider_action_duration_seconds",
		"kedge_provider_action_request_bytes",
		"kedge_provider_action_response_bytes",
	} {
		family := gatherMetricFamily(t, metricsRegistry, name)
		if len(family.Metric) != 2 {
			t.Fatalf("%s metric series = %d, want success and error series", name, len(family.Metric))
		}
	}

	for _, family := range gatherMetricFamilies(t, metricsRegistry) {
		for _, metric := range family.Metric {
			for _, label := range metric.Label {
				if strings.Contains(label.GetValue(), "do-not-label") || strings.Contains(label.GetValue(), "taxi-trips") {
					t.Fatalf("metric %s exposed payload/resource label %s=%q", family.GetName(), label.GetName(), label.GetValue())
				}
			}
		}
	}
}

func TestProviderActionTraceHeadersPropagateOnlyWhenValid(t *testing.T) {
	base, _ := url.Parse("https://provider.invalid/vw")
	action := mustAction(t, "a/v1", "g/v1", "Thing", "things", `{}`, `{}`, providersv1alpha1.ProviderActionIdempotencyInherent, providers.ProviderActionLimits{})
	reg := providers.NewRegistry()
	reg.Upsert(providers.Provider{Name: "db", EndpointsValid: true, VirtualWorkspaceURL: base, Actions: []providers.ProviderAction{action}})
	var forwarded []*http.Request
	h := newObservabilityHandler(reg, nil, func(r *http.Request) (*http.Response, error) {
		forwarded = append(forwarded, r)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})

	valid := observabilityRequest(action.SchemaDigest, `{}`)
	valid.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	valid.Header.Set("tracestate", "congo=t61rcWkgMzE")
	validResponse := httptest.NewRecorder()
	h.ServeHTTP(validResponse, valid)
	if validResponse.Code != http.StatusOK {
		t.Fatalf("valid trace status = %d, body=%s", validResponse.Code, validResponse.Body.String())
	}
	if got := forwarded[0].Header.Get("traceparent"); got != valid.Header.Get("traceparent") {
		t.Fatalf("traceparent = %q, want %q", got, valid.Header.Get("traceparent"))
	}
	if got := forwarded[0].Header.Get("tracestate"); got != valid.Header.Get("tracestate") {
		t.Fatalf("tracestate = %q, want %q", got, valid.Header.Get("tracestate"))
	}

	malformed := observabilityRequest(action.SchemaDigest, `{}`)
	malformed.Header.Set("traceparent", "00-00000000000000000000000000000000-00f067aa0ba902b7-01")
	malformed.Header.Set("tracestate", strings.Repeat("x", maxHeaderValueLength+1))
	malformedResponse := httptest.NewRecorder()
	h.ServeHTTP(malformedResponse, malformed)
	if malformedResponse.Code != http.StatusOK {
		t.Fatalf("malformed trace status = %d, body=%s", malformedResponse.Code, malformedResponse.Body.String())
	}
	if got := forwarded[1].Header.Get("traceparent"); got != "" {
		t.Fatalf("malformed traceparent forwarded as %q", got)
	}
	if got := forwarded[1].Header.Get("tracestate"); got != "" {
		t.Fatalf("oversized tracestate forwarded as %q", got)
	}
}

func newObservabilityHandler(reg *providers.Registry, metrics *Metrics, transport roundTripFunc) *Handler {
	return New(Options{
		Registry: reg, Metrics: metrics, HTTPClient: &http.Client{Transport: transport}, Logger: logr.Discard(),
		TenantResolver:  fakeTenantResolver(func(*http.Request) (string, string, error) { return "caller", "root:kedge:tenants:org:ws", nil }),
		ClusterResolver: func(context.Context, string) (string, error) { return "cluster-a", nil },
	})
}

func observabilityRequest(digest, input string) *http.Request {
	body := fmt.Sprintf(`{"provider":"db","action":"a","actionVersion":"v1","schemaDigest":%q,"resourceRef":{"apiVersion":"g/v1","kind":"Thing","resource":"things","name":"taxi-trips"},"input":%s}`, digest, input)
	r := httptest.NewRequest(http.MethodPost, PathInvoke, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer caller")
	return r
}

func gatherMetricFamilies(t *testing.T, registry *prometheus.Registry) []*dto.MetricFamily {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	return families
}

func gatherMetricFamily(t *testing.T, registry *prometheus.Registry, name string) *dto.MetricFamily {
	t.Helper()
	for _, family := range gatherMetricFamilies(t, registry) {
		if family.GetName() == name {
			return family
		}
	}
	t.Fatalf("metric family %q not found", name)
	return nil
}

func assertMetricLabels(t *testing.T, registry *prometheus.Registry, name string, want map[string]string) {
	t.Helper()
	family := gatherMetricFamily(t, registry, name)
	for _, metric := range family.Metric {
		labels := make(map[string]string, len(metric.Label))
		for _, label := range metric.Label {
			labels[label.GetName()] = label.GetValue()
		}
		matches := true
		for key, value := range want {
			if labels[key] != value {
				matches = false
				break
			}
		}
		if matches {
			return
		}
	}
	t.Fatalf("metric %s did not contain labels %#v", name, want)
}
