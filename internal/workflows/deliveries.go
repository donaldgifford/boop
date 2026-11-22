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
	"go.temporal.io/sdk/workflow"
)

// DeliveryAuditWorkflow audits one App's webhook deliveries (DESIGN-0003
// § Delivery audit): one ListHookDeliveries call for the deliveries
// since its cursor, the failed ones counted in
// boopd_ingest_deliveries_failed_total{app,event} and the missed ones in
// boopd_ingest_deliveries_missed_total{app,event}, then a wait of Every
// and ContinueAsNew with the new cursor. The delivery-audit/<app>
// Schedule starts it, and with overlap skip restarts it only if the
// chain ends. A failed pass keeps the cursor, so the next one re-reads.
// It never redelivers (DESIGN-0003 OQ5).
func DeliveryAuditWorkflow(ctx workflow.Context, in *DeliveryAuditInput) error {
	log := workflow.GetLogger(ctx)
	next := *in
	var res DeliveryAudit
	err := workflow.ExecuteActivity(shortOptions(ctx, PriorityNormal, 0), ListHookDeliveriesActivity,
		&ListHookDeliveriesInput{App: in.App, After: in.Cursor}).Get(ctx, &res)
	if err != nil {
		log.Warn("delivery audit pass failed; the next pass re-reads", "app", in.App, "error", err)
	} else {
		recordDeliveries(ctx, in.App, &res)
		next.Cursor = max(next.Cursor, res.Newest)
	}
	if in.Every <= 0 {
		return nil
	}
	if err := workflow.Sleep(ctx, in.Every); err != nil {
		return err
	}
	return workflow.NewContinueAsNewError(ctx, DeliveryAuditWorkflowName, &next)
}

// recordDeliveries counts one pass's failed and missed deliveries by
// event.
func recordDeliveries(ctx workflow.Context, app string, res *DeliveryAudit) {
	metrics := workflow.GetMetricsHandler(ctx)
	failed, missed := 0, 0
	for _, c := range res.Counts {
		tagged := metrics.WithTags(map[string]string{"app": app, "event": c.Event})
		tagged.Counter(MetricDeliveriesFailed).Inc(int64(c.Failed))
		tagged.Counter(MetricDeliveriesMissed).Inc(int64(c.Missed))
		failed += c.Failed
		missed += c.Missed
	}
	workflow.GetLogger(ctx).Info("delivery audit", "app", app, "read", res.Read, "pages", res.Pages,
		"failed", failed, "missed", missed, "newest", res.Newest)
}
