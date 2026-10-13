//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"hash/fnv"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.temporal.io/sdk/client"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"

	"github.com/donaldgifford/boop/internal/activities"
	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/kube"
	"github.com/donaldgifford/boop/internal/platform"
	"github.com/donaldgifford/boop/internal/temporal"
	"github.com/donaldgifford/boop/internal/temporal/temporaltest"
	"github.com/donaldgifford/boop/internal/worker"
	"github.com/donaldgifford/boop/internal/workflows"
	"github.com/donaldgifford/boop/test/fakegithub"
)

// syncBuffer is a log sink safe for the worker's goroutines.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// lines returns the JSON log records whose msg is msg.
func (b *syncBuffer) lines(msg string) []map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []map[string]any
	for _, l := range strings.Split(b.buf.String(), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(l), &rec) == nil && rec["msg"] == msg {
			out = append(out, rec)
		}
	}
	return out
}

// quickRepoID finds a repository id whose first-due jitter within
// cadence is under limit, mirroring RepoWorkflow's hash, so the first
// run comes soon.
func quickRepoID(t *testing.T, cadence, limit time.Duration) int64 {
	t.Helper()
	return quickRepoIDFrom(t, cadence, limit, 5000)
}

// quickRepoIDFrom is quickRepoID searching from id from.
func quickRepoIDFrom(t *testing.T, cadence, limit time.Duration, from int64) int64 {
	t.Helper()
	for id := from; id < from+100000; id++ {
		h := fnv.New64a()
		_, _ = h.Write([]byte(strconv.FormatInt(id, 10)))
		if time.Duration(h.Sum64()%uint64(cadence)) < limit {
			return id
		}
	}
	t.Fatal("no quick repository id")
	return 0
}

type workerHarness struct {
	env      *Env
	srv      *temporaltest.Server
	gh       *fakegithub.Server
	log, run *syncBuffer
	repoID   int64
	stop     context.CancelFunc
	done     chan error
}

// workerOption adjusts the worker's config and the fake GitHub before
// the worker starts.
type workerOption func(cfg *config.Config, gh *fakegithub.Server)

// startWorker runs the worker role against k3d, the dev server, the
// stub image and the fake GitHub, with profile as the default profile.
func startWorker(
	t *testing.T, profile string, cadence time.Duration, timings activities.RunTimings, opts ...workerOption,
) *workerHarness {
	t.Helper()
	env := Setup(t)
	srv := temporaltest.Start(t)
	gh := fakegithub.New(t)
	repoID := quickRepoID(t, cadence, 10*time.Second)
	gh.SetRepos(
		fakegithub.Repo{
			ID:            repoID,
			NodeID:        "R_on",
			Slug:          runSlug,
			DefaultBranch: "main",
			HasConfig:     true,
			// JSON5 at renovate.json5, as the fleet's repositories carry it.
			ConfigPath: "renovate.json5",
			Config:     "{\n  // the shared preset\n  extends: ['config:recommended',],\n}\n",
		},
		fakegithub.Repo{ID: repoID + 1, NodeID: "R_off", Slug: "boop-e2e/no-config", DefaultBranch: "main"},
	)
	cfg := &config.Config{
		Renovate:       config.Renovate{Image: env.StubImage, ConfigPaths: platform.DefaultConfigPaths},
		Runs:           config.Runs{Namespace: env.Namespace, PendingTimeout: time.Minute},
		Profiles:       profiles(),
		Order:          []string{"default", "hang", "no-report", "slow", "unschedulable"},
		DefaultProfile: profile,
		UnknownProfile: profile,
		Apps: []*config.App{{
			Name: "main", AppID: 1, Endpoint: gh.BaseURL(), PrivateKey: config.NewSecret(gh.PEM),
			Cadence:   cadence,
			Discovery: config.Discovery{Every: time.Hour, Probe: config.ProbeGraphQL},
			Budget:    config.Budget{ReserveFraction: 0.1, MaxConcurrentRuns: 2},
		}},
	}
	for _, opt := range opts {
		opt(cfg, gh)
	}
	h := &workerHarness{env: env, srv: srv, gh: gh, log: &syncBuffer{}, run: &syncBuffer{}, repoID: repoID, done: make(chan error, 1)}
	ctx, stop := context.WithCancel(context.Background())
	h.stop = stop
	go func() {
		h.done <- worker.Run(ctx, &worker.Options{
			Config:    cfg,
			Temporal:  srv.Client,
			Namespace: srv.Config.Namespace,
			Worker: temporal.WorkerConfig{
				TaskQueue: srv.Config.TaskQueue, ActivityConcurrency: 4, BuildID: "0.1.0", StopTimeout: 3 * time.Minute,
			},
			Clientset: env.Clientset,
			Logger:    slog.New(slog.NewJSONHandler(h.log, nil)),
			RunLog:    slog.New(slog.NewJSONHandler(h.run, nil)),
			Timings:   timings,
		})
	}()
	t.Cleanup(func() {
		stop()
		select {
		case <-h.done:
		case <-time.After(4 * time.Minute):
			t.Error("worker did not stop")
		}
	})
	return h
}

