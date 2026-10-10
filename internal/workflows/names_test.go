package workflows

import (
	"slices"
	"testing"

	"go.temporal.io/sdk/workflow"
)

// TestWorkflowIDs pins ADR-0002's ID forms: the platform is the second
// segment of every entity ID, and the discovery Schedule is per App.
func TestWorkflowIDs(t *testing.T) {
	t.Parallel()

	if got := RepoWorkflowID(42); got != "repo/github/42" {
		t.Errorf("RepoWorkflowID(42) = %q", got)
	}

	if got := InstallationWorkflowID(7); got != "installation/github/7" {
		t.Errorf("InstallationWorkflowID(7) = %q", got)
	}

	if got := DiscoveryScheduleID("boop-bot"); got != "discovery/boop-bot" {
		t.Errorf("DiscoveryScheduleID = %q", got)
	}

	if got := DiscoveryWorkflowID(); got != "discovery/github" {
		t.Errorf("DiscoveryWorkflowID = %q", got)
	}
}

func TestTaskPriority(t *testing.T) {
	t.Parallel()

	p := TaskPriority(PriorityHigh, 7)
	if p.PriorityKey != 1 || p.FairnessKey != "7" {
		t.Errorf("TaskPriority(high, 7) = %+v, want key 1, fairness by installation", p)
	}

	if PriorityNormal != 3 {
		t.Errorf("PriorityNormal = %d, want the server default 3", PriorityNormal)
	}
}

// recordingRegistry records registered workflow names.
type recordingRegistry struct{ names []string }

func (*recordingRegistry) RegisterWorkflow(any) {}

func (r *recordingRegistry) RegisterWorkflowWithOptions(_ any, o workflow.RegisterOptions) {
	r.names = append(r.names, o.Name)
}

func (*recordingRegistry) RegisterDynamicWorkflow(any, workflow.DynamicRegisterOptions) {}

func TestRegister(t *testing.T) {
	t.Parallel()

	var r recordingRegistry

	Register(&r)

	if !slices.Contains(r.names, InstallationWorkflowName) {
		t.Errorf("Register registered %v, want %s", r.names, InstallationWorkflowName)
	}
}

// TestMetricNames pins the metrics workflow code emits (DESIGN-0001
// § Observability).
func TestMetricNames(t *testing.T) {
	t.Parallel()
	for got, want := range map[string]string{
		MetricBudgetLimit:        "boopd_budget_limit",
		MetricBudgetRemaining:    "boopd_budget_remaining",
		MetricBudgetAdmittedRuns: "boopd_budget_admitted_runs",
		MetricRateSpendPerRun:    "boopd_rate_spend_per_run",
		MetricDiscoveryMissed:    "boopd_discovery_missed",
		MetricReposStalled:       "boopd_repos_stalled",
		MetricReposIncomplete:    "boopd_repos_incomplete",
	} {
		if got != want {
			t.Errorf("metric %q, want %q", got, want)
		}
	}
}
