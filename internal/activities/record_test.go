package activities_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/donaldgifford/boop/internal/activities"
	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/store"
	"github.com/donaldgifford/boop/internal/workflows"
)

// TestRecordKinds_AreStoreKinds keeps the workflows' kind strings in the
// store's catalogue.
func TestRecordKinds_AreStoreKinds(t *testing.T) {
	t.Parallel()
	for _, k := range []string{
		workflows.KindRepoOnboarded, workflows.KindRepoOffboarded, workflows.KindRepoRefreshed, workflows.KindRepoRunScheduled,
		workflows.KindRunStalled, workflows.KindRunCapacityDenied, workflows.KindRunBudgetWait,
		workflows.KindActionRecheck, workflows.KindDiscoveryFinished,
		workflows.KindInstallationAdded, workflows.KindInstallationSuspended,
	} {
		if !slices.Contains(store.Kinds(), store.Kind(k)) {
			t.Errorf("workflow kind %q is not in the store catalogue", k)
		}
	}
}

func TestRecordEvents_NoPayload(t *testing.T) {
	t.Parallel()
	a := activities.New(&activities.Deps{Config: &config.Config{}, Store: nopRecorder{}})
	err := a.RecordEvents(t.Context(), &workflows.RecordEventsInput{Events: []workflows.RecordedEvent{{Key: "k", Kind: workflows.KindRepoOnboarded}}})
	if err == nil {
		t.Error("RecordEvents(no payload) = nil, want an error")
	}
}

type nopRecorder struct{}

func (nopRecorder) Append(_ context.Context, _ ...store.Event) error { return nil }

func (nopRecorder) ReceivedDeliveries(context.Context, []string) (map[string]bool, error) {
	return map[string]bool{}, nil
}

func (nopRecorder) OpenPullRequests(context.Context, int64) ([]store.PRRef, error) { return nil, nil }

func (nopRecorder) Maintain(context.Context, store.MaintenanceRule, time.Time, store.Retention) (int64, error) {
	return 0, nil
}

// TestMaintenanceRules_AreStoreRules keeps the workflows' rule names in
// step with the store's.
func TestMaintenanceRules_AreStoreRules(t *testing.T) {
	t.Parallel()
	want := make([]string, 0, len(store.MaintenanceRules()))
	for _, r := range store.MaintenanceRules() {
		want = append(want, string(r))
	}
	if !slices.Equal(workflows.MaintenanceRules, want) {
		t.Errorf("workflows.MaintenanceRules = %v, want the store's %v", workflows.MaintenanceRules, want)
	}
}
