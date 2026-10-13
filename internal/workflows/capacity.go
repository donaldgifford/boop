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
	"time"

	"go.temporal.io/sdk/workflow"
)

// CapacityState is CapacityWorkflow's ContinueAsNew state: the cluster's
// open run leases.
type CapacityState struct {
	LeaseBook
}

// Capacity is the cap Signal's payload: runs.max_concurrent as the
// worker read it at start. 0 lifts the cap.
type Capacity struct {
	MaxConcurrentRuns int
}

// CapacityWorkflowInput is CapacityWorkflow's input and its
// ContinueAsNew state.
type CapacityWorkflowInput struct {
	// MaxConcurrentRuns caps open leases across every installation
	// (runs.max_concurrent); 0 grants every request.
	MaxConcurrentRuns int

	// LeaseTTL releases a grant that was never reported; 0 means
	// DefaultLeaseTTL.
	LeaseTTL time.Duration

	// MaxHandled overrides DefaultMaxHandled when positive.
	MaxHandled int

	State CapacityState
}

// capacity is CapacityWorkflow's state within one run.
type capacity struct {
	in      CapacityWorkflowInput
	handled int
}

// CapacityWorkflow holds the cluster-wide cap on concurrent runs
// (runs.max_concurrent, DESIGN-0001 OQ12 as amended by IMPL-0002). A
// RepoWorkflow takes a lease here after its installation's budget lease
// and before the run mints a token or creates a Job, and reports it when
// the run ends. It is the InstallationWorkflow's lease book without the
// rate resources: the same acquire Update, report Signal, TTL sweep and
// jittered retryAt. The worker starts it, or sends the cap Signal to the
// running one, at boot.
func CapacityWorkflow(ctx workflow.Context, in *CapacityWorkflowInput) error {
	c := &capacity{in: *in}
	c.in.State.init()
	if c.in.LeaseTTL <= 0 {
		c.in.LeaseTTL = DefaultLeaseTTL
	}

	if err := workflow.SetQueryHandler(ctx, StateQuery, func() (CapacityState, error) { return c.in.State, nil }); err != nil {
		return err
	}

	if err := workflow.SetUpdateHandlerWithOptions(ctx, AcquireUpdate,
		func(ctx workflow.Context, req *AcquireRequest) (*AcquireResult, error) {
			c.handled++
			res := c.acquire(workflow.Now(ctx), req)
			if !res.Granted {
				workflow.GetMetricsHandler(ctx).Counter(MetricCapacityDenied).Inc(1)
			}
			c.gauges(ctx)

			return res, nil
		},
		workflow.UpdateHandlerOptions{Validator: validateAcquire},
	); err != nil {
		return err
	}

	reports := workflow.GetSignalChannel(ctx, ReportSignal)
	caps := workflow.GetSignalChannel(ctx, CapacitySignal)

	workflow.Go(ctx, func(ctx workflow.Context) {
		for {
			var r Report
			if !reports.Receive(ctx, &r) {
				return
			}

			c.handled++
			c.in.State.close(r.LeaseID)
			c.gauges(ctx)
		}
	})

	workflow.Go(ctx, func(ctx workflow.Context) {
		for {
			var v Capacity
			if !caps.Receive(ctx, &v) {
				return
			}

			c.handled++
			c.in.MaxConcurrentRuns = v.MaxConcurrentRuns
		}
	})

	maxHandled := c.in.MaxHandled
	if maxHandled <= 0 {
		maxHandled = DefaultMaxHandled
	}

	done := func() bool {
		return c.handled >= maxHandled || workflow.GetInfo(ctx).GetContinueAsNewSuggested()
	}

	for {
		now := workflow.Now(ctx)
		wake := max(c.in.State.nextExpiry(now).Sub(now), time.Second)

		if _, err := workflow.AwaitWithTimeout(ctx, wake, done); err != nil {
			return err
		}

		c.in.State.sweep(workflow.Now(ctx))
		c.gauges(ctx)

		if done() {
			return c.continueAsNew(ctx, reports, caps)
		}
	}
}

// acquire grants a lease while fewer than the cap are open, else says
// when to ask again.
func (c *capacity) acquire(now time.Time, req *AcquireRequest) *AcquireResult {
	s := &c.in.State
	s.sweep(now)

	if s.full(c.in.MaxConcurrentRuns) {
		return &AcquireResult{RetryAt: s.capRetry(now)}
	}

	return &AcquireResult{Granted: true, LeaseID: s.grant(now, req.Holder, c.in.LeaseTTL)}
}

// continueAsNew drains in-flight Updates and pending Signals first, so
// no grant, report or cap change is lost across runs.
func (c *capacity) continueAsNew(ctx workflow.Context, reports, caps workflow.ReceiveChannel) error {
	if err := workflow.Await(ctx, func() bool { return workflow.AllHandlersFinished(ctx) }); err != nil {
		return err
	}

	for {
		var r Report
		if !reports.ReceiveAsync(&r) {
			break
		}

		c.in.State.close(r.LeaseID)
	}

	for {
		var v Capacity
		if !caps.ReceiveAsync(&v) {
			break
		}

		c.in.MaxConcurrentRuns = v.MaxConcurrentRuns
	}

	return workflow.NewContinueAsNewError(ctx, CapacityWorkflowName, &c.in)
}

// gauges publishes the open leases and the cap through the SDK's
// metrics handler, which stays silent during replay.
func (c *capacity) gauges(ctx workflow.Context) {
	m := workflow.GetMetricsHandler(ctx)
	m.Gauge(MetricCapacityLeases).Update(float64(c.in.State.open()))
	m.Gauge(MetricCapacityLimit).Update(float64(c.in.MaxConcurrentRuns))
}
