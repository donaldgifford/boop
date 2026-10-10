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

package activities

import (
	"context"
	"fmt"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"

	"github.com/donaldgifford/boop/internal/workflows"
)

// BudgetConfig is an App's budget.* config, handed to every
// InstallationWorkflow the activity starts (DESIGN-0001 § API /
// Interface Changes).
type BudgetConfig struct {
	ReserveFraction   float64
	MaxConcurrentRuns int
	DefaultEstimates  map[string]float64
	LeaseTTL          time.Duration

	// MaxHandled is passed to new InstallationWorkflows; 0 keeps the
	// workflow default. Load tests set it.
	MaxHandled int
}

// DefaultBudgetConfig is the config file's defaults (OQ5, OQ6, OQ10).
func DefaultBudgetConfig() BudgetConfig {
	return BudgetConfig{
		ReserveFraction:   workflows.DefaultReserveFraction,
		MaxConcurrentRuns: workflows.DefaultMaxConcurrentRuns,
		DefaultEstimates: map[string]float64{
			workflows.ResourceCore:    workflows.DefaultEstimateCore,
			workflows.ResourceGraphQL: workflows.DefaultEstimateGraphQL,
		},
		LeaseTTL: workflows.DefaultLeaseTTL,
	}
}

// Budget is the AcquireBudget activity. Workflow code cannot call
// Update-with-Start, so RepoWorkflow reaches its InstallationWorkflow
// through this activity.
type Budget struct {
	client    client.Client
	taskQueue string
	cfg       BudgetConfig
}

// NewBudget returns the budget activity, starting InstallationWorkflows
// on taskQueue with cfg.
func NewBudget(c client.Client, taskQueue string, cfg BudgetConfig) *Budget {
	return &Budget{client: c, taskQueue: taskQueue, cfg: cfg}
}

// AcquireBudget sends acquire to installation/github/<id>, starting the
// InstallationWorkflow if it is not running. The same UpdateID returns
// the first attempt's answer, so a retried activity never takes a
// second lease.
func (b *Budget) AcquireBudget(ctx context.Context, in *workflows.AcquireInput) (*workflows.AcquireResult, error) {
	id := workflows.InstallationWorkflowID(in.InstallationID)

	start := b.client.NewWithStartWorkflowOperation(client.StartWorkflowOptions{
		ID:                       id,
		TaskQueue:                b.taskQueue,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
		Priority:                 workflows.TaskPriority(in.Request.Priority, in.InstallationID),
	}, workflows.InstallationWorkflowName, &workflows.InstallationWorkflowInput{
		InstallationID:    in.InstallationID,
		ReserveFraction:   b.cfg.ReserveFraction,
		MaxConcurrentRuns: b.cfg.MaxConcurrentRuns,
		DefaultEstimates:  b.cfg.DefaultEstimates,
		LeaseTTL:          b.cfg.LeaseTTL,
		MaxHandled:        b.cfg.MaxHandled,
	})

	handle, err := b.client.UpdateWithStartWorkflow(ctx, client.UpdateWithStartWorkflowOptions{
		StartWorkflowOperation: start,
		UpdateOptions: client.UpdateWorkflowOptions{
			UpdateID:     in.UpdateID,
			UpdateName:   workflows.AcquireUpdate,
			Args:         []any{&in.Request},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("acquire on %s: %w", id, err)
	}

	var res workflows.AcquireResult
	if err := handle.Get(ctx, &res); err != nil {
		return nil, fmt.Errorf("acquire on %s: %w", id, err)
	}

	return &res, nil
}

// Register registers AcquireBudget under its workflows package name.
func (b *Budget) Register(r Registry) {
	r.RegisterActivityWithOptions(b.AcquireBudget, activity.RegisterOptions{Name: workflows.AcquireBudgetActivity})
}
