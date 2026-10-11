package workflows

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"testing"

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
