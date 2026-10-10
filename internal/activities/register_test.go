package activities_test

import (
	"slices"
	"testing"

	"go.temporal.io/sdk/activity"

	"github.com/donaldgifford/boop/internal/activities"
	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/workflows"
)

type registry struct{ names []string }

func (r *registry) RegisterActivityWithOptions(_ any, o activity.RegisterOptions) {
	r.names = append(r.names, o.Name)
}

// TestRegister pins the names workflows execute activities by.
func TestRegister(t *testing.T) {
	t.Parallel()
	var r registry
	activities.New(&activities.Deps{Config: &config.Config{}}).Register(&r)
	activities.NewBudget(nil, "boopd", activities.DefaultBudgetConfig()).Register(&r)
	slices.Sort(r.names)
	want := []string{
		workflows.AcquireBudgetActivity,
		workflows.CheckRepoActivity,
		workflows.DiscoverInstallationActivity,
		workflows.ListInstallationsActivity,
		workflows.ReadRateLimitActivity,
		workflows.RunRenovateActivity,
	}
	slices.Sort(want)
	if !slices.Equal(r.names, want) {
		t.Errorf("registered %v, want %v", r.names, want)
	}
}
