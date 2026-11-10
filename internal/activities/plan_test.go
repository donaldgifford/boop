package activities_test

import (
	"net/http"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"go.temporal.io/sdk/testsuite"

	"github.com/donaldgifford/boop/internal/activities"
	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/workflows"
	"github.com/donaldgifford/boop/test/fakegithub"
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

// TestPlanRun_FloorDetection lists the tree through fakegithub
// (IMPL-0006 tasks 7.3 to 7.5): a floor file raises the plan, none
// leaves it, a truncated listing raises it, and a failed listing
// resolves from the known managers. Each plan costs one tree read.
func TestPlanRun_FloorDetection(t *testing.T) {
	t.Parallel()
	const other = "local>acme/x"
	tests := []struct {
		name       string
		files      []string
		truncated  bool
		status     int
		managers   []string
		mode       string
		want       string
		sandboxed  bool
		raised     bool
		wantFloor  []string
		wantErrors int
	}{
		{
			name: "pyproject.toml", files: []string{"pyproject.toml"}, managers: []string{"npm"}, mode: config.FloorEnforce,
			want: "python", sandboxed: true, raised: true, wantFloor: []string{"pep621", "poetry"},
		},
		{name: "no floor file", files: []string{"package.json"}, managers: []string{"npm"}, mode: config.FloorEnforce, want: "baseline"},
		{
			name: "truncated", files: []string{"package.json"}, truncated: true, managers: []string{"npm"}, mode: config.FloorEnforce,
			want: "python", sandboxed: true, raised: true,
		},
		{
			name: "listing fails", status: http.StatusBadGateway, managers: []string{"npm"}, mode: config.FloorEnforce,
			want: "baseline", wantErrors: 1,
		},
		{
			name: "report mode keeps the ordinary profile", files: []string{"Gemfile"}, managers: []string{"npm"}, mode: config.FloorReport,
			want: "baseline", raised: true, wantFloor: []string{"bundler"},
		},
		{
			name: "first run, managers unknown", files: []string{"package.json"}, mode: config.FloorEnforce,
			want: "python", sandboxed: true, raised: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gh := fakegithub.New(t)
			gh.SetRepos(fakegithub.Repo{
				ID: 9, NodeID: "R_9", Slug: "acme/app", DefaultBranch: "main",
				HasConfig: true, Files: tt.files, TreeTruncated: tt.truncated,
			})
			gh.SetTreeStatus(tt.status)
			metrics := &floorMetrics{}
			cfg := &config.Config{
				Profiles: map[string]*config.Profile{
					"baseline": {Name: "baseline"}, "python": {Name: "python", Sandboxed: true},
				},
				Order:          []string{"baseline", "python"},
				DefaultProfile: "baseline",
				UnknownProfile: "baseline",
				Sandbox:        &config.Sandbox{RuntimeClass: "kata", FloorMode: tt.mode},
				Apps: []*config.App{{
					Name: "main", AppID: 1, Cadence: time.Hour,
					Endpoint: gh.BaseURL(), PrivateKey: config.NewSecret(gh.PEM),
				}},
			}
			env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
			activities.New(&activities.Deps{Config: cfg, Metrics: metrics}).Register(env)
			val, err := env.ExecuteActivity(workflows.PlanRunActivity, &workflows.PlanRunInput{
				InstallationID: 1, Extends: []string{other}, Managers: tt.managers, RepoID: 9, DefaultBranch: "main",
			})
			if err != nil {
				t.Fatalf("PlanRun: %v", err)
			}
			var got workflows.RunPlan
			if err := val.Get(&got); err != nil {
				t.Fatal(err)
			}
			if got.Profile != tt.want || got.Sandboxed != tt.sandboxed || got.FloorRaised != tt.raised ||
				!slices.Equal(got.FloorManagers, tt.wantFloor) {
				t.Errorf("plan = %+v; want %s sandboxed=%v raised=%v floor=%v", got, tt.want, tt.sandboxed, tt.raised, tt.wantFloor)
			}
			if n := gh.TreeReads(); n != 1 {
				t.Errorf("tree reads = %d, want exactly 1", n)
			}
			if n := int(metrics.floorErrors.Load()); n != tt.wantErrors {
				t.Errorf("floor detection errors = %d, want %d", n, tt.wantErrors)
			}
		})
	}
}

// Without a floor (no sandbox block, no sandboxed profile) PlanRun
// lists nothing.
func TestPlanRun_NoFloorNoListing(t *testing.T) {
	t.Parallel()
	gh := fakegithub.New(t)
	gh.SetRepos(fakegithub.Repo{ID: 9, Slug: "acme/app", DefaultBranch: "main", Files: []string{"Gemfile"}})
	cfg := &config.Config{
		Profiles:       map[string]*config.Profile{"baseline": {Name: "baseline"}},
		Order:          []string{"baseline"},
		DefaultProfile: "baseline",
		UnknownProfile: "baseline",
		Apps:           []*config.App{{Name: "main", AppID: 1, Endpoint: gh.BaseURL(), PrivateKey: config.NewSecret(gh.PEM)}},
	}
	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	activities.New(&activities.Deps{Config: cfg}).Register(env)
	if _, err := env.ExecuteActivity(workflows.PlanRunActivity, &workflows.PlanRunInput{
		InstallationID: 1, RepoID: 9, DefaultBranch: "main",
	}); err != nil {
		t.Fatal(err)
	}
	if n := gh.TreeReads(); n != 0 {
		t.Errorf("tree reads = %d without a floor, want 0", n)
	}
}

type floorMetrics struct {
	activities.NopMetrics
	floorErrors atomic.Int32
}

func (m *floorMetrics) FloorDetectionError() { m.floorErrors.Add(1) }
