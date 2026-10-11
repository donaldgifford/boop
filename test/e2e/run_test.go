//go:build e2e

package e2e

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/donaldgifford/boop/internal/activities"
	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/jobspec"
	"github.com/donaldgifford/boop/internal/kube"
	"github.com/donaldgifford/boop/internal/renovate"
	"github.com/donaldgifford/boop/internal/workflows"
	"github.com/donaldgifford/boop/test/fakegithub"
)

const (
	presetSlug = "boop-e2e/renovate-config"
	presetID   = 9000
)

// countingMetrics records token outcomes.
type countingMetrics struct {
	activities.NopMetrics
	mu          sync.Mutex
	mints       []string
	revocations []string
	pending     int
}

func (m *countingMetrics) TokenMint(o string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mints = append(m.mints, o)
}

func (m *countingMetrics) TokenRevocation(o string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revocations = append(m.revocations, o)
}

func (m *countingMetrics) PendingTimeout() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending++
}

// runHarness runs the RunRenovate activity against the k3d cluster with
// the stub image and an in-process GitHub.
type runHarness struct {
	env     *Env
	gh      *fakegithub.Server
	metrics *countingMetrics
	acts    *activities.Activities
}

// Profiles the scenarios run under; the stub reads its knobs from
// customEnvVariables in RENOVATE_CONFIG.
func profiles() map[string]*config.Profile {
	stub := func(name string, vars map[string]any) *config.Profile {
		return &config.Profile{Name: name, Renovate: map[string]any{"customEnvVariables": vars}}
	}
	return map[string]*config.Profile{
		"default":   {Name: "default"},
		"hang":      stub("hang", map[string]any{"STUB_HANG_AFTER": "6"}),
		"no-report": stub("no-report", map[string]any{"STUB_DROP_REPORT": "true"}),
		"unschedulable": {Name: "unschedulable", Pod: jobspec.PodOverlay{
			NodeSelector: map[string]string{"boopd.dev/never": "true"},
		}},
	}
}

func newRunHarness(t *testing.T, timings activities.RunTimings) *runHarness {
	t.Helper()
	env := Setup(t)
	gh := fakegithub.New(t)
	gh.SetRepos(
		fakegithub.Repo{ID: 4242, NodeID: "R_4242", Slug: runSlug, DefaultBranch: "main", HasConfig: true, Config: "{}"},
		fakegithub.Repo{ID: presetID, NodeID: "R_preset", Slug: presetSlug, DefaultBranch: "main"},
	)
	cfg := &config.Config{
		Renovate: config.Renovate{
			Image:        env.StubImage,
			ConfigPath:   "renovate.json",
			SharedPreset: "github>" + presetSlug + ":default.json",
		},
		Runs:           config.Runs{Namespace: env.Namespace, PendingTimeout: 15 * time.Second},
		Profiles:       profiles(),
		DefaultProfile: "default",
		Apps: []*config.App{{
			Name: "main", AppID: 1, Endpoint: gh.BaseURL(), PrivateKey: config.NewSecret(gh.PEM),
		}},
	}
	m := &countingMetrics{}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	acts := activities.New(&activities.Deps{
		Config:  cfg,
		Runner:  kube.NewRunner(env.Clientset, env.Namespace, kube.WithPollInterval(250*time.Millisecond)),
		Metrics: m,
		Logger:  slog.New(slog.NewTextHandler(testWriter{t}, nil)),
		RunLog:  quiet,
		Timings: timings,
	})
	return &runHarness{env: env, gh: gh, metrics: m, acts: acts}
}

// testWriter sends the activity's own log to t.Log.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}

func input(profile string) *workflows.RunInput {
	return &workflows.RunInput{
		RepoID: 4242, Slug: runSlug, DefaultBranch: "main", InstallationID: 1, Profile: profile, LeaseID: "lease-1",
	}
}

// execute runs the activity in the SDK's test environment with ctx as
// the activity's background context.
func (h *runHarness) execute(ctx context.Context, in *workflows.RunInput) (*workflows.RunResult, error) {
	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	env.SetWorkerOptions(worker.Options{BackgroundActivityContext: ctx})
	h.acts.Register(env)
	val, err := env.ExecuteActivity(workflows.RunRenovateActivity, in)
	if err != nil {
		return nil, err
	}
	var res workflows.RunResult
	return &res, val.Get(&res)
}

