package workflows

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// TestDiscoveryWorkflow: every installation's budget is told its
// suspended flag, active installations are discovered in parallel,
// suspended ones are not, and one failed installation is counted
// without failing the pass.
func TestDiscoveryWorkflow(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	suite.SetLogger(log.NewStructuredLogger(slog.New(slog.DiscardHandler)))
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflowWithOptions(DiscoveryWorkflow, workflow.RegisterOptions{Name: DiscoveryWorkflowName})

	env.RegisterActivityWithOptions(func(_ context.Context, in *ListInstallationsInput) ([]InstallationInfo, error) {
		if in.App != "main" {
			return nil, errors.New("wrong app")
		}
		return []InstallationInfo{{ID: 1}, {ID: 2, Suspended: true}, {ID: 3}, {ID: 4}}, nil
	}, activity.RegisterOptions{Name: ListInstallationsActivity})

	var (
		mu         sync.Mutex
		discovered []int64
	)
	env.RegisterActivityWithOptions(func(_ context.Context, in *DiscoverInput) (*DiscoverSummary, error) {
		mu.Lock()
		discovered = append(discovered, in.InstallationID)
		mu.Unlock()
		if in.InstallationID == 4 {
			return nil, temporal.NewNonRetryableApplicationError("bad installation", "test", nil)
		}
		return &DiscoverSummary{InstallationID: in.InstallationID, Pages: 1, Seen: 10, Onboarded: 3, ProbeCost: 1}, nil
	}, activity.RegisterOptions{Name: DiscoverInstallationActivity})

	suspends := map[string]bool{}
	env.OnSignalExternalWorkflow(mock.Anything, mock.Anything, "", SuspendSignal, mock.Anything).Return(
		func(_, id, _, _ string, arg any) error {
			mu.Lock()
			defer mu.Unlock()
			suspends[id] = arg.(Suspend).Suspended
			if id == InstallationWorkflowID(3) {
				return errors.New("workflow not found")
			}
			return nil
		})

	env.ExecuteWorkflow(DiscoveryWorkflowName, &DiscoveryInput{App: "main"})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow: %v", err)
	}
	var got DiscoveryResult
	if err := env.GetWorkflowResult(&got); err != nil {
		t.Fatal(err)
	}
	want := DiscoveryResult{App: "main", Installations: 4, Suspended: 1, Failed: 1, Seen: 20, Onboarded: 6, ProbeCost: 2}
	if got != want {
		t.Errorf("result = %+v, want %+v", got, want)
	}
	slices.Sort(discovered)
	if !slices.Equal(discovered, []int64{1, 3, 4}) {
		t.Errorf("discovered %v, want the three active installations", discovered)
	}
	wantSuspends := map[string]bool{
		InstallationWorkflowID(1): false, InstallationWorkflowID(2): true,
		InstallationWorkflowID(3): false, InstallationWorkflowID(4): false,
	}
	for id, s := range wantSuspends {
		if v, ok := suspends[id]; !ok || v != s {
			t.Errorf("suspend signal to %s = %v (sent %v), want %v", id, v, ok, s)
		}
	}
}

