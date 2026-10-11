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
	"strconv"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// ReadRateLimit options. GET /rate_limit is one cheap call; a failure is
// retried a few times and then left to the next refresh, so a GitHub
// outage never pins the budget's main loop.
const (
	rateLimitTimeout     = 30 * time.Second
	rateLimitRetryFirst  = 10 * time.Second
	rateLimitRetryMax    = 5 * time.Minute
	rateLimitMaxAttempts = 5
)

// TaskPriority is the priority every boopd workflow start and activity
// carries (DESIGN-0001 § RepoWorkflow, Priority). The fairness key is
// the installation, so a 15,000-repository installation cannot starve a
// 50-repository one on the shared task queue; the priority key orders
// human-triggered rechecks ahead of scheduled runs.
func TaskPriority(p Priority, installationID int64) temporal.Priority {
	return temporal.Priority{PriorityKey: int(p), FairnessKey: strconv.FormatInt(installationID, 10)}
}

// rateLimitOptions runs ReadRateLimit from the InstallationWorkflow.
func rateLimitOptions(ctx workflow.Context, installationID int64) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: rateLimitTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    rateLimitRetryFirst,
			BackoffCoefficient: 2,
			MaximumInterval:    rateLimitRetryMax,
			MaximumAttempts:    rateLimitMaxAttempts,
		},
		Priority: TaskPriority(PriorityNormal, installationID),
	})
}

// RunRenovate options (DESIGN-0001 § RunRenovate activity, OQ10). The
// workflow decides every retry, so the activity runs once.
const (
	RunScheduleToStart = 5 * time.Minute
	RunStartToClose    = 58 * time.Minute
	RunHeartbeat       = time.Minute
)

// runOptions runs RunRenovate at priority p.
func runOptions(ctx workflow.Context, p Priority, installationID int64) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		ScheduleToStartTimeout: RunScheduleToStart,
		StartToCloseTimeout:    RunStartToClose,
		HeartbeatTimeout:       RunHeartbeat,
		RetryPolicy:            &temporal.RetryPolicy{MaximumAttempts: 1},
		Priority:               TaskPriority(p, installationID),
	})
}

// shortOptions run the quick activities (PlanRun, CheckRepo,
// AcquireBudget, ListInstallations): a few retries, then the workflow
// decides.
func shortOptions(ctx workflow.Context, p Priority, installationID int64) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    5 * time.Second,
			BackoffCoefficient: 2,
			MaximumInterval:    time.Minute,
			MaximumAttempts:    5,
		},
		Priority: TaskPriority(p, installationID),
	})
}

// DiscoverInstallation options. A pass over a large installation can
// sleep to a rate-limit reset; it heartbeats every page and every
// reserve tick, and a retry resumes from the last heartbeat's page.
const (
	DiscoverStartToClose = 3 * time.Hour
	DiscoverHeartbeat    = 2 * time.Minute
	discoverMaxAttempts  = 3
)

func discoverOptions(ctx workflow.Context, installationID int64) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: DiscoverStartToClose,
		HeartbeatTimeout:    DiscoverHeartbeat,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    30 * time.Second,
			BackoffCoefficient: 2,
			MaximumAttempts:    discoverMaxAttempts,
		},
		Priority: TaskPriority(PriorityNormal, installationID),
	})
}
