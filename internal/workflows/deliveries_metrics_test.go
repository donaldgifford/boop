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

package workflows

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"testing"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// countingHandler is a metrics handler that sums counters by name and
// tags.
type countingHandler struct {
	tags   map[string]string
	counts map[string]int64
}

func (h *countingHandler) WithTags(tags map[string]string) client.MetricsHandler {
	merged := maps.Clone(h.tags)
	if merged == nil {
		merged = make(map[string]string)
	}
	maps.Copy(merged, tags)
	return &countingHandler{tags: merged, counts: h.counts}
}

func (h *countingHandler) Counter(name string) client.MetricsCounter {
	return counterFunc(func(d int64) {
		h.counts[fmt.Sprintf("%s{app=%s,event=%s}", name, h.tags["app"], h.tags["event"])] += d
	})
}

func (*countingHandler) Gauge(string) client.MetricsGauge { return client.MetricsNopHandler.Gauge("") }

func (*countingHandler) Timer(string) client.MetricsTimer { return client.MetricsNopHandler.Timer("") }

type counterFunc func(int64)

func (c counterFunc) Inc(d int64) { c(d) }

// The pass's failed and missed counts move their counters.
func TestDeliveryAuditWorkflow_CountsFailedAndMissed(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	suite.SetLogger(log.NewStructuredLogger(slog.New(slog.DiscardHandler)))
	h := &countingHandler{counts: make(map[string]int64)}
	suite.SetMetricsHandler(h)
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflowWithOptions(DeliveryAuditWorkflow, workflow.RegisterOptions{Name: DeliveryAuditWorkflowName})
	env.RegisterActivityWithOptions(func(context.Context, *ListHookDeliveriesInput) (*DeliveryAudit, error) {
		return &DeliveryAudit{Newest: 9, Read: 4, Counts: []DeliveryCounts{
			{Event: "issues", Missed: 2},
			{Event: "push", Failed: 1, OK: 1},
		}}, nil
	}, activity.RegisterOptions{Name: ListHookDeliveriesActivity})
	env.ExecuteWorkflow(DeliveryAuditWorkflowName, &DeliveryAuditInput{App: "main"})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{
		MetricDeliveriesMissed + "{app=main,event=issues}": 2,
		MetricDeliveriesFailed + "{app=main,event=issues}": 0,
		MetricDeliveriesMissed + "{app=main,event=push}":   0,
		MetricDeliveriesFailed + "{app=main,event=push}":   1,
	}
	for k, v := range want {
		if h.counts[k] != v {
			t.Errorf("%s = %d, want %d", k, h.counts[k], v)
		}
	}
}
