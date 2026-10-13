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
	"errors"
	"fmt"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"

	"github.com/donaldgifford/boop/internal/workflows"
)

// CapacityConfig is runs.max_concurrent and the lease TTL, handed to the
// CapacityWorkflow the activity starts.
type CapacityConfig struct {
	// MaxConcurrentRuns is the cluster-wide cap; 0 disables it, and
	// AcquireCapacity then grants without a lease.
	MaxConcurrentRuns int
	LeaseTTL          time.Duration
	// MaxHandled is passed to a new CapacityWorkflow; 0 keeps the
	// workflow default.
	MaxHandled int
}

// Capacity is the AcquireCapacity activity: RepoWorkflow reaches the
// CapacityWorkflow through it, as it reaches its InstallationWorkflow
// through AcquireBudget.
type Capacity struct {
	client    client.Client
	taskQueue string
	cfg       CapacityConfig
}

// NewCapacity returns the capacity activity, starting the
// CapacityWorkflow on taskQueue with cfg.
func NewCapacity(c client.Client, taskQueue string, cfg CapacityConfig) *Capacity {
	return &Capacity{client: c, taskQueue: taskQueue, cfg: cfg}
}

// AcquireCapacity sends acquire to capacity/runs, starting the
// CapacityWorkflow if it is not running. Without a cap it grants at once
// with no lease id, so the caller has nothing to report. The same
// UpdateID returns the first attempt's answer.
func (c *Capacity) AcquireCapacity(ctx context.Context, in *workflows.AcquireInput) (*workflows.AcquireResult, error) {
	if c.cfg.MaxConcurrentRuns <= 0 {
		return &workflows.AcquireResult{Granted: true}, nil
	}

	handle, err := c.client.UpdateWithStartWorkflow(ctx, client.UpdateWithStartWorkflowOptions{
		StartWorkflowOperation: c.client.NewWithStartWorkflowOperation(c.startOptions(), workflows.CapacityWorkflowName, c.input()),
		UpdateOptions: client.UpdateWorkflowOptions{
			UpdateID:     in.UpdateID,
			UpdateName:   workflows.AcquireUpdate,
			Args:         []any{&in.Request},
			WaitForStage: client.WorkflowUpdateStageCompleted,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("acquire on %s: %w", workflows.CapacityWorkflowID, err)
	}

	var res workflows.AcquireResult
	if err := handle.Get(ctx, &res); err != nil {
		return nil, fmt.Errorf("acquire on %s: %w", workflows.CapacityWorkflowID, err)
	}

	return &res, nil
}

// SetCap hands the configured cap to the CapacityWorkflow at worker
// start, starting it if the cap is set and it is not running. With no
// cap and no workflow there is nothing to do.
func (c *Capacity) SetCap(ctx context.Context) error {
	sig := &workflows.Capacity{MaxConcurrentRuns: c.cfg.MaxConcurrentRuns}

	if c.cfg.MaxConcurrentRuns <= 0 {
		err := c.client.SignalWorkflow(ctx, workflows.CapacityWorkflowID, "", workflows.CapacitySignal, sig)
		var notFound *serviceerror.NotFound
		if err != nil && !errors.As(err, &notFound) {
			return fmt.Errorf("lift the cap on %s: %w", workflows.CapacityWorkflowID, err)
		}

		return nil
	}

	// Signal-with-Start signals a running workflow by definition, so it
	// takes no conflict policy.
	opts := client.StartWorkflowOptions{ID: workflows.CapacityWorkflowID, TaskQueue: c.taskQueue}
	_, err := c.client.SignalWithStartWorkflow(ctx, workflows.CapacityWorkflowID, workflows.CapacitySignal, sig,
		opts, workflows.CapacityWorkflowName, c.input())
	if err != nil {
		return fmt.Errorf("set the cap on %s: %w", workflows.CapacityWorkflowID, err)
	}

	return nil
}

func (c *Capacity) startOptions() client.StartWorkflowOptions {
	return client.StartWorkflowOptions{
		ID:                       workflows.CapacityWorkflowID,
		TaskQueue:                c.taskQueue,
		WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
	}
}

func (c *Capacity) input() *workflows.CapacityWorkflowInput {
	return &workflows.CapacityWorkflowInput{
		MaxConcurrentRuns: c.cfg.MaxConcurrentRuns,
		LeaseTTL:          c.cfg.LeaseTTL,
		MaxHandled:        c.cfg.MaxHandled,
	}
}

// Register registers AcquireCapacity under its workflows package name.
func (c *Capacity) Register(r Registry) {
	r.RegisterActivityWithOptions(c.AcquireCapacity, activity.RegisterOptions{Name: workflows.AcquireCapacityActivity})
}
