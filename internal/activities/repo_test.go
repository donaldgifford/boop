package activities_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"go.temporal.io/sdk/testsuite"

	"github.com/donaldgifford/boop/internal/activities"
	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/platform"
	"github.com/donaldgifford/boop/internal/platform/github"
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

func checkRepo(t *testing.T, env *testsuite.TestActivityEnvironment, installationID int64) (*workflows.CheckRepoResult, error) {
	t.Helper()
	val, err := env.ExecuteActivity(workflows.CheckRepoActivity, &workflows.CheckRepoInput{InstallationID: installationID, RepoID: 42})
	if err != nil {
		return nil, err
	}
	var got workflows.CheckRepoResult
	if err := val.Get(&got); err != nil {
		t.Fatal(err)
	}
	return &got, nil
}

func TestCheckRepo_ReturnsExtends(t *testing.T) {
	t.Parallel()
	in := &fakeInstall{
		state: platform.RepoPresent, repo: &platform.Repository{ID: 42, Slug: "acme/app", DefaultBranch: "main"},
		config: github.ConfigProbe{Exists: true, Path: "renovate.json5", Extends: []string{"config:recommended"}},
	}
	got, err := checkRepo(t, newEnv(t, &fakeGitHub{installs: map[int64]*fakeInstall{1: in}}), 1)
	if err != nil {
		t.Fatalf("CheckRepo: %v", err)
	}
	if got.State != workflows.RepoPresent || !slices.Equal(got.Extends, []string{"config:recommended"}) {
		t.Errorf("got %+v, want present with the probe's extends", got)
	}
}

// A mint 404 means the installation was deleted: gone. Any other mint
// error stays an activity error, so an outage never offboards.
func TestCheckRepo_MintErrors(t *testing.T) {
	t.Parallel()
	gone := &fakeInstall{checkErr: fmt.Errorf("github: mint installation token: %w", platform.ErrInstallationGone)}
	got, err := checkRepo(t, newEnv(t, &fakeGitHub{installs: map[int64]*fakeInstall{1: gone}}), 1)
	if err != nil || got.State != workflows.RepoGone {
		t.Errorf("mint 404: got %+v, err %v; want gone", got, err)
	}
	flaky := &fakeInstall{checkErr: fmt.Errorf("github: mint installation token: %w", platform.ErrTransient)}
	if got, err := checkRepo(t, newEnv(t, &fakeGitHub{installs: map[int64]*fakeInstall{1: flaky}}), 1); err == nil {
		t.Errorf("transient mint error: got %+v, want an error", got)
	}
}

// A 401 on the cached client's token is a deleted installation's revoked
// token: CheckRepo forgets the client and asks again with a fresh one,
// whose mint answers 404.
func TestCheckRepo_RevokedTokenRemints(t *testing.T) {
	t.Parallel()
	revoked := &fakeInstall{checkErr: fmt.Errorf("github: get repository: %w", platform.ErrUnauthorized)}
	gone := &fakeInstall{checkErr: fmt.Errorf("github: mint installation token: %w", platform.ErrInstallationGone)}
	gh := &fakeGitHub{installs: map[int64]*fakeInstall{1: revoked}, fresh: map[int64]*fakeInstall{1: gone}}
	got, err := checkRepo(t, newEnv(t, gh), 1)
	if err != nil || got.State != workflows.RepoGone {
		t.Errorf("revoked token: got %+v, err %v; want gone", got, err)
	}
	if len(gh.forgot) != 1 || gh.forgot[0] != 1 {
		t.Errorf("forgot %v, want [1]", gh.forgot)
	}

	// A fresh client that still gets 401 is an error, not gone.
	gh = &fakeGitHub{installs: map[int64]*fakeInstall{1: revoked}, fresh: map[int64]*fakeInstall{1: revoked}}
	if got, err := checkRepo(t, newEnv(t, gh), 1); err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("401 after a fresh mint: got %+v, err %v; want ErrUnauthorized", got, err)
	}
}

// With several Apps, an installation absent from every fresh listing is
// gone; a listing that fails is an error.
func TestCheckRepo_AbsentFromEveryListing(t *testing.T) {
	t.Parallel()
	apps := []*config.App{{Name: "a", AppID: 1}, {Name: "b", AppID: 2}}
	gh := &fakeGitHub{
		apps: map[string]*fakeApp{
			"a": {installs: []platform.Installation{{ID: 1}}},
			"b": {installs: []platform.Installation{{ID: 2}}},
		},
		installs: map[int64]*fakeInstall{3: {state: platform.RepoPresent}},
	}
	got, err := checkRepo(t, newEnv(t, gh, apps...), 3)
	if err != nil || got.State != workflows.RepoGone {
		t.Errorf("absent installation: got %+v, err %v; want gone", got, err)
	}

	gh.apps["b"].listErr = fmt.Errorf("list: %w", platform.ErrTransient)
	if got, err := checkRepo(t, newEnv(t, gh, apps...), 3); err == nil {
		t.Errorf("failed listing: got %+v, want an error", got)
	}
}
