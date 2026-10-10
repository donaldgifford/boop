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

func newEnv(t *testing.T, gh *fakeGitHub, apps ...*config.App) *testsuite.TestActivityEnvironment {
	t.Helper()
	if len(apps) == 0 {
		apps = []*config.App{{Name: "main", AppID: 7}}
	}
	acts := activities.New(&activities.Deps{Config: &config.Config{Apps: apps}, GitHub: gh})
	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	acts.Register(env)
	return env
}

func TestCheckRepo(t *testing.T) {
	t.Parallel()
	repo := &platform.Repository{ID: 42, Slug: "acme/new-name", DefaultBranch: "trunk"}
	tests := []struct {
		state platform.RepoState
		want  string
	}{
		{platform.RepoGone, workflows.RepoGone},
		{platform.RepoNoConfig, workflows.RepoNoConfig},
		{platform.RepoPresent, workflows.RepoPresent},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			env := newEnv(t, &fakeGitHub{installs: map[int64]*fakeInstall{1: {state: tt.state, repo: repo}}})
			val, err := env.ExecuteActivity(workflows.CheckRepoActivity, &workflows.CheckRepoInput{InstallationID: 1, RepoID: 42})
			if err != nil {
				t.Fatalf("CheckRepo: %v", err)
			}
			var got workflows.CheckRepoResult
			if err := val.Get(&got); err != nil {
				t.Fatal(err)
			}
			if got.State != tt.want || got.Slug != repo.Slug || got.DefaultBranch != "trunk" {
				t.Errorf("got %+v, want state %s with the repository's slug and branch", got, tt.want)
			}
		})
	}
}

func TestCheckRepo_UnknownIsAnError(t *testing.T) {
	t.Parallel()
	env := newEnv(t, &fakeGitHub{installs: map[int64]*fakeInstall{1: {state: platform.RepoUnknown}}})
	if _, err := env.ExecuteActivity(workflows.CheckRepoActivity, &workflows.CheckRepoInput{InstallationID: 1, RepoID: 42}); err == nil {
		t.Fatal("an unknown state must be an error, never an answer")
	}
}

func TestReadRateLimit(t *testing.T) {
	t.Parallel()
	in := &fakeInstall{readings: readings(time.Unix(1000, 0).UTC())}
	env := newEnv(t, &fakeGitHub{installs: map[int64]*fakeInstall{1: in}})
	val, err := env.ExecuteActivity(workflows.ReadRateLimitActivity, &workflows.ReadRateLimitInput{InstallationID: 1})
	if err != nil {
		t.Fatalf("ReadRateLimit: %v", err)
	}
	var got workflows.Readings
	if err := val.Get(&got); err != nil {
		t.Fatal(err)
	}
	if got.Resources["core"].Limit != 5000 || got.Resources["graphql"].Remaining != 4900 {
		t.Errorf("got %+v", got)
	}
	if in.limit != 5000 {
		t.Errorf("limiter re-tuned to %d, want the core limit 5000", in.limit)
	}
}

// With two apps, the installation's app is learned from the listings.
func TestReadRateLimit_LearnsAppFromListings(t *testing.T) {
	t.Parallel()
	gh := &fakeGitHub{
		apps: map[string]*fakeApp{
			"a": {installs: []platform.Installation{{ID: 1}}},
			"b": {installs: []platform.Installation{{ID: 2}}},
		},
		installs: map[int64]*fakeInstall{2: {readings: readings(time.Unix(1000, 0))}},
	}
	env := newEnv(t, gh, &config.App{Name: "a", AppID: 1}, &config.App{Name: "b", AppID: 2})
	if _, err := env.ExecuteActivity(workflows.ReadRateLimitActivity, &workflows.ReadRateLimitInput{InstallationID: 2}); err != nil {
		t.Fatalf("ReadRateLimit: %v", err)
	}
	if _, err := env.ExecuteActivity(workflows.ReadRateLimitActivity, &workflows.ReadRateLimitInput{InstallationID: 9}); err == nil {
		t.Fatal("an installation in no listing must be an error")
	}
}
