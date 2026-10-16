package activities_test

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"

	"github.com/donaldgifford/boop/internal/activities"
	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/platform"
	"github.com/donaldgifford/boop/internal/workflows"
	"github.com/donaldgifford/boop/test/fakegithub"
)

// starter records SignalWithStart calls.
type starter struct {
	mu    sync.Mutex
	calls []signalCall
}

type signalCall struct {
	ID     string
	Signal string
	Arg    workflows.Discovered
	Opts   client.StartWorkflowOptions
	Type   any
	State  *workflows.RepoState
}

func (s *starter) SignalWithStartWorkflow(_ context.Context, id, signal string, arg any,
	opts client.StartWorkflowOptions, //nolint:gocritic // hugeParam: the client.Client signature
	wf any, args ...any,
) (client.WorkflowRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := signalCall{ID: id, Signal: signal, Arg: arg.(workflows.Discovered), Opts: opts, Type: wf}
	if len(args) == 1 {
		c.State, _ = args[0].(*workflows.RepoState)
	}
	s.calls = append(s.calls, c)
	return nil, nil
}

func (s *starter) ids() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.calls))
	for i := range s.calls {
		out[i] = s.calls[i].ID
	}
	return out
}

// fleetConfigs are the config files fleet rotates through: JSON at
// renovate.json, JSON5 at renovate.json5 and .github/renovate.json5.
var fleetConfigs = []struct{ path, text string }{
	{"renovate.json", `{"extends":["local>acme/renovate-config"]}`},
	{"renovate.json5", "{\n  // shared preset\n  extends: ['local>acme/renovate-config',],\n}"},
	{".github/renovate.json5", `{extends: ["local>acme/renovate-config"]}`},
}

// fleet is n repositories; every third has a config file, rotating
// through fleetConfigs, and every tenth is a fork.
func fleet(n int) []fakegithub.Repo {
	repos := make([]fakegithub.Repo, n)
	for i := range repos {
		id := int64(i + 1)
		cfg := fleetConfigs[(i/3)%len(fleetConfigs)]
		repos[i] = fakegithub.Repo{
			ID: id, NodeID: fmt.Sprintf("R_%d", id), Slug: fmt.Sprintf("acme/r%d", id), DefaultBranch: "main",
			Fork:       i%10 == 9,
			HasConfig:  i%3 == 0,
			Config:     cfg.text,
			ConfigPath: cfg.path,
		}
	}
	return repos
}

// onboardedIDs is every repository id in repos with the file that is not
// a fork, as workflow ids.
func onboardedIDs(repos []fakegithub.Repo, fromPage int) []string {
	var out []string
	for i, r := range repos {
		if i/100+1 >= fromPage && r.HasConfig && !r.Fork {
			out = append(out, workflows.RepoWorkflowID(r.ID))
		}
	}
	return out
}

type discoveryHarness struct {
	gh      *fakegithub.Server
	start   *starter
	env     *testsuite.TestActivityEnvironment
	metrics *probedMetrics
	beats   []workflows.DiscoverProgress
	mu      sync.Mutex
}

// probedMetrics records ConfigProbed counts by config attribute.
type probedMetrics struct {
	activities.NopMetrics
	mu     sync.Mutex
	counts map[string]int
}

func (m *probedMetrics) ConfigProbed(_ int64, outcome string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.counts == nil {
		m.counts = map[string]int{}
	}
	m.counts[outcome]++
}

func newDiscovery(t *testing.T, gh *fakegithub.Server, probe string) *discoveryHarness {
	t.Helper()
	h := &discoveryHarness{gh: gh, start: &starter{}, metrics: &probedMetrics{}}
	cfg := &config.Config{
		Renovate: config.Renovate{ConfigPaths: platform.DefaultConfigPaths},
		Apps: []*config.App{{
			Name: "main", AppID: 7, Endpoint: gh.BaseURL(), PrivateKey: config.NewSecret(gh.PEM),
			Discovery: config.Discovery{Every: 6 * time.Hour, Probe: probe, SkipForks: true},
			Budget:    config.Budget{ReserveFraction: 0.1},
		}},
	}
	acts := activities.New(&activities.Deps{Config: cfg, Temporal: h.start, TaskQueue: "boopd", Metrics: h.metrics})
	h.env = (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	acts.Register(h.env)
	h.env.SetOnActivityHeartbeatListener(func(_ *activity.Info, d converter.EncodedValues) {
		var p workflows.DiscoverProgress
		if err := d.Get(&p); err == nil {
			h.mu.Lock()
			h.beats = append(h.beats, p)
			h.mu.Unlock()
		}
	})
	return h
}

func (h *discoveryHarness) run() (*workflows.DiscoverSummary, error) {
	val, err := h.env.ExecuteActivity(workflows.DiscoverInstallationActivity, &workflows.DiscoverInput{App: "main", InstallationID: 1})
	if err != nil {
		return nil, err
	}
	var sum workflows.DiscoverSummary
	return &sum, val.Get(&sum)
}

func (h *discoveryHarness) lastBeat() workflows.DiscoverProgress {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.beats) == 0 {
		return workflows.DiscoverProgress{}
	}
	return h.beats[len(h.beats)-1]
}

