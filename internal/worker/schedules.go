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

// Package worker assembles boopd's worker role (DESIGN-0001 § API /
// Interface Changes): the discovery schedules, the clients, the
// activities and workflows on one Temporal worker, and the health and
// metrics endpoints. cmd/boopd calls it, and so does the e2e suite.
package worker

import (
	"context"
	"fmt"

	"go.temporal.io/sdk/client"

	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/temporal"
	"github.com/donaldgifford/boop/internal/workflows"
)

// DiscoverySchedules is one Schedule per configured App: discovery/<app>
// every discovery.every, starting DiscoveryWorkflow for that app, with
// overlap skip (EnsureSchedule's policy).
func DiscoverySchedules(cfg *config.Config, taskQueue string) []*temporal.Schedule {
	out := make([]*temporal.Schedule, 0, len(cfg.Apps))
	for _, app := range cfg.Apps {
		out = append(out, &temporal.Schedule{
			ID:        workflows.DiscoveryScheduleID(app.Name),
			Every:     app.Discovery.Every,
			Workflow:  workflows.DiscoveryWorkflowName,
			Args:      []any{&workflows.DiscoveryInput{App: app.Name}},
			TaskQueue: taskQueue,
			Priority:  workflows.TaskPriority(workflows.PriorityNormal, 0),
		})
	}
	return out
}

// EnsureSchedules creates or updates every discovery schedule. Every
// worker calls it at start; it is idempotent.
func EnsureSchedules(ctx context.Context, c client.Client, cfg *config.Config, taskQueue string) error {
	for _, s := range DiscoverySchedules(cfg, taskQueue) {
		if err := temporal.EnsureSchedule(ctx, c, s); err != nil {
			return fmt.Errorf("discovery schedule: %w", err)
		}
	}
	return nil
}
