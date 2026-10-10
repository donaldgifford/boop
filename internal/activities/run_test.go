package activities_test

import (
	"errors"
	"testing"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/donaldgifford/boop/internal/activities"
	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/kube"
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
