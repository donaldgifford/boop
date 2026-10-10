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
