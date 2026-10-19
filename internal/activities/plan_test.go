package activities_test

import (
	"testing"
	"time"

	"go.temporal.io/sdk/testsuite"

	"github.com/donaldgifford/boop/internal/activities"
	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/workflows"
)

func TestPlanRun(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Profiles: map[string]*config.Profile{
			"baseline": {Name: "baseline"}, "python": {Name: "python"}, "strict": {Name: "strict"},
		},
		Order:          []string{"baseline", "python", "strict"},
		DefaultProfile: "baseline",
		UnknownProfile: "strict",
		ProfileRules:   []config.ProfileRule{{Managers: []string{"pip_requirements"}, Profile: "python"}},
		Ingest:         config.Ingest{Settle: 30 * time.Second, MinInterval: 5 * time.Minute},
		Apps:           []*config.App{{Name: "main", AppID: 7, Cadence: 12 * time.Hour}},
	}
	tests := []struct {
		name string
		in   workflows.PlanRunInput
		want string
	}{
		{"unknown extends is strict", workflows.PlanRunInput{InstallationID: 1}, "strict"},
		{"known extends, no rule", workflows.PlanRunInput{InstallationID: 1, Extends: []string{"local>acme/x"}}, "baseline"},
		{
			"a manager tightens",
			workflows.PlanRunInput{InstallationID: 1, Extends: []string{"local>acme/x"}, Managers: []string{"pip_requirements"}},
			"python",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
			activities.New(&activities.Deps{Config: cfg, GitHub: &fakeGitHub{}}).Register(env)
			val, err := env.ExecuteActivity(workflows.PlanRunActivity, &tt.in)
			if err != nil {
				t.Fatalf("PlanRun: %v", err)
			}
			var got workflows.RunPlan
			if err := val.Get(&got); err != nil {
				t.Fatal(err)
			}
			if got.Profile != tt.want || got.Cadence != 12*time.Hour {
				t.Errorf("plan = %+v, want %s every 12h", got, tt.want)
			}
			if got.Settle != 30*time.Second || got.MinInterval != 5*time.Minute {
				t.Errorf("plan = %+v, want the ingest settle 30s and min_interval 5m", got)
			}
		})
	}
}
