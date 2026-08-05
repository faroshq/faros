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
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/component-base/metrics/legacyregistry"
)

const (
	metricProvider       = "provider"
	metricAction         = "action"
	metricVersion        = "version"
	metricOutcome        = "outcome"
	metricStatus         = "status"
	metricErrorCode      = "error_code"
	metricOutcomeSuccess = "success"
	metricOutcomeError   = "error"
	metricUnknown        = "unknown"
	metricNoError        = "none"
)

var providerActionMetricLabels = []string{
	metricProvider,
	metricAction,
	metricVersion,
	metricOutcome,
	metricStatus,
	metricErrorCode,
}

// Metrics contains the provider-action metrics. A Metrics value can be
// constructed with a test registry; production handlers use DefaultMetrics,
// which is registered exactly once in the component-base legacy registry.
type Metrics struct {
	Invocations   *prometheus.CounterVec
	Duration      *prometheus.HistogramVec
	RequestBytes  *prometheus.HistogramVec
	ResponseBytes *prometheus.HistogramVec
}

// NewMetrics creates and registers a provider-action metric set in registerer.
// The registerer is supplied so tests can use an isolated registry rather than
// mutating the process-wide scrape surface.
func NewMetrics(registerer prometheus.Registerer) (*Metrics, error) {
	if registerer == nil {
		return nil, &metricsConfigError{"registerer must not be nil"}
	}

	m := &Metrics{
		Invocations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "kedge_provider_action_invocations_total",
			Help: "Total provider action invocations completed by the hub.",
		}, providerActionMetricLabels),
		Duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "kedge_provider_action_duration_seconds",
			Help:    "Provider action completion latency in seconds.",
			Buckets: prometheus.DefBuckets,
		}, providerActionMetricLabels),
		RequestBytes: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "kedge_provider_action_request_bytes",
			Help:    "Provider action request body sizes in bytes.",
			Buckets: []float64{0, 100, 1_000, 10_000, 100_000, 1_000_000},
		}, providerActionMetricLabels),
		ResponseBytes: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "kedge_provider_action_response_bytes",
			Help:    "Provider action response body sizes in bytes.",
			Buckets: []float64{0, 100, 1_000, 10_000, 100_000, 1_000_000, 8_000_000},
		}, providerActionMetricLabels),
	}

	collectors := []prometheus.Collector{
		m.Invocations,
		m.Duration,
		m.RequestBytes,
		m.ResponseBytes,
	}
	registered := make([]prometheus.Collector, 0, len(collectors))
	for _, collector := range collectors {
		if err := registerer.Register(collector); err != nil {
			for _, previous := range registered {
				registerer.Unregister(previous)
			}
			return nil, err
		}
		registered = append(registered, collector)
	}
	return m, nil
}

type metricsConfigError struct{ message string }

func (e *metricsConfigError) Error() string { return e.message }

var (
	defaultMetricsOnce sync.Once
	defaultMetrics     *Metrics
)

// DefaultMetrics returns the process-wide provider-action metrics. The
// component-base registry is global, so this initialization must remain
// guarded when a test or process constructs multiple hub servers.
func DefaultMetrics() *Metrics {
	defaultMetricsOnce.Do(func() {
		var err error
		defaultMetrics, err = NewMetrics(legacyregistry.Registerer())
		if err != nil {
			// A duplicate registration should never take down the hub. This can
			// only happen if another package has claimed one of these names before
			// this package initialized; retain a no-op set in that unusual case.
			defaultMetrics = &Metrics{}
		}
	})
	return defaultMetrics
}

// MetricsHandler returns the hub's component-base registry scrape handler.
// Calling it more than once is safe; metric registration itself is guarded by
// DefaultMetrics.
func MetricsHandler() http.Handler {
	DefaultMetrics()
	return legacyregistry.Handler()
}

func (m *Metrics) observe(labels []string, durationSeconds float64, requestBytes, responseBytes int64) {
	if m == nil {
		return
	}
	if m.Invocations != nil {
		m.Invocations.WithLabelValues(labels...).Inc()
	}
	if m.Duration != nil {
		m.Duration.WithLabelValues(labels...).Observe(durationSeconds)
	}
	if m.RequestBytes != nil {
		m.RequestBytes.WithLabelValues(labels...).Observe(float64(requestBytes))
	}
	if m.ResponseBytes != nil {
		m.ResponseBytes.WithLabelValues(labels...).Observe(float64(responseBytes))
	}
}