// A github-rate-limited error's NextRetryDelay, not the policy's 30s
// initial interval, spaces DiscoverInstallation's retry (#16).
func TestDiscoveryWorkflow_RetryAfter(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	suite.SetLogger(log.NewStructuredLogger(slog.New(slog.DiscardHandler)))
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflowWithOptions(DiscoveryWorkflow, workflow.RegisterOptions{Name: DiscoveryWorkflowName})
	env.RegisterActivityWithOptions(func(context.Context, *ListInstallationsInput) ([]InstallationInfo, error) {
		return []InstallationInfo{{ID: 1}}, nil
	}, activity.RegisterOptions{Name: ListInstallationsActivity})

	const wait = 7 * time.Second
	var starts []time.Time
	env.RegisterActivityWithOptions(func(context.Context, *DiscoverInput) (*DiscoverSummary, error) {
		starts = append(starts, env.Now())
		if len(starts) == 1 {
			return nil, temporal.NewApplicationErrorWithOptions("github rate limit", ErrTypeGitHubRateLimited,
				temporal.ApplicationErrorOptions{NextRetryDelay: wait})
		}
		return &DiscoverSummary{InstallationID: 1, Seen: 1}, nil
	}, activity.RegisterOptions{Name: DiscoverInstallationActivity})
	env.OnSignalExternalWorkflow(mock.Anything, mock.Anything, "", SuspendSignal, mock.Anything).Return(nil)

	env.ExecuteWorkflow(DiscoveryWorkflowName, &DiscoveryInput{App: "main"})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow: %v", err)
	}
	if len(starts) != 2 {
		t.Fatalf("DiscoverInstallation ran %d times, want 2", len(starts))
	}
	if gap := starts[1].Sub(starts[0]); gap < wait || gap >= 30*time.Second {
		t.Errorf("retry %v after the first attempt, want %v, not the 30s backoff", gap, wait)
	}
}

// A targeted pass discovers and signals only the installations asked
// for; one outside the allowlisted listing is skipped.
func TestDiscoveryWorkflow_Targeted(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	suite.SetLogger(log.NewStructuredLogger(slog.New(slog.DiscardHandler)))
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflowWithOptions(DiscoveryWorkflow, workflow.RegisterOptions{Name: DiscoveryWorkflowName})
	// The activity's listing is already filtered by the allowlist: 9 is
	// not in it.
	env.RegisterActivityWithOptions(func(context.Context, *ListInstallationsInput) ([]InstallationInfo, error) {
		return []InstallationInfo{{ID: 1}, {ID: 2}, {ID: 3}}, nil
	}, activity.RegisterOptions{Name: ListInstallationsActivity})
	var (
		mu         sync.Mutex
		discovered []int64
		suspends   []string
	)
	env.RegisterActivityWithOptions(func(_ context.Context, in *DiscoverInput) (*DiscoverSummary, error) {
		mu.Lock()
		defer mu.Unlock()
		discovered = append(discovered, in.InstallationID)
		return &DiscoverSummary{InstallationID: in.InstallationID, Seen: 1}, nil
	}, activity.RegisterOptions{Name: DiscoverInstallationActivity})
	env.OnSignalExternalWorkflow(mock.Anything, mock.Anything, "", SuspendSignal, mock.Anything).Return(
		func(_, id, _, _ string, _ any) error {
			mu.Lock()
			defer mu.Unlock()
			suspends = append(suspends, id)
			return nil
		})

	env.ExecuteWorkflow(DiscoveryWorkflowName, &DiscoveryInput{App: "main", Installations: []int64{2, 9}})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow: %v", err)
	}
	var got DiscoveryResult
	if err := env.GetWorkflowResult(&got); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(discovered, []int64{2}) || !slices.Equal(suspends, []string{InstallationWorkflowID(2)}) {
		t.Errorf("discovered %v, suspend signals %v; want installation 2 only", discovered, suspends)
	}
	if got.Installations != 1 || got.Seen != 1 {
		t.Errorf("result = %+v, want one installation", got)
	}
}

func TestTargetedDiscoveryID(t *testing.T) {
	t.Parallel()
	if got := TargetedDiscoveryID("boop-bot", 42); got != "discovery/boop-bot/installation/42" {
		t.Errorf("TargetedDiscoveryID = %q", got)
	}
}

