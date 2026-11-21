package activities_test

import (
	"context"
	"slices"
	"testing"

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
