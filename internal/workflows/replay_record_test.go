//go:build integration

package workflows_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	sdkworker "go.temporal.io/sdk/worker"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/donaldgifford/boop/internal/temporal"
	"github.com/donaldgifford/boop/internal/temporal/temporaltest"
	"github.com/donaldgifford/boop/internal/workflows"
)

// TestRecordReplayHistories writes the replay baselines in
// testdata/replay from the current workflow code against a dev server.
// Re-record only when the histories should move to a new baseline:
//
//	BOOPD_RECORD_HISTORIES=1 go test -tags integration -run TestRecordReplayHistories ./internal/workflows/
func TestRecordReplayHistories(t *testing.T) {
	if os.Getenv("BOOPD_RECORD_HISTORIES") != "1" {
		t.Skip("set BOOPD_RECORD_HISTORIES=1 to record the replay histories")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	srv := temporaltest.Start(t)
	if err := temporal.EnsureSearchAttributes(ctx, srv.Client, srv.Config.Namespace, workflows.SearchAttributes); err != nil {
		t.Fatal(err)
	}
	checks := 0
	w := sdkworker.New(srv.Client, srv.Config.TaskQueue, sdkworker.Options{})
	workflows.Register(w)
	register := func(name string, fn any) {
		w.RegisterActivityWithOptions(fn, activity.RegisterOptions{Name: name})
	}
	register(workflows.PlanRunActivity, func(context.Context, *workflows.PlanRunInput) (*workflows.RunPlan, error) {
		return &workflows.RunPlan{Profile: "default", Cadence: time.Hour}, nil
	})
	register(workflows.AcquireBudgetActivity, func(_ context.Context, in *workflows.AcquireInput) (*workflows.AcquireResult, error) {
		return &workflows.AcquireResult{Granted: true, LeaseID: in.UpdateID}, nil
	})
	register(workflows.AcquireCapacityActivity, func(_ context.Context, in *workflows.AcquireInput) (*workflows.AcquireResult, error) {
		return &workflows.AcquireResult{Granted: true, LeaseID: "capacity/" + in.UpdateID}, nil
	})
	register(workflows.RunRenovateActivity, func(context.Context, *workflows.RunInput) (*workflows.RunResult, error) {
		return &workflows.RunResult{Outcome: workflows.OutcomeSucceeded, RepositoryResult: "done"}, nil
	})
	register(workflows.CheckRepoActivity, func(context.Context, *workflows.CheckRepoInput) (*workflows.CheckRepoResult, error) {
		checks++
		if checks == 1 {
			return &workflows.CheckRepoResult{State: workflows.RepoPresent, Slug: "boop-e2e/absent", DefaultBranch: "main"}, nil
		}
		return &workflows.CheckRepoResult{State: workflows.RepoGone}, nil
	})
	register(workflows.ListInstallationsActivity, func(context.Context, *workflows.ListInstallationsInput) ([]workflows.InstallationInfo, error) {
		return []workflows.InstallationInfo{{ID: 1, Account: "one"}, {ID: 2, Account: "two"}}, nil
	})
	register(workflows.DiscoverInstallationActivity, func(_ context.Context, in *workflows.DiscoverInput) (*workflows.DiscoverSummary, error) {
		return &workflows.DiscoverSummary{InstallationID: in.InstallationID, Pages: 1, Seen: 3, Onboarded: 1, ProbeCost: 1}, nil
	})
	if err := w.Start(); err != nil {
		t.Fatal(err)
	}
	defer w.Stop()

	start := func(id, name string, arg any) client.WorkflowRun {
		t.Helper()
		run, err := srv.Client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: id, TaskQueue: srv.Config.TaskQueue}, name, arg)
		if err != nil {
			t.Fatalf("start %s: %v", id, err)
		}
		return run
	}
	now := time.Now()

	// Discovered, ran once, rechecked, ran again, continued as new: two
	// iterations short of MaxIterations, so the second run continues.
	run := start(workflows.RepoWorkflowID(101), workflows.RepoWorkflowName, &workflows.RepoState{
		RepoID: 101, Slug: "boop-e2e/runs", DefaultBranch: "main", InstallationID: 1,
		DiscoveryInterval: time.Hour, LastSeen: now, NextDue: now, Iterations: workflows.MaxIterations - 2,
	})
	waitState(ctx, t, srv.Client, 101, func(s *workflows.RepoState) bool { return s.LastRun != nil })
	signal(ctx, t, srv.Client, 101, workflows.DiscoveredSignal, workflows.Discovered{
		Slug: "boop-e2e/runs", DefaultBranch: "main", InstallationID: 1, DiscoveryInterval: time.Hour, Extends: []string{"config:recommended"},
	})
	signal(ctx, t, srv.Client, 101, workflows.RecheckSignal, nil)
	waitClosed(ctx, t, srv.Client, run, enumspb.WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW)
	write(ctx, t, srv.Client, run, "repo-discovered-run-recheck-continue.json")
	if err := srv.Client.TerminateWorkflow(ctx, workflows.RepoWorkflowID(101), "", "recorded"); err != nil {
		t.Fatal(err)
	}

	// Present, then gone: the absence check runs CheckRepo twice and
	// offboards.
	run = start(workflows.RepoWorkflowID(102), workflows.RepoWorkflowName, &workflows.RepoState{
		RepoID: 102, Slug: "boop-e2e/absent", DefaultBranch: "main", InstallationID: 1,
		DiscoveryInterval: time.Second, LastSeen: now, NextDue: now.Add(24 * time.Hour),
	})
	if err := run.Get(ctx, nil); err != nil {
		t.Fatalf("absence RepoWorkflow: %v", err)
	}
	write(ctx, t, srv.Client, run, "repo-absence-present-then-gone.json")

	// A discovery pass over two installations.
	run = start(workflows.DiscoveryScheduleID("main"), workflows.DiscoveryWorkflowName, &workflows.DiscoveryInput{App: "main"})
	if err := run.Get(ctx, nil); err != nil {
		t.Fatalf("DiscoveryWorkflow: %v", err)
	}
	write(ctx, t, srv.Client, run, "discovery-two-installations.json")
}