// assertClean checks the namespace holds no Job, pod or Secret.
func (h *runHarness) assertClean(ctx context.Context, t *testing.T) {
	t.Helper()
	ns := h.env.Namespace
	cs := h.env.Clientset
	jobs, err := cs.BatchV1().Jobs(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	pods, err := cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := cs.CoreV1().Secrets(ns).List(ctx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=boopd"})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(jobs.Items) + len(pods.Items) + len(secrets.Items); n != 0 {
		t.Errorf("namespace holds %d jobs, %d pods, %d run secrets, want none", len(jobs.Items), len(pods.Items), len(secrets.Items))
	}
}

// assertTokens checks one run-token mint scoped to the repository and
// the preset, and one revoke of that token. Mints without repository ids
// are the installation client's own token (used for the preset lookup),
// which is cached and not part of the run.
func (h *runHarness) assertTokens(t *testing.T) {
	t.Helper()
	var mints []fakegithub.Mint
	for _, m := range h.gh.Mints() {
		if len(m.RepoIDs) > 0 {
			mints = append(mints, m)
		}
	}
	if len(mints) != 1 {
		t.Fatalf("mints = %+v, want one", mints)
	}
	if !slices.Equal(mints[0].RepoIDs, []int64{4242, presetID}) {
		t.Errorf("token scoped to %v, want the repository and the preset repository", mints[0].RepoIDs)
	}
	if rev := h.gh.Revokes(); !slices.Equal(rev, []string{mints[0].Token}) {
		t.Errorf("revokes = %v, want the minted token once", rev)
	}
}

func appError(t *testing.T, err error) *temporal.ApplicationError {
	t.Helper()
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) {
		t.Fatalf("error %v is not an application error", err)
	}
	return appErr
}

// expected is what the scanner makes of the live fixture, without its
// report line when dropReport.
func expected(t *testing.T, dropReport bool) *renovate.Result {
	t.Helper()
	s := renovate.NewScanner(runSlug)
	for _, l := range fixtureLines(t) {
		if dropReport && strings.Contains(l, `"msg":"Printing report"`) {
			continue
		}
		s.Feed(l)
	}
	return s.Result()
}

// TestRunRenovate_HappyPath: the fixture's result, readings before and
// after, one mint and one revoke, and nothing left in the namespace.
func TestRunRenovate_HappyPath(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	h := newRunHarness(t, activities.RunTimings{Heartbeat: time.Second})

	res, err := h.execute(ctx, input("default"))
	if err != nil {
		t.Fatalf("RunRenovate: %v", err)
	}
	want := expected(t, false)
	if res.Outcome != workflows.OutcomeSucceeded || res.RepositoryResult != want.Finished.Result || res.ExitCode != 0 {
		t.Errorf("outcome %s result %q exit %d, want Succeeded %q 0", res.Outcome, res.RepositoryResult, res.ExitCode, want.Finished.Result)
	}
	if res.ReportMissing || len(res.Tuples) != len(want.Tuples) || len(res.Tuples) == 0 {
		t.Fatalf("tuples = %+v (missing %v), want %+v", res.Tuples, res.ReportMissing, want.Tuples)
	}
	for i := range want.Tuples {
		if res.Tuples[i] != workflows.UpdateTuple(want.Tuples[i]) {
			t.Errorf("tuple %d = %+v, want %+v", i, res.Tuples[i], want.Tuples[i])
		}
	}
	if !slices.Equal(res.Managers, want.Managers) || len(res.Problems) != len(want.Problems) {
		t.Errorf("managers %v problems %+v, want %v %+v", res.Managers, res.Problems, want.Managers, want.Problems)
	}
	if res.Progress != (workflows.Progress{BranchesChanged: want.Progress.BranchesChanged, PRsChanged: want.Progress.PRsChanged}) {
		t.Errorf("progress = %+v, want %+v", res.Progress, want.Progress)
	}
	if res.RenovateVersion != want.RenovateVersion {
		t.Errorf("version = %q, want %q", res.RenovateVersion, want.RenovateVersion)
	}
	for name, r := range map[string]*workflows.Readings{"before": res.RateBefore, "after": res.RateAfter} {
		if r == nil || r.Resources["core"].Limit != 5000 || r.Resources["graphql"].Remaining != 4900 {
			t.Errorf("rate %s = %+v, want the fake's readings", name, r)
		}
	}
	if res.PodStart <= 0 || res.Duration <= 0 {
		t.Errorf("pod start %v duration %v, want both measured", res.PodStart, res.Duration)
	}
	h.assertTokens(t)
	h.assertClean(ctx, t)
}

