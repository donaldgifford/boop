//go:build integration

package activities_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"go.temporal.io/sdk/activity"

	"github.com/donaldgifford/boop/internal/activities"
	"github.com/donaldgifford/boop/internal/temporal"
	"github.com/donaldgifford/boop/internal/temporal/temporaltest"
	"github.com/donaldgifford/boop/internal/workflows"
)

// TestAcquireBudget_DevServer runs the budget entity end to end on the
// dev server: a versioned worker, promoted, serves Update-with-Start
// from the activity; the entity reads /rate_limit through the
// registered activity; the same UpdateID is idempotent; the cap holds;
// a report frees the slot.
func TestAcquireBudget_DevServer(t *testing.T) {
	t.Parallel()

	srv := temporaltest.Start(t)
	c := srv.Client
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	cfg := activities.DefaultBudgetConfig()
	cfg.MaxConcurrentRuns = 1
	budget := activities.NewBudget(c, srv.Config.TaskQueue, cfg)

	w := temporal.NewWorker(c, &temporal.WorkerConfig{TaskQueue: srv.Config.TaskQueue, ActivityConcurrency: 2, BuildID: "0.1.0"})
	workflows.Register(w)
	budget.Register(w)
	w.RegisterActivityWithOptions(func(context.Context, *workflows.ReadRateLimitInput) (*workflows.Readings, error) {
		now := time.Now()

		return &workflows.Readings{
			Resources: map[string]workflows.Reading{
				workflows.ResourceCore:    {Limit: 5000, Remaining: 4000, Reset: now.Add(time.Hour)},
				workflows.ResourceGraphQL: {Limit: 5000, Remaining: 4500, Reset: now.Add(time.Hour)},
			},
			ObservedAt: now,
		}, nil
	}, activity.RegisterOptions{Name: workflows.ReadRateLimitActivity})

	if err := w.Start(); err != nil {
		t.Fatalf("start worker: %v", err)
	}
	t.Cleanup(w.Stop)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	if err := temporal.PromoteBuild(ctx, c, temporal.DefaultDeployment, "0.1.0", quiet); err != nil {
		t.Fatalf("PromoteBuild: %v", err)
	}

	acquire := func(updateID, holder string) *workflows.AcquireResult {
		t.Helper()

		res, err := budget.AcquireBudget(ctx, &workflows.AcquireInput{
			InstallationID: 7, UpdateID: updateID,
			Request: workflows.AcquireRequest{Holder: holder, Priority: workflows.PriorityNormal},
		})
		if err != nil {
			t.Fatalf("AcquireBudget(%s): %v", updateID, err)
		}

		return res
	}

	first := acquire("u1", workflows.RepoWorkflowID(42))
	if !first.Granted || first.LeaseID == "" {
		t.Fatalf("first acquire = %+v, want a grant", first)
	}

	if again := acquire("u1", workflows.RepoWorkflowID(42)); again.LeaseID != first.LeaseID {
		t.Errorf("retried acquire with the same UpdateID = %+v, want the first answer %+v", again, first)
	}

	if second := acquire("u2", workflows.RepoWorkflowID(43)); second.Granted || second.RetryAt.Before(time.Now()) {
		t.Errorf("second acquire at a cap of 1 = %+v, want a wait", second)
	}

	id := workflows.InstallationWorkflowID(7)

	state := func() workflows.BudgetState {
		t.Helper()

		v, err := c.QueryWorkflow(ctx, id, "", workflows.StateQuery)
		if err != nil {
			t.Fatalf("query: %v", err)
		}

		var s workflows.BudgetState
		if err := v.Get(&s); err != nil {
			t.Fatalf("decode: %v", err)
		}

		return s
	}

	if s := state(); s.Resources[workflows.ResourceCore].Limit != 5000 || s.Resources[workflows.ResourceCore].Remaining != 4000 {
		t.Errorf("state = %+v, want the readings from the ReadRateLimit activity", s)
	}

	if err := c.SignalWorkflow(ctx, id, "", workflows.ReportSignal, &workflows.Report{LeaseID: first.LeaseID}); err != nil {
		t.Fatalf("report: %v", err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for len(state().Leases) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the report did not close the lease")
		}

		time.Sleep(200 * time.Millisecond)
	}

	if third := acquire("u3", workflows.RepoWorkflowID(43)); !third.Granted {
		t.Errorf("acquire after the report = %+v, want a grant: the slot is free", third)
	}
}
