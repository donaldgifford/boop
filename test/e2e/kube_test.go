//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/donaldgifford/boop/internal/jobspec"
	"github.com/donaldgifford/boop/internal/kube"
)

const (
	runSlug  = "boop-e2e/scratch"
	stubSlug = "boop-bot/scratch"
)

// requests records every API request a Runner makes.
type requests struct {
	mu   sync.Mutex
	seen []string
}

func (r *requests) RecordRequest(verb, resource, _ string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, verb+" "+resource)
}

func (r *requests) deletes() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, s := range r.seen {
		if strings.HasPrefix(s, "delete") {
			out = append(out, s)
		}
	}
	return out
}

type run struct {
	env    *Env
	runner *kube.Runner
	cs     kubernetes.Interface
	reqs   *requests
}

func newRun(t *testing.T) *run {
	t.Helper()
	env := Setup(t)
	reqs := &requests{}
	cs, err := kubernetes.NewForConfig(kube.Instrument(env.Config, reqs))
	if err != nil {
		t.Fatal(err)
	}
	return &run{
		env:    env,
		cs:     cs,
		reqs:   reqs,
		runner: kube.NewRunner(cs, env.Namespace, kube.WithPollInterval(250*time.Millisecond)),
	}
}

// job builds the run's Job with jobspec, as RunRenovate will, plus the
// stub's knobs on the container.
func (r *run) job(t *testing.T, repoID int64, stub map[string]string, mutate func(*jobspec.BuildInput)) *batchv1.Job {
	t.Helper()
	in := &jobspec.BuildInput{
		Namespace:   r.env.Namespace,
		Image:       r.env.StubImage,
		App:         jobspec.App{Endpoint: "https://api.github.com"},
		Repo:        jobspec.Repo{ID: repoID, Slug: runSlug, DefaultBranch: "main", InstallationID: 1},
		Profile:     jobspec.Profile{Name: "baseline"},
		TokenSecret: jobspec.JobName(repoID, 0),
	}
	if mutate != nil {
		mutate(in)
	}
	job, err := jobspec.BuildJob(in)
	if err != nil {
		t.Fatalf("BuildJob: %v", err)
	}
	c := &job.Spec.Template.Spec.Containers[0]
	keys := make([]string, 0, len(stub))
	for k := range stub {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		c.Env = append(c.Env, corev1.EnvVar{Name: k, Value: stub[k]})
	}
	return job
}

// start creates the Job suspended, its Secret, and unsuspends it.
func (r *run) start(ctx context.Context, t *testing.T, job *batchv1.Job) *batchv1.Job {
	t.Helper()
	created, err := r.runner.CreateSuspended(ctx, job)
	if err != nil {
		t.Fatalf("CreateSuspended: %v", err)
	}
	secret, err := jobspec.BuildTokenSecret(created, "ghs_e2e-token")
	if err != nil {
		t.Fatalf("BuildTokenSecret: %v", err)
	}
	if _, err := r.runner.CreateSecret(ctx, secret); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	if err := r.runner.Unsuspend(ctx, created.Name); err != nil {
		t.Fatalf("Unsuspend: %v", err)
	}
	return created
}

func (r *run) follow(ctx context.Context, t *testing.T, name string) []string {
	t.Helper()
	var lines []string
	err := r.runner.FollowLog(ctx, name, time.Time{}, func(l kube.Line) error {
		lines = append(lines, l.Text)
		return nil
	})
	if err != nil {
		t.Fatalf("FollowLog: %v", err)
	}
	return lines
}

func (r *run) assertGone(ctx context.Context, t *testing.T, name string) {
	t.Helper()
	pods, err := r.env.Clientset.CoreV1().Pods(r.env.Namespace).List(ctx, metav1.ListOptions{LabelSelector: kube.JobNameLabel + "=" + name})
	if err != nil {
		t.Fatal(err)
	}
	if len(pods.Items) != 0 {
		t.Errorf("pods of %s after Delete = %d, want 0", name, len(pods.Items))
	}
	if _, err := r.env.Clientset.CoreV1().Secrets(r.env.Namespace).Get(ctx, name, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Errorf("secret %s after Delete: err = %v, want NotFound", name, err)
	}
}

func fixtureLines(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile("../stub-renovate/fixtures/live.log")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(strings.ReplaceAll(string(b), stubSlug, runSlug), "\n"), "\n")
}