// TestRunRenovate_PendingTimeout: an unschedulable pod fails pending
// and the Job is deleted.
func TestRunRenovate_PendingTimeout(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	h := newRunHarness(t, activities.RunTimings{})

	_, err := h.execute(ctx, input("unschedulable"))
	if typ := appError(t, err).Type(); typ != workflows.ErrTypePending {
		t.Fatalf("error type = %q (%v), want %q", typ, err, workflows.ErrTypePending)
	}
	if h.metrics.pending != 1 {
		t.Errorf("pending timeouts counted = %d, want 1", h.metrics.pending)
	}
	h.assertTokens(t)
	h.assertClean(ctx, t)
}

// TestRunRenovate_SoftDeadline: a stub that hangs is deleted at the soft
// deadline and the run classifies TimedOut with the scanner's progress.
func TestRunRenovate_SoftDeadline(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	h := newRunHarness(t, activities.RunTimings{Heartbeat: time.Second, SoftDeadline: 45 * time.Second})

	res, err := h.execute(ctx, input("hang"))
	if err != nil {
		t.Fatalf("RunRenovate: %v", err)
	}
	if res.Outcome != workflows.OutcomeTimedOut {
		t.Errorf("outcome = %s, want TimedOut", res.Outcome)
	}
	if res.Progress != (workflows.Progress{BranchesChanged: 2, PRsChanged: 1}) {
		t.Errorf("progress = %+v, want the two branches and one PR before the hang", res.Progress)
	}
	if res.RepositoryResult != "" {
		t.Errorf("result = %q, want none", res.RepositoryResult)
	}
	h.assertTokens(t)
	h.assertClean(ctx, t)
}

// TestRunRenovate_ReportMissing: without the report line the tuples are
// rebuilt from branch and PR events.
func TestRunRenovate_ReportMissing(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	h := newRunHarness(t, activities.RunTimings{})

	res, err := h.execute(ctx, input("no-report"))
	if err != nil {
		t.Fatalf("RunRenovate: %v", err)
	}
	if !res.ReportMissing || res.Outcome != workflows.OutcomeSucceeded {
		t.Fatalf("report missing %v outcome %s, want true Succeeded", res.ReportMissing, res.Outcome)
	}
	want := expected(t, true)
	if len(res.Tuples) != len(want.Tuples) || len(res.Tuples) == 0 {
		t.Fatalf("rebuilt tuples = %+v, want %+v", res.Tuples, want.Tuples)
	}
	for i := range want.Tuples {
		if res.Tuples[i] != workflows.UpdateTuple(want.Tuples[i]) {
			t.Errorf("rebuilt tuple %d = %+v, want %+v", i, res.Tuples[i], want.Tuples[i])
		}
	}
	h.assertClean(ctx, t)
}

// TestRunRenovate_RevokeFailure is counted, not an error.
func TestRunRenovate_RevokeFailure(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	h := newRunHarness(t, activities.RunTimings{})
	h.gh.SetRevokeStatus(http.StatusInternalServerError)

	res, err := h.execute(ctx, input("default"))
	if err != nil {
		t.Fatalf("RunRenovate: %v", err)
	}
	if res.Outcome != workflows.OutcomeSucceeded {
		t.Errorf("outcome = %s, want Succeeded", res.Outcome)
	}
	if !slices.Equal(h.metrics.revocations, []string{"failure"}) {
		t.Errorf("revocations counted = %v, want one failure", h.metrics.revocations)
	}
	h.assertTokens(t)
	h.assertClean(ctx, t)
}

// TestRunRenovate_Cancel: cancelling the activity deletes the Job, with
// foreground propagation, before the activity returns.
func TestRunRenovate_Cancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	h := newRunHarness(t, activities.RunTimings{Heartbeat: time.Second})

	actCtx, stop := context.WithCancel(ctx)
	defer stop()
	name := jobspec.JobName(4242, 0)
	go func() {
		// Cancel once the pod is Running.
		for actCtx.Err() == nil {
			pods, err := h.env.Clientset.CoreV1().Pods(h.env.Namespace).List(actCtx,
				metav1.ListOptions{LabelSelector: kube.JobNameLabel + "=" + name})
			if err == nil && len(pods.Items) == 1 && pods.Items[0].Status.Phase == "Running" {
				time.Sleep(2 * time.Second)
				stop()
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
	}()
	_, err := h.execute(actCtx, input("hang"))
	if err == nil {
		t.Fatal("RunRenovate returned no error after cancellation")
	}
	if _, err := h.env.Clientset.BatchV1().Jobs(h.env.Namespace).Get(ctx, name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("job after cancellation: err = %v, want NotFound", err)
	}
	h.assertTokens(t)
	h.assertClean(ctx, t)
}