// eventually polls cond every half second until it holds or d passes.
func eventually(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("timed out after %v waiting for %s", d, what)
}

func (h *workerHarness) repoState(ctx context.Context, t *testing.T, id int64) (workflows.RepoState, bool) {
	t.Helper()
	v, err := h.srv.Client.QueryWorkflow(ctx, workflows.RepoWorkflowID(id), "", workflows.StateQuery)
	if err != nil {
		return workflows.RepoState{}, false
	}
	var s workflows.RepoState
	if err := v.Get(&s); err != nil {
		t.Fatal(err)
	}
	return s, true
}

func (h *workerHarness) triggerDiscovery(ctx context.Context, t *testing.T) {
	t.Helper()
	handle := h.srv.Client.ScheduleClient().GetHandle(ctx, workflows.DiscoveryScheduleID("main"))
	eventually(t, time.Minute, "the discovery schedule", func() bool {
		_, err := handle.Describe(ctx)
		return err == nil
	})
	if err := handle.Trigger(ctx, client.ScheduleTriggerOptions{}); err != nil {
		t.Fatalf("trigger schedule: %v", err)
	}
}

// TestWorker_DiscoveryRunRecheck: the schedule fires, a RepoWorkflow
// starts for the repository with the file and none for the one without,
// the first run goes from lease to run_complete with the log forwarded
// and the budget reported, the next due time is set, and a recheck runs
// again at once.
func TestWorker_DiscoveryRunRecheck(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	cadence := 10 * time.Minute
	h := startWorker(t, "default", cadence, activities.RunTimings{Heartbeat: time.Second})

	h.triggerDiscovery(ctx, t)
	eventually(t, 2*time.Minute, "the first run_complete", func() bool { return len(h.log.lines("run_complete")) == 1 })

	if _, ok := h.repoState(ctx, t, h.repoID+1); ok {
		t.Error("a RepoWorkflow started for the repository without the config file")
	}
	var s workflows.RepoState
	eventually(t, 30*time.Second, "the state after the run", func() bool {
		var ok bool
		s, ok = h.repoState(ctx, t, h.repoID)
		return ok && s.LastRun != nil
	})
	if s.LastRun.Outcome != workflows.OutcomeSucceeded || s.Profile != "default" {
		t.Errorf("last run %+v profile %q, want Succeeded on default", s.LastRun, s.Profile)
	}
	if !slices.Equal(s.Extends, []string{"config:recommended"}) {
		t.Errorf("state extends = %q, want config:recommended parsed from renovate.json5", s.Extends)
	}
	if wait := time.Until(s.NextDue); wait < cadence-2*time.Minute || wait > cadence {
		t.Errorf("next due in %v, want about a cadence (%v)", wait, cadence)
	}
	rc := h.log.lines("run_complete")[0]
	if rc["outcome"] != "Succeeded" || rc["workflow_id"] != workflows.RepoWorkflowID(h.repoID) {
		t.Errorf("run_complete = %v", rc)
	}
	// Container start (whole seconds) to the stub's first line, both by
	// the kubelet's clock, so above zero however late the worker saw
	// Running.
	if o, _ := rc["overhead"].(float64); o <= 0 {
		t.Errorf("run_complete overhead = %v, want above zero", rc["overhead"])
	}
	fwd := h.run.lines("renovate")
	if len(fwd) == 0 {
		t.Fatal("no Renovate lines forwarded")
	}
	for _, k := range []string{"repo", "workflow_id", "run_id", "attempt", "job", "profile"} {
		if _, ok := fwd[0][k]; !ok {
			t.Errorf("forwarded line lacks %q: %v", k, fwd[0])
		}
	}
	mints := 0
	for _, m := range h.gh.Mints() {
		if len(m.RepoIDs) > 0 {
			mints++
		}
	}
	if mints != 1 || len(h.gh.Revokes()) != 1 {
		t.Errorf("run mints %d revokes %d, want 1 1", mints, len(h.gh.Revokes()))
	}
	eventually(t, 30*time.Second, "the lease to close", func() bool {
		v, err := h.srv.Client.QueryWorkflow(ctx, workflows.InstallationWorkflowID(1), "", workflows.StateQuery)
		if err != nil {
			return false
		}
		var b workflows.BudgetState
		return v.Get(&b) == nil && len(b.Leases) == 0 && b.Resources[workflows.ResourceCore].Limit == 5000
	})

	if err := h.srv.Client.SignalWorkflow(ctx, workflows.RepoWorkflowID(h.repoID), "", workflows.RecheckSignal, nil); err != nil {
		t.Fatalf("recheck: %v", err)
	}
	eventually(t, 2*time.Minute, "the recheck run", func() bool { return len(h.log.lines("run_complete")) == 2 })
	if jobs, err := h.env.Clientset.BatchV1().Jobs(h.env.Namespace).List(ctx, metav1.ListOptions{}); err != nil || len(jobs.Items) != 0 {
		t.Errorf("jobs left after the runs: %v (err %v)", len(jobs.Items), err)
	}
}