// TestKube_Lifecycle: suspended Job, Secret before unsuspend, Running,
// the whole log in order, exit 0, and a foreground delete that leaves no
// pod and no Secret and is the only delete issued.
func TestKube_Lifecycle(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	r := newRun(t)
	job := r.job(t, 1001, map[string]string{"STUB_DELAY": "50ms"}, nil)

	created, err := r.runner.CreateSuspended(ctx, job)
	if err != nil {
		t.Fatalf("CreateSuspended: %v", err)
	}
	time.Sleep(3 * time.Second)
	pods, err := r.env.Clientset.CoreV1().Pods(r.env.Namespace).List(ctx, metav1.ListOptions{LabelSelector: kube.JobNameLabel + "=" + created.Name})
	if err != nil {
		t.Fatal(err)
	}
	if len(pods.Items) != 0 {
		t.Fatalf("suspended job has %d pods, want 0", len(pods.Items))
	}

	secret, err := jobspec.BuildTokenSecret(created, "ghs_e2e-token")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.runner.CreateSecret(ctx, secret); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	if _, err := r.env.Clientset.CoreV1().Secrets(r.env.Namespace).Get(ctx, created.Name, metav1.GetOptions{}); err != nil {
		t.Fatalf("secret before unsuspend: %v", err)
	}
	if err := r.runner.Unsuspend(ctx, created.Name); err != nil {
		t.Fatalf("Unsuspend: %v", err)
	}
	if _, err := r.runner.WaitRunning(ctx, created.Name, 2*time.Minute); err != nil {
		t.Fatalf("WaitRunning: %v", err)
	}

	got := r.follow(ctx, t, created.Name)
	if want := fixtureLines(t); !slices.Equal(got, want) {
		t.Errorf("log = %d lines, want the fixture's %d, whole and in order\n got: %q\nwant: %q", len(got), len(want), got, want)
	}
	exit, err := r.runner.ExitCode(ctx, created.Name)
	if err != nil || exit.Code != 0 {
		t.Errorf("ExitCode = %+v, %v; want 0", exit, err)
	}
	if err := r.runner.Delete(ctx, created.Name); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	r.assertGone(ctx, t, created.Name)
	if d := r.reqs.deletes(); !slices.Equal(d, []string{"delete jobs"}) {
		t.Errorf("deletes issued = %q, want only [delete jobs]", d)
	}
}

// TestKube_LargeReportLine: a 1 MiB report line arrives whole.
func TestKube_LargeReportLine(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	r := newRun(t)
	created := r.start(ctx, t, r.job(t, 1002, map[string]string{"STUB_REPORT_BYTES": "1048576"}, nil))
	if _, err := r.runner.WaitRunning(ctx, created.Name, 2*time.Minute); err != nil {
		t.Fatalf("WaitRunning: %v", err)
	}
	var report string
	for _, l := range r.follow(ctx, t, created.Name) {
		if strings.Contains(l, `"msg":"Printing report"`) {
			report = l
		}
	}
	if len(report) < 1<<20 {
		t.Fatalf("report line = %d bytes, want >= 1 MiB", len(report))
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(report), &v); err != nil {
		t.Errorf("report line is not whole JSON: %v", err)
	}
	if err := r.runner.Delete(ctx, created.Name); err != nil {
		t.Fatal(err)
	}
}

// TestKube_NonZeroExit: the container's exit code is read.
func TestKube_NonZeroExit(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	r := newRun(t)
	created := r.start(ctx, t, r.job(t, 1003, map[string]string{"STUB_EXIT_CODE": "3"}, nil))
	if _, err := r.runner.WaitRunning(ctx, created.Name, 2*time.Minute); err != nil {
		t.Fatalf("WaitRunning: %v", err)
	}
	r.follow(ctx, t, created.Name)
	exit, err := r.runner.ExitCode(ctx, created.Name)
	if err != nil || exit.Code != 3 || exit.Reason != "Error" {
		t.Errorf("ExitCode = %+v, %v; want 3/Error", exit, err)
	}
	if err := r.runner.Delete(ctx, created.Name); err != nil {
		t.Fatal(err)
	}
	r.assertGone(ctx, t, created.Name)
}

// TestKube_DeadlineExceeded: a hung run under a short
// activeDeadlineSeconds ends with DeadlineExceeded.
func TestKube_DeadlineExceeded(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	r := newRun(t)
	job := r.job(t, 1004, map[string]string{"STUB_HANG_AFTER": "1"}, func(in *jobspec.BuildInput) {
		in.ActiveDeadline = 15 * time.Second
	})
	created := r.start(ctx, t, job)
	if _, err := r.runner.WaitRunning(ctx, created.Name, 2*time.Minute); err != nil {
		t.Fatalf("WaitRunning: %v", err)
	}
	exit, err := r.runner.ExitCode(ctx, created.Name)
	if err != nil || exit.Reason != batchv1.JobReasonDeadlineExceeded {
		t.Errorf("ExitCode = %+v, %v; want DeadlineExceeded", exit, err)
	}
	if err := r.runner.Delete(ctx, created.Name); err != nil {
		t.Fatal(err)
	}
	r.assertGone(ctx, t, created.Name)
}

// TestKube_PendingTimeout: a pod that cannot schedule hits the pending
// timeout and is deleted with its Job.
func TestKube_PendingTimeout(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	r := newRun(t)
	job := r.job(t, 1005, nil, func(in *jobspec.BuildInput) {
		in.Profile.Pod.NodeSelector = map[string]string{"boopd.dev/never": "true"}
	})
	created := r.start(ctx, t, job)
	_, err := r.runner.WaitRunning(ctx, created.Name, 10*time.Second)
	if !errors.Is(err, kube.ErrPending) {
		t.Fatalf("WaitRunning = %v, want ErrPending", err)
	}
	if err := r.runner.Delete(ctx, created.Name); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	r.assertGone(ctx, t, created.Name)
}