// With Record, the pass reconciles each discovered installation's PRs
// and ends by recording each listed installation and discovery.finished
// with its totals; without it, nothing is reconciled or recorded.
func TestDiscoveryWorkflow_RecordsThePass(t *testing.T) {
	t.Parallel()
	for _, record := range []bool{true, false} {
		var suite testsuite.WorkflowTestSuite
		suite.SetLogger(log.NewStructuredLogger(slog.New(slog.DiscardHandler)))
		env := suite.NewTestWorkflowEnvironment()
		env.RegisterWorkflowWithOptions(DiscoveryWorkflow, workflow.RegisterOptions{Name: DiscoveryWorkflowName})
		env.RegisterActivityWithOptions(func(context.Context, *ListInstallationsInput) ([]InstallationInfo, error) {
			return []InstallationInfo{
				{ID: 1, Account: "acme"}, {ID: 2, Account: "beta"}, {ID: 3, Account: "gone"}, {ID: 4, Account: "paused", Suspended: true},
			}, nil
		}, activity.RegisterOptions{Name: ListInstallationsActivity})
		env.RegisterActivityWithOptions(func(_ context.Context, in *DiscoverInput) (*DiscoverSummary, error) {
			if in.InstallationID == 3 {
				return nil, temporal.NewNonRetryableApplicationError("gone", "test", nil)
			}
			return &DiscoverSummary{InstallationID: in.InstallationID, Seen: 5, Onboarded: 1, ProbeCost: 2}, nil
		}, activity.RegisterOptions{Name: DiscoverInstallationActivity})
		var reconciled []ReconcilePRsInput
		env.RegisterActivityWithOptions(func(_ context.Context, in *ReconcilePRsInput) (*ReconcilePRsResult, error) {
			reconciled = append(reconciled, *in)
			return &ReconcilePRsResult{Open: 2, Synced: 2}, nil
		}, activity.RegisterOptions{Name: ReconcilePullRequestsActivity})
		var recorded []RecordedEvent
		env.RegisterActivityWithOptions(func(_ context.Context, in *RecordEventsInput) error {
			recorded = append(recorded, in.Events...)
			return nil
		}, activity.RegisterOptions{Name: RecordEventsActivity})
		env.OnSignalExternalWorkflow(mock.Anything, mock.Anything, "", SuspendSignal, mock.Anything).Return(nil)

		env.ExecuteWorkflow(DiscoveryWorkflowName, &DiscoveryInput{App: "main", Record: record})
		if err := env.GetWorkflowError(); err != nil {
			t.Fatalf("workflow: %v", err)
		}
		var res DiscoveryResult
		if err := env.GetWorkflowResult(&res); err != nil {
			t.Fatal(err)
		}
		if !record {
			if len(recorded) != 0 || len(reconciled) != 0 {
				t.Errorf("recorded %+v and reconciled %+v without Record, want nothing", recorded, reconciled)
			}
			continue
		}
		slices.SortFunc(reconciled, func(x, y ReconcilePRsInput) int { return cmp.Compare(x.InstallationID, y.InstallationID) })
		want := []ReconcilePRsInput{{App: "main", InstallationID: 1, Account: "acme"}, {App: "main", InstallationID: 2, Account: "beta"}}
		if !slices.Equal(reconciled, want) || res.PRsSynced != 4 || res.ReconcileFailed != 0 {
			t.Errorf("reconciled %+v (synced %d), want %+v: the failed installation skipped", reconciled, res.PRsSynced, want)
		}
		if len(recorded) != 5 || recorded[4].Kind != KindDiscoveryFinished || recorded[4].Discovery == nil {
			t.Fatalf("recorded = %+v, want four installations and discovery.finished", recorded)
		}
		for i, want := range []struct {
			kind    string
			account string
		}{
			{KindInstallationAdded, "acme"},
			{KindInstallationAdded, "beta"},
			{KindInstallationAdded, "gone"},
			{KindInstallationSuspended, "paused"},
		} {
			e := recorded[i]
			if e.Kind != want.kind || e.InstallationID != int64(i+1) || e.Installation == nil ||
				e.Installation.App != "main" || e.Installation.Account != want.account {
				t.Errorf("recorded[%d] = %+v, want %s for %s", i, e, want.kind, want.account)
			}
		}
		d := recorded[4].Discovery
		if d.App != "main" || d.Seen != 10 || d.Onboarded != 2 || d.ProbeCost != 4 || d.Failed != 1 || d.StartedAt.After(d.FinishedAt) {
			t.Errorf("discovery.finished = %+v", d)
		}
	}
}
