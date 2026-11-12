package activities_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	nodev1 "k8s.io/api/node/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/donaldgifford/boop/internal/activities"
	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/kube"
	"github.com/donaldgifford/boop/internal/workflows"
)

// sandboxHarness runs RunRenovate for the sandboxed profile "jvm"
// against a fake clientset.
type sandboxHarness struct {
	app     *fakeApp
	cs      *fake.Clientset
	env     *testsuite.TestActivityEnvironment
	metrics *needsMetrics
}

// needsMetrics counts NeedsSandbox by app.
type needsMetrics struct {
	activities.NopMetrics
	mu    sync.Mutex
	needs map[string]int
}

func (m *needsMetrics) NeedsSandbox(app string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.needs[app]++
}

func (m *needsMetrics) count(app string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.needs[app]
}

func newSandboxHarness(t *testing.T, sandbox *config.Sandbox, objects ...runtime.Object) *sandboxHarness {
	t.Helper()
	h := &sandboxHarness{app: &fakeApp{}, cs: fake.NewClientset(objects...), metrics: &needsMetrics{needs: map[string]int{}}}
	h.cs.PrependReactor("create", "jobs", func(a k8stesting.Action) (bool, runtime.Object, error) {
		a.(k8stesting.CreateAction).GetObject().(*batchv1.Job).UID = "job-uid"
		return false, nil, nil
	})
	acts := activities.New(&activities.Deps{
		Config: &config.Config{
			Renovate: config.Renovate{Image: "renovate:44@sha256:" + strings.Repeat("a", 64)},
			Apps:     []*config.App{{Name: "main", AppID: 7}},
			Runs:     config.Runs{Namespace: "boopd", PendingTimeout: time.Hour},
			Profiles: map[string]*config.Profile{"jvm": {Name: "jvm", Sandboxed: true, UnsafeExecutions: []string{"gradleWrapper"}}},
			Sandbox:  sandbox,
		},
		GitHub:  &fakeGitHub{apps: map[string]*fakeApp{"main": h.app}},
		Runner:  kube.NewRunner(h.cs, "boopd"),
		Metrics: h.metrics,
		Timings: activities.RunTimings{Heartbeat: 50 * time.Millisecond, Cleanup: time.Second},
	})
	h.env = (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	acts.Register(h.env)
	return h
}

func (h *sandboxHarness) run(t *testing.T) *temporal.ApplicationError {
	t.Helper()
	_, err := h.env.ExecuteActivity(workflows.RunRenovateActivity, &workflows.RunInput{
		RepoID: 1, Slug: "acme/app", InstallationID: 1, Profile: "jvm",
	})
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) {
		t.Fatalf("err = %v, want an application error", err)
	}
	return appErr
}

func (h *sandboxHarness) created() []string {
	var out []string
	for _, a := range h.cs.Actions() {
		if a.GetVerb() == "create" {
			out = append(out, a.GetResource().Resource)
		}
	}
	return out
}

var kataClass = &nodev1.RuntimeClass{ObjectMeta: metav1.ObjectMeta{Name: "kata"}, Handler: "kata"}

// Task 8.1: no sandbox block, or no RuntimeClass, fails closed before
// any mint or create.
func TestRunRenovate_NeedsSandboxBeforeMint(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		sandbox *config.Sandbox
		objects []runtime.Object
	}{
		"no sandbox block": {objects: []runtime.Object{kataClass}},
		"no runtime class": {sandbox: &config.Sandbox{RuntimeClass: "kata"}},
		"other class only": {sandbox: &config.Sandbox{RuntimeClass: "gvisor"}, objects: []runtime.Object{kataClass}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newSandboxHarness(t, tt.sandbox, tt.objects...)
			appErr := h.run(t)
			if appErr.Type() != workflows.ErrTypeNeedsSandbox || !appErr.NonRetryable() {
				t.Errorf("err = %v, want non-retryable %s", appErr, workflows.ErrTypeNeedsSandbox)
			}
			if len(h.app.minted) != 0 || len(h.created()) != 0 {
				t.Errorf("minted %v, created %v; want nothing", h.app.minted, h.created())
			}
			if n := h.metrics.count("main"); n != 1 {
				t.Errorf("boopd_runs_needs_sandbox_total{app=main} = %d, want 1", n)
			}
		})
	}
}

// Task 8.2: a sandboxed pod that stays unschedulable past
// sandbox.pending_timeout is NeedsSandbox, its Job deleted; one pending
// for no sandbox reason stays Pending.
func TestRunRenovate_SandboxPending(t *testing.T) {
	t.Parallel()
	pod := func(cond *corev1.PodCondition) *corev1.Pod {
		p := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "run-1-0-abcde", Namespace: "boopd", Labels: map[string]string{kube.JobNameLabel: "run-1-0"}},
			Status:     corev1.PodStatus{Phase: corev1.PodPending},
		}
		if cond != nil {
			p.Status.Conditions = []corev1.PodCondition{*cond}
		}
		return p
	}
	unschedulable := &corev1.PodCondition{
		Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: corev1.PodReasonUnschedulable,
		Message: "0/5 nodes are available: 5 node(s) didn't match Pod's node affinity/selector.",
	}
	sandbox := &config.Sandbox{RuntimeClass: "kata", PendingTimeout: 200 * time.Millisecond}
	tests := []struct {
		name string
		pod  *corev1.Pod
		want string
	}{
		{name: "unschedulable", pod: pod(unschedulable), want: workflows.ErrTypeNeedsSandbox},
		{name: "pending for another reason", pod: pod(nil), want: workflows.ErrTypePending},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := newSandboxHarness(t, sandbox, kataClass, tt.pod)
			appErr := h.run(t)
			if appErr.Type() != tt.want {
				t.Fatalf("err = %v, want %s", appErr, tt.want)
			}
			want := 0
			if tt.want == workflows.ErrTypeNeedsSandbox {
				want = 1
			}
			if n := h.metrics.count("main"); n != want {
				t.Errorf("boopd_runs_needs_sandbox_total{app=main} = %d, want %d", n, want)
			}
			deleted := false
			for _, a := range h.cs.Actions() {
				deleted = deleted || (a.GetVerb() == "delete" && a.GetResource().Resource == "jobs")
			}
			if !deleted {
				t.Error("the Job was not deleted")
			}
		})
	}
}