// fastFake is a fake GitHub whose discovered core limit does not
// throttle the test.
func fastFake(t *testing.T, repos []fakegithub.Repo) *fakegithub.Server {
	t.Helper()
	gh := fakegithub.New(t)
	gh.SetRate("core", fakegithub.Rate{Limit: 3_600_000, Remaining: 3_600_000, Reset: time.Now().Add(time.Hour)})
	gh.SetRepos(repos...)
	return gh
}

func TestDiscoverInstallation_GraphQL(t *testing.T) {
	t.Parallel()
	repos := fleet(250)
	h := newDiscovery(t, fastFake(t, repos), config.ProbeGraphQL)

	sum, err := h.run()
	if err != nil {
		t.Fatalf("DiscoverInstallation: %v", err)
	}
	want := onboardedIDs(repos, 1)
	if got := h.start.ids(); !slices.Equal(got, want) {
		t.Fatalf("signalled %d workflows, want %d:\n got %v\nwant %v", len(got), len(want), got, want)
	}
	// 10 default paths: at most 10 repositories per query (INV-0002).
	sizes := h.gh.ProbeSizes()
	if *sum != (workflows.DiscoverSummary{InstallationID: 1, Pages: 3, Seen: 250, Onboarded: len(want), ProbeCost: len(sizes)}) {
		t.Errorf("summary = %+v, want one point per query (%d)", *sum, len(sizes))
	}
	if i := slices.IndexFunc(sizes, func(n int) bool { return n > 10 }); i >= 0 {
		t.Errorf("probe sizes = %v, want at most 10 repositories per query", sizes)
	}
	if r := h.gh.RateReads(); r != 3 {
		t.Errorf("rate reads = %d, want one before each page", r)
	}
	assertExtends(t, h.start.calls)

	c := h.start.calls[0]
	if c.Signal != workflows.DiscoveredSignal || c.Type != workflows.RepoWorkflowName || c.Opts.ID != c.ID || c.Opts.TaskQueue != "boopd" {
		t.Errorf("call = %+v", c)
	}
	wantSig := workflows.Discovered{
		Slug: "acme/r1", DefaultBranch: "main", InstallationID: 1, DiscoveryInterval: 6 * time.Hour,
		Extends: []string{"local>acme/renovate-config"},
	}
	if !equalDiscovered(c.Arg, wantSig) {
		t.Errorf("signal = %+v, want %+v", c.Arg, wantSig)
	}
	if c.State == nil || c.State.RepoID != 1 || c.State.Platform != workflows.Platform || c.State.LastSeen.IsZero() {
		t.Errorf("start state = %+v", c.State)
	}
}

func equalDiscovered(a, b workflows.Discovered) bool {
	return a.Slug == b.Slug && a.DefaultBranch == b.DefaultBranch && a.InstallationID == b.InstallationID &&
		a.DiscoveryInterval == b.DiscoveryInterval && slices.Equal(a.Extends, b.Extends)
}

func TestDiscoverInstallation_REST(t *testing.T) {
	t.Parallel()
	repos := fleet(30)
	h := newDiscovery(t, fastFake(t, repos), config.ProbeREST)
	if _, err := h.run(); err != nil {
		t.Fatalf("DiscoverInstallation: %v", err)
	}
	if got, want := h.start.ids(), onboardedIDs(repos, 1); !slices.Equal(got, want) {
		t.Errorf("signalled %v, want %v", got, want)
	}
	if q := h.gh.ProbeQueries(); q != 0 {
		t.Errorf("GraphQL queries = %d with the REST probe", q)
	}
	assertExtends(t, h.start.calls)
}

// assertExtends checks every signal carries the fleet's extends,
// whichever file and format it came from.
func assertExtends(t *testing.T, calls []signalCall) {
	t.Helper()
	for i := range calls {
		if c := &calls[i]; !slices.Equal(c.Arg.Extends, []string{"local>acme/renovate-config"}) {
			t.Errorf("signal %s extends = %q, want the fleet preset", c.ID, c.Arg.Extends)
		}
	}
}

