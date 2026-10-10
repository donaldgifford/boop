/*
Copyright 2026.

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

// Package observability holds boopd's logs, metrics and health
// endpoints (DESIGN-0001 § Observability). Metrics is the boopd metric
// set over an OpenTelemetry meter; instruments carry their full
// Prometheus names, so the exporter must add no unit or counter
// suffixes.
package observability

import (
	"context"
	"errors"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// MeterName is the instrumentation scope of the boopd metric set.
const MeterName = "boopd"

// Metric names (DESIGN-0001 § Observability).
const (
	RunsTotal             = "boopd_runs_total"
	RunsActive            = "boopd_runs_active"
	RunDurationSeconds    = "boopd_run_duration_seconds"
	RunPodStartSeconds    = "boopd_run_pod_start_seconds"
	RunOverheadSeconds    = "boopd_run_overhead_seconds"
	RunPendingTimeouts    = "boopd_run_pending_timeouts_total"
	TokenMintsTotal       = "boopd_token_mints_total"       //nolint:gosec // G101: a metric name
	TokenRevocationsTotal = "boopd_token_revocations_total" //nolint:gosec // G101: a metric name
	DiscoveryProbeCost    = "boopd_discovery_probe_cost"
)

// runBuckets span a pod start of seconds to a run of the full hour.
var runBuckets = []float64{1, 5, 10, 30, 60, 120, 300, 600, 1200, 1800, 2700, 3600}

// Metrics records the boopd metric set. It implements
// activities.Metrics.
type Metrics struct {
	runs        metric.Int64Counter
	active      metric.Int64UpDownCounter
	duration    metric.Float64Histogram
	podStart    metric.Float64Histogram
	overhead    metric.Float64Histogram
	pending     metric.Int64Counter
	mints       metric.Int64Counter
	revocations metric.Int64Counter
	probeCost   metric.Int64Counter
}

// NewMetrics creates the instruments on mp's boopd meter. Call it once
// per provider.
func NewMetrics(mp metric.MeterProvider) (*Metrics, error) {
	m := mp.Meter(MeterName)
	var (
		out  Metrics
		errs []error
	)
	add := func(err error) { errs = append(errs, err) }
	hist := func(name, desc string) metric.Float64Histogram {
		h, err := m.Float64Histogram(name, metric.WithDescription(desc), metric.WithExplicitBucketBoundaries(runBuckets...))
		add(err)
		return h
	}
	counter := func(name, desc string) metric.Int64Counter {
		c, err := m.Int64Counter(name, metric.WithDescription(desc))
		add(err)
		return c
	}
	out.runs = counter(RunsTotal, "Renovate runs by outcome, repository result and profile.")
	active, err := m.Int64UpDownCounter(RunsActive, metric.WithDescription("Renovate runs in flight by profile."))
	add(err)
	out.active = active
	out.duration = hist(RunDurationSeconds, "Pod Running to container exit, by profile.")
	out.podStart = hist(RunPodStartSeconds, "Job creation to pod Running.")
	out.overhead = hist(RunOverheadSeconds, "Pod Running to the first Renovate log line.")
	out.pending = counter(RunPendingTimeouts, "Runs whose pod was not Running within the pending timeout.")
	out.mints = counter(TokenMintsTotal, "Run token mints by outcome.")
	out.revocations = counter(TokenRevocationsTotal, "Run token revocations by outcome.")
	out.probeCost = counter(DiscoveryProbeCost, "Discovery config-probe spend by installation and resource.")
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return &out, nil
}

// RunStarted implements activities.Metrics.
func (m *Metrics) RunStarted(profile string) {
	m.active.Add(context.Background(), 1, metric.WithAttributes(attribute.String("profile", profile)))
}

// RunEnded implements activities.Metrics.
func (m *Metrics) RunEnded(profile string) {
	m.active.Add(context.Background(), -1, metric.WithAttributes(attribute.String("profile", profile)))
}

// RunCompleted implements activities.Metrics.
func (m *Metrics) RunCompleted(outcome, result, profile string, duration, podStart, overhead time.Duration) {
	ctx := context.Background()
	byProfile := metric.WithAttributes(attribute.String("profile", profile))
	m.runs.Add(ctx, 1, metric.WithAttributes(
		attribute.String("outcome", outcome), attribute.String("result", result), attribute.String("profile", profile)))
	if duration > 0 {
		m.duration.Record(ctx, duration.Seconds(), byProfile)
	}
	if podStart > 0 {
		m.podStart.Record(ctx, podStart.Seconds())
	}
	if overhead > 0 {
		m.overhead.Record(ctx, overhead.Seconds())
	}
}

// PendingTimeout implements activities.Metrics.
func (m *Metrics) PendingTimeout() { m.pending.Add(context.Background(), 1) }

// TokenMint implements activities.Metrics.
func (m *Metrics) TokenMint(outcome string) {
	m.mints.Add(context.Background(), 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}

// TokenRevocation implements activities.Metrics.
func (m *Metrics) TokenRevocation(outcome string) {
	m.revocations.Add(context.Background(), 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}

// ProbeCost implements activities.Metrics.
func (m *Metrics) ProbeCost(installationID int64, resource string, cost int) {
	m.probeCost.Add(context.Background(), int64(cost), metric.WithAttributes(
		attribute.String("installation", strconv.FormatInt(installationID, 10)), attribute.String("resource", resource)))
}
