package activities_test

import (
	"testing"
	"time"

	"go.temporal.io/sdk/testsuite"

	"github.com/donaldgifford/boop/internal/activities"
	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/platform"
	"github.com/donaldgifford/boop/internal/workflows"
)

func TestListInstallations(t *testing.T) {
	t.Parallel()

	installs := []platform.Installation{
		{ID: 1, Account: "acme"},
		{ID: 2, Account: "globex", SuspendedAt: time.Unix(1, 0)},
		{ID: 3, Account: "initech"},
	}
	tests := []struct {
		name  string
		allow []int64
		want  []workflows.InstallationInfo
	}{
		{
			name: "every installation without an allowlist",
			want: []workflows.InstallationInfo{
				{ID: 1, Account: "acme"},
				{ID: 2, Account: "globex", Suspended: true},
				{ID: 3, Account: "initech"},
			},
		},
		{
			name:  "allowlist filters",
			allow: []int64{2, 3},
			want: []workflows.InstallationInfo{
				{ID: 2, Account: "globex", Suspended: true},
				{ID: 3, Account: "initech"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := &config.Config{Apps: []*config.App{{Name: "main", AppID: 7, Installations: tt.allow}}}
			gh := &fakeGitHub{apps: map[string]*fakeApp{"main": {installs: installs}}}
			acts := activities.New(&activities.Deps{Config: cfg, GitHub: gh})

			env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
			acts.Register(env)
			val, err := env.ExecuteActivity(workflows.ListInstallationsActivity, &workflows.ListInstallationsInput{App: "main"})
			if err != nil {
				t.Fatalf("ListInstallations: %v", err)
			}
			var got []workflows.InstallationInfo
			if err := val.Get(&got); err != nil {
				t.Fatalf("Get: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestListInstallations_UnknownApp(t *testing.T) {
	t.Parallel()
	acts := activities.New(&activities.Deps{Config: &config.Config{}, GitHub: &fakeGitHub{}})
	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	acts.Register(env)
	if _, err := env.ExecuteActivity(workflows.ListInstallationsActivity, &workflows.ListInstallationsInput{App: "nope"}); err == nil {
		t.Fatal("want an error for an app missing from config")
	}
}
