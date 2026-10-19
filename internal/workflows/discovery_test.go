package workflows

import (
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
