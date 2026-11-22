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

// StoreMaintenanceWorkflow is one daily retention pass (DESIGN-0004 §
// Retention), started by the store-maintenance Schedule when
// store.enabled: each of MaintenanceRules in turn, as of the workflow's
// start. A failed rule is logged and counted; the others still run, and
// the next day's pass catches up.
func StoreMaintenanceWorkflow(ctx workflow.Context) (*StoreMaintenanceResult, error) {
	log := workflow.GetLogger(ctx)
	now := workflow.Now(ctx)
	out := &StoreMaintenanceResult{}
	for _, rule := range MaintenanceRules {
		var res MaintainStoreResult
		err := workflow.ExecuteActivity(maintainOptions(ctx), MaintainStoreActivity, &MaintainStoreInput{Rule: rule, Now: now}).Get(ctx, &res)
		if err != nil {
			out.Failed++
			log.Warn("store maintenance rule failed", "rule", rule, "error", err)
			continue
		}
		out.Rules = append(out.Rules, res)
	}
	log.Info("store maintenance", "rules", out.Rules, "failed", out.Failed)
	return out, nil
}