// TestWorker_GracefulStop: SIGTERM (context cancel) with a run in flight
// stops polling but keeps the activity to its soft deadline, which
// deletes the Job and logs run_complete before the worker returns.
func TestWorker_GracefulStop(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	h := startWorker(t, "hang", 10*time.Minute, activities.RunTimings{Heartbeat: time.Second, SoftDeadline: 40 * time.Second})

	h.triggerDiscovery(ctx, t)
	eventually(t, 2*time.Minute, "a running pod", func() bool {
		pods, err := h.env.Clientset.CoreV1().Pods(h.env.Namespace).List(ctx, metav1.ListOptions{LabelSelector: kube.JobNameLabel})
		return err == nil && len(pods.Items) == 1 && pods.Items[0].Status.Phase == "Running"
	})
	stoppedAt := time.Now()
	h.stop()
	select {
	case err := <-h.done:
		if err != nil {
			t.Fatalf("worker.Run: %v", err)
		}
	case <-time.After(3 * time.Minute):
		t.Fatal("worker did not stop within its stop timeout")
	}
	h.done <- nil // for the cleanup
	if time.Since(stoppedAt) < 10*time.Second {
		t.Errorf("worker returned %v after the stop, want it to wait for the run", time.Since(stoppedAt))
	}
	rc := h.log.lines("run_complete")
	if len(rc) != 1 || rc[0]["outcome"] != string(workflows.OutcomeTimedOut) {
		t.Fatalf("run_complete = %v, want one TimedOut", rc)
	}
	if jobs, err := h.env.Clientset.BatchV1().Jobs(h.env.Namespace).List(ctx, metav1.ListOptions{}); err != nil || len(jobs.Items) != 0 {
		t.Errorf("jobs after the graceful stop: %d (err %v), want none", len(jobs.Items), err)
	}
}

// TestWorker_MaxConcurrent (IMPL-0002 task 4.8): with runs.max_concurrent
// 1 and two repositories due at once, the CapacityWorkflow admits one
// run at a time. The second run mints no token and creates no Job until
// the first ends, then it runs.
func TestWorker_MaxConcurrent(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cadence := 30 * time.Minute
	var second int64
	h := startWorker(t, "slow", cadence, activities.RunTimings{Heartbeat: time.Second},
		func(cfg *config.Config, gh *fakegithub.Server) {
			cfg.Runs.MaxConcurrent = 1
			first := quickRepoID(t, cadence, 10*time.Second)
			second = quickRepoIDFrom(t, cadence, 10*time.Second, first+2)
			gh.SetRepos(
				fakegithub.Repo{ID: first, NodeID: "R_first", Slug: runSlug, DefaultBranch: "main", HasConfig: true, Config: "{}"},
				fakegithub.Repo{ID: second, NodeID: "R_second", Slug: "boop-e2e/second", DefaultBranch: "main", HasConfig: true, Config: "{}"},
			)
		})

	// Count the run Jobs alive at once from the watch's adds and deletes.
	w, err := h.env.Clientset.BatchV1().Jobs(h.env.Namespace).Watch(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("watch jobs: %v", err)
	}
	defer w.Stop()
	var (
		mu               sync.Mutex
		alive, most, all int
		mintsAtSecondJob = -1
	)
	go func() {
		for ev := range w.ResultChan() {
			mu.Lock()
			switch ev.Type {
			case watch.Added:
				alive++
				all++
				most = max(most, alive)
				if all == 2 {
					mintsAtSecondJob = runMints(h.gh)
				}
			case watch.Deleted:
				alive--
			}
			mu.Unlock()
		}
	}()

	h.triggerDiscovery(ctx, t)
	eventually(t, 8*time.Minute, "two run_complete", func() bool { return len(h.log.lines("run_complete")) == 2 })

	mu.Lock()
	defer mu.Unlock()
	if most != 1 || all != 2 {
		t.Errorf("run Jobs: %d created, at most %d at once; want 2 created, 1 at a time", all, most)
	}
	if mintsAtSecondJob != 2 {
		t.Errorf("mints when the second Job appeared = %d, want 2: one per admitted run, none for the waiting one", mintsAtSecondJob)
	}
	if got := runMints(h.gh); got != 2 {
		t.Errorf("run mints = %d, want 2", got)
	}
	if s, ok := h.repoState(ctx, t, second); !ok || s.LastRun == nil {
		t.Errorf("the second repository's state = %+v (found %v), want a run", s, ok)
	}
}

// runMints counts the repository-scoped mints: one per admitted run.
func runMints(gh *fakegithub.Server) int {
	n := 0
	for _, m := range gh.Mints() {
		if len(m.RepoIDs) > 0 {
			n++
		}
	}
	return n
}
