package observability_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/donaldgifford/boop/internal/activities"
	"github.com/donaldgifford/boop/internal/kube"
	"github.com/donaldgifford/boop/internal/observability"
)

var (
	_ activities.Metrics   = (*observability.Metrics)(nil)
	_ kube.RequestRecorder = (*observability.Metrics)(nil)
)

func collect(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Metrics {
	t.Helper()
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	out := make(map[string]metricdata.Metrics)
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			out[m.Name] = m
		}
	}
	return out
}

// TestMetrics_Names pins the metric names the design and dashboards use.
func TestMetrics_Names(t *testing.T) {
	t.Parallel()
	reader := sdkmetric.NewManualReader()
	m, err := observability.NewMetrics(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	m.RunStarted("default")
	m.RunCompleted("Succeeded", "done", "default", time.Minute, 10*time.Second, 2*time.Second)
	m.RunEnded("default")
	m.PendingTimeout()
	m.TokenMint("success")
	m.TokenRevocation("failure")
	m.ProbeCost(1, "graphql", 3)
	m.RecordRequest("create", "jobs", "201")

	got := collect(t, reader)
	names := make([]string, 0, len(got))
	for n := range got {
		names = append(names, n)
	}
	slices.Sort(names)
	want := []string{
		"boopd_discovery_probe_cost",
		"boopd_kube_requests_total",
		"boopd_run_duration_seconds",
		"boopd_run_overhead_seconds",
		"boopd_run_pending_timeouts_total",
		"boopd_run_pod_start_seconds",
		"boopd_runs_active",
		"boopd_runs_total",
		"boopd_token_mints_total",
		"boopd_token_revocations_total",
	}
	if !slices.Equal(names, want) {
		t.Fatalf("metric names:\n got %v\nwant %v", names, want)
	}

	runs := got["boopd_runs_total"].Data.(metricdata.Sum[int64]).DataPoints[0]
	for key, want := range map[string]string{"outcome": "Succeeded", "result": "done", "profile": "default"} {
		if v, ok := runs.Attributes.Value(attribute.Key(key)); !ok || v.AsString() != want {
			t.Errorf("boopd_runs_total %s = %v, want %s", key, v, want)
		}
	}
	if active := got["boopd_runs_active"].Data.(metricdata.Sum[int64]).DataPoints[0].Value; active != 0 {
		t.Errorf("boopd_runs_active = %d after start and end, want 0", active)
	}
	if cost := got["boopd_discovery_probe_cost"].Data.(metricdata.Sum[int64]).DataPoints[0].Value; cost != 3 {
		t.Errorf("boopd_discovery_probe_cost = %d, want 3", cost)
	}
}