func signal(ctx context.Context, t *testing.T, c client.Client, repoID int64, name string, arg any) {
	t.Helper()
	if err := c.SignalWorkflow(ctx, workflows.RepoWorkflowID(repoID), "", name, arg); err != nil {
		t.Fatalf("signal %s: %v", name, err)
	}
}

func waitState(ctx context.Context, t *testing.T, c client.Client, repoID int64, cond func(*workflows.RepoState) bool) {
	t.Helper()
	for ctx.Err() == nil {
		if v, err := c.QueryWorkflow(ctx, workflows.RepoWorkflowID(repoID), "", workflows.StateQuery); err == nil {
			var s workflows.RepoState
			if v.Get(&s) == nil && cond(&s) {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("RepoWorkflow %d never reached the state", repoID)
}

// waitClosed waits for run's own execution, not its continuation, to
// close with want.
func waitClosed(ctx context.Context, t *testing.T, c client.Client, run client.WorkflowRun, want enumspb.WorkflowExecutionStatus) {
	t.Helper()
	for ctx.Err() == nil {
		d, err := c.DescribeWorkflowExecution(ctx, run.GetID(), run.GetRunID())
		if err == nil && d.GetWorkflowExecutionInfo().GetStatus() != enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING {
			if got := d.GetWorkflowExecutionInfo().GetStatus(); got != want {
				t.Fatalf("%s closed %v, want %v", run.GetID(), got, want)
			}
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("%s never closed", run.GetID())
}

// write saves run's first execution's history as JSON.
func write(ctx context.Context, t *testing.T, c client.Client, run client.WorkflowRun, name string) {
	t.Helper()
	var h historypb.History
	it := c.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false, 0)
	for it.HasNext() {
		e, err := it.Next()
		if err != nil {
			t.Fatalf("history of %s: %v", run.GetID(), err)
		}
		h.Events = append(h.Events, e)
	}
	b, err := protojson.MarshalOptions{Indent: "  "}.Marshal(&h)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("testdata", "replay", name), append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}
