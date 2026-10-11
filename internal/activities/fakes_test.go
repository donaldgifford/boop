package activities_test

import (
	"context"
	"sync"
	"time"

	"github.com/donaldgifford/boop/internal/activities"
	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/platform"
	"github.com/donaldgifford/boop/internal/platform/github"
	"github.com/donaldgifford/boop/internal/workflows"
)

// fakeGitHub hands out one fakeApp and one fakeInstall per id.
type fakeGitHub struct {
	apps     map[string]*fakeApp
	installs map[int64]*fakeInstall
	token    activities.RateReader
}

func (f *fakeGitHub) App(app *config.App) (activities.AppAPI, error) {
	return f.apps[app.Name], nil
}

func (f *fakeGitHub) Installation(_ *config.App, id int64, _ string) (activities.InstallationAPI, error) {
	return f.installs[id], nil
}

func (f *fakeGitHub) Token(*config.App, string) (activities.RateReader, error) {
	return f.token, nil
}

type fakeApp struct {
	installs []platform.Installation

	mu      sync.Mutex
	minted  [][]int64
	revoked []string
	mintErr error
	revErr  error
}

func (f *fakeApp) ListInstallations(context.Context) ([]platform.Installation, error) {
	return f.installs, nil
}

func (f *fakeApp) Mint(_ context.Context, _ int64, ids []int64) (string, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.mintErr != nil {
		return "", time.Time{}, f.mintErr
	}
	f.minted = append(f.minted, ids)
	return "ghs_fake", time.Now().Add(time.Hour), nil
}

func (f *fakeApp) Revoke(_ context.Context, tok string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked = append(f.revoked, tok)
	return f.revErr
}

// fakeInstall serves pages, probe answers, readings and repo states.
type fakeInstall struct {
	pages    [][]platform.Repository
	configs  map[string]github.ConfigProbe // by node id
	readings *workflows.Readings
	state    platform.RepoState
	repo     *platform.Repository
	ids      map[string]int64 // slug -> id

	mu        sync.Mutex
	limit     int
	probed    int
	failAfter int // DiscoverPages errors after this many pages when > 0
	failed    bool
}

func (f *fakeInstall) DiscoverPages(_ context.Context, _ platform.DiscoveryFilter, fn func(github.Page) error) error {
	seen := 0
	for i, repos := range f.pages {
		seen += len(repos)
		if err := fn(github.Page{Number: i + 1, Seen: seen, Repos: repos}); err != nil {
			return err
		}
		f.mu.Lock()
		fail := f.failAfter > 0 && !f.failed && i+1 == f.failAfter
		if fail {
			f.failed = true
		}
		f.mu.Unlock()
		if fail {
			return platform.ErrTransient
		}
	}
	return nil
}

func (f *fakeInstall) probe(nodeIDs []string) *github.ProbeResult {
	f.mu.Lock()
	f.probed += len(nodeIDs)
	f.mu.Unlock()
	res := &github.ProbeResult{Configs: make(map[string]github.ConfigProbe), Queries: 1, Cost: 1, Remaining: 4999}
	for _, id := range nodeIDs {
		if c, ok := f.configs[id]; ok {
			res.Configs[id] = c
		}
	}
	return res
}

func (f *fakeInstall) ProbeConfig(_ context.Context, nodeIDs []string, _ string) (*github.ProbeResult, error) {
	return f.probe(nodeIDs), nil
}

func (f *fakeInstall) ProbeConfigREST(_ context.Context, repos []platform.Repository, _ string) (*github.ProbeResult, error) {
	ids := make([]string, len(repos))
	for i := range repos {
		ids[i] = repos[i].NodeID
	}
	return f.probe(ids), nil
}

func (f *fakeInstall) ReadRateLimit(context.Context) (*workflows.Readings, error) {
	return f.readings, nil
}

func (f *fakeInstall) CheckRepo(context.Context, int64) (platform.RepoState, *platform.Repository, error) {
	return f.state, f.repo, nil
}

func (f *fakeInstall) RepoIDBySlug(_ context.Context, slug string) (int64, error) {
	return f.ids[slug], nil
}

func (f *fakeInstall) SetDiscoveredLimit(limit int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.limit = limit
}

// readings is a healthy /rate_limit answer.
func readings(now time.Time) *workflows.Readings {
	return &workflows.Readings{
		ObservedAt: now,
		Resources: map[string]workflows.Reading{
			"core":    {Limit: 5000, Remaining: 4900, Reset: now.Add(time.Hour)},
			"graphql": {Limit: 5000, Remaining: 4900, Reset: now.Add(time.Hour)},
		},
	}
}
