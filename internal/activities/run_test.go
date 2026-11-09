package activities_test

import (
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/donaldgifford/boop/internal/activities"
	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/kube"
	"github.com/donaldgifford/boop/internal/platform"
	"github.com/donaldgifford/boop/internal/workflows"
)

// A failed mint is an infrastructure error and creates nothing.
func TestRunRenovate_MintFailure(t *testing.T) {
	t.Parallel()
	cs := fake.NewClientset()
	app := &fakeApp{mintErr: errors.New("401 bad credentials")}
	acts := activities.New(&activities.Deps{
		Config: &config.Config{Apps: []*config.App{{Name: "main", AppID: 7}}, Runs: config.Runs{Namespace: "boopd"}},
		GitHub: &fakeGitHub{apps: map[string]*fakeApp{"main": app}},
		Runner: kube.NewRunner(cs, "boopd"),
	})
	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	acts.Register(env)

	_, err := env.ExecuteActivity(workflows.RunRenovateActivity, &workflows.RunInput{RepoID: 1, Slug: "acme/app", InstallationID: 1})
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) || appErr.Type() != workflows.ErrTypeInfrastructure {
		t.Fatalf("err = %v, want an %s application error", err, workflows.ErrTypeInfrastructure)
	}
	if n := len(cs.Actions()); n != 0 {
		t.Errorf("Kubernetes saw %d actions after a failed mint, want 0", n)
	}
	if len(app.revoked) != 0 {
		t.Errorf("revoked %v with no token minted", app.revoked)
	}
}

// A pod that takes longer than the heartbeat interval to start is still
// heartbeated, so a slow schedule hits the pending timeout, not the
// activity's heartbeat timeout.
func TestRunRenovate_HeartbeatsWhilePending(t *testing.T) {
	t.Parallel()
	cs := fake.NewClientset()
	cs.PrependReactor("create", "jobs", func(a k8stesting.Action) (bool, runtime.Object, error) {
		a.(k8stesting.CreateAction).GetObject().(*batchv1.Job).UID = "job-uid" // the fake assigns none
		return false, nil, nil
	})
	app := &fakeApp{}
	acts := activities.New(&activities.Deps{
		Config: &config.Config{
			Renovate: config.Renovate{Image: "renovate:44@sha256:" + strings.Repeat("a", 64)},
			Apps:     []*config.App{{Name: "main", AppID: 7}},
			Runs:     config.Runs{Namespace: "boopd", PendingTimeout: time.Second},
			Profiles: map[string]*config.Profile{"default": {Name: "default"}},
		},
		GitHub:  &fakeGitHub{apps: map[string]*fakeApp{"main": app}},
		Runner:  kube.NewRunner(cs, "boopd"),
		Timings: activities.RunTimings{Heartbeat: 100 * time.Millisecond, Cleanup: time.Second},
	})
	env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
	var beats atomic.Int32
	env.SetOnActivityHeartbeatListener(func(*activity.Info, converter.EncodedValues) { beats.Add(1) })
	acts.Register(env)

	_, err := env.ExecuteActivity(workflows.RunRenovateActivity, &workflows.RunInput{
		RepoID: 1, Slug: "acme/app", InstallationID: 1, Profile: "default",
	})
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) || appErr.Type() != workflows.ErrTypePending {
		t.Fatalf("err = %v, want a %s application error", err, workflows.ErrTypePending)
	}
	if beats.Load() == 0 {
		t.Error("no heartbeat while the pod was pending")
	}
}

// A sandboxed run mints with sandbox.token_permissions, an ordinary run
// with none (IMPL-0006 task 6.2); GitHub refusing the set is a
// non-retryable TokenPermissions error that creates nothing (6.4).
func TestRunRenovate_TokenPermissions(t *testing.T) {
	t.Parallel()
	perms := config.DefaultTokenPermissions()
	newActs := func(app *fakeApp, cs *fake.Clientset) *testsuite.TestActivityEnvironment {
		acts := activities.New(&activities.Deps{
			Config: &config.Config{
				Renovate: config.Renovate{Image: "renovate:44@sha256:" + strings.Repeat("a", 64)},
				Apps:     []*config.App{{Name: "main", AppID: 7}},
				Runs:     config.Runs{Namespace: "boopd", PendingTimeout: 100 * time.Millisecond},
				Profiles: map[string]*config.Profile{
					"default": {Name: "default"},
					"jvm":     {Name: "jvm", Sandboxed: true},
				},
				Sandbox: &config.Sandbox{RuntimeClass: "kata", ServiceAccount: "boopd-sandbox", TokenPermissions: perms},
			},
			GitHub:  &fakeGitHub{apps: map[string]*fakeApp{"main": app}},
			Runner:  kube.NewRunner(cs, "boopd"),
			Timings: activities.RunTimings{Heartbeat: 50 * time.Millisecond, Cleanup: time.Second},
		})
		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		acts.Register(env)
		return env
	}
	jobUID := func(a k8stesting.Action) (bool, runtime.Object, error) {
		a.(k8stesting.CreateAction).GetObject().(*batchv1.Job).UID = "job-uid"
		return false, nil, nil
	}

	app := &fakeApp{}
	for i, profile := range []string{"default", "jvm"} {
		cs := fake.NewClientset()
		cs.PrependReactor("create", "jobs", jobUID)
		_, err := newActs(app, cs).ExecuteActivity(workflows.RunRenovateActivity, &workflows.RunInput{
			RepoID: int64(i + 1), Slug: "acme/app", InstallationID: 1, Profile: profile,
		})
		var appErr *temporal.ApplicationError
		if !errors.As(err, &appErr) || appErr.Type() != workflows.ErrTypePending {
			t.Fatalf("%s: err = %v, want pending", profile, err)
		}
	}
	if len(app.mintPerms) != 2 || app.mintPerms[0] != nil || !maps.Equal(app.mintPerms[1], perms) {
		t.Errorf("mint permissions = %v; want none for default, the sandbox set for jvm", app.mintPerms)
	}

	cs := fake.NewClientset()
	refused := &fakeApp{mintErr: fmt.Errorf("422: %w", platform.ErrTokenPermissions)}
	_, err := newActs(refused, cs).ExecuteActivity(workflows.RunRenovateActivity, &workflows.RunInput{
		RepoID: 1, Slug: "acme/app", InstallationID: 1, Profile: "jvm",
	})
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) || appErr.Type() != workflows.ErrTypeTokenPermissions || !appErr.NonRetryable() {
		t.Fatalf("err = %v, want a non-retryable %s error", err, workflows.ErrTypeTokenPermissions)
	}
	if n := len(cs.Actions()); n != 0 {
		t.Errorf("Kubernetes saw %d actions after a refused mint, want 0", n)
	}
}