// Every repository discovery sees is counted once, by whether its
// config was present, absent or unparsed; an unparsed one is still
// onboarded, with no extends.
func TestDiscoverInstallation_CountsConfigOutcomes(t *testing.T) {
	t.Parallel()
	repos := []fakegithub.Repo{
		{ID: 1, NodeID: "R_1", Slug: "acme/json5", DefaultBranch: "main", HasConfig: true, ConfigPath: "renovate.json5", Config: `{extends: ["a"],}`},
		{ID: 2, NodeID: "R_2", Slug: "acme/none", DefaultBranch: "main"},
		{ID: 3, NodeID: "R_3", Slug: "acme/broken", DefaultBranch: "main", HasConfig: true, ConfigPath: ".renovaterc", Config: `{extends: [`},
		{ID: 4, NodeID: "R_4", Slug: "acme/other-name", DefaultBranch: "main", HasConfig: true, ConfigPath: "renovate.yaml", Config: `{}`},
	}
	for _, probe := range []string{config.ProbeGraphQL, config.ProbeREST} {
		t.Run(probe, func(t *testing.T) {
			t.Parallel()
			h := newDiscovery(t, fastFake(t, repos), probe)
			sum, err := h.run()
			if err != nil {
				t.Fatalf("DiscoverInstallation: %v", err)
			}
			if got, want := h.start.ids(), []string{workflows.RepoWorkflowID(1), workflows.RepoWorkflowID(3)}; !slices.Equal(got, want) {
				t.Errorf("signalled %v, want %v", got, want)
			}
			if sum.Seen != 4 || sum.Onboarded != 2 {
				t.Errorf("summary = %+v, want 4 seen and 2 onboarded", *sum)
			}
			want := map[string]int{activities.ConfigPresent: 1, activities.ConfigAbsent: 2, activities.ConfigUnparsed: 1}
			if !maps.Equal(h.metrics.counts, want) {
				t.Errorf("ConfigProbed counts = %v, want %v", h.metrics.counts, want)
			}
			if c := h.start.calls[1]; c.Arg.Extends != nil {
				t.Errorf("unparsed config's signal extends = %q, want none", c.Arg.Extends)
			}
		})
	}
}

// A failure on page 2 fails the attempt; the retry, given the last
// heartbeat, signals only pages 2 and 3. The SDK throttles heartbeats
// after the first, so page 1's is the one the test can rely on.
func TestDiscoverInstallation_ResumesFromHeartbeat(t *testing.T) {
	t.Parallel()
	repos := fleet(250)
	gh := fastFake(t, repos)
	gh.FailPage(2, 1000)
	h := newDiscovery(t, gh, config.ProbeGraphQL)
	if _, err := h.run(); err == nil {
		t.Fatal("want the page-2 failure")
	}
	beat := h.lastBeat()
	if beat.Page != 1 || beat.Seen != 100 {
		t.Fatalf("last heartbeat = %+v, want page 1 of 100", beat)
	}

	gh.FailPage(2, 0)
	retry := newDiscovery(t, gh, config.ProbeGraphQL)
	retry.env.SetHeartbeatDetails(beat)
	sum, err := retry.run()
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got, want := retry.start.ids(), onboardedIDs(repos, 2); !slices.Equal(got, want) {
		t.Errorf("retry signalled %v, want pages 2-3 only: %v", got, want)
	}
	if sum.Seen != 250 || sum.Onboarded != len(onboardedIDs(repos, 1)) {
		t.Errorf("summary = %+v, want the totals across both attempts", *sum)
	}
}

// Under the reserve, the activity sleeps to the reset, heartbeating,
// then continues.
func TestDiscoverInstallation_SleepsUnderReserve(t *testing.T) {
	t.Parallel()
	repos := fleet(5)
	gh := fastFake(t, repos)
	reset := time.Now().Add(1500 * time.Millisecond)
	gh.SetRate("graphql", fakegithub.Rate{Limit: 5000, Remaining: 10, Reset: reset})
	h := newDiscovery(t, gh, config.ProbeGraphQL)

	start := time.Now()
	if _, err := h.run(); err != nil {
		t.Fatalf("DiscoverInstallation: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 500*time.Millisecond {
		t.Errorf("returned after %v, want a sleep to the reset", elapsed)
	}
	if r := gh.RateReads(); r < 2 {
		t.Errorf("rate reads = %d, want a re-read after the sleep", r)
	}
	if got, want := h.start.ids(), onboardedIDs(repos, 1); !slices.Equal(got, want) {
		t.Errorf("signalled %v, want %v", got, want)
	}
}
