package workflows_test

import (
	"log/slog"
	"path/filepath"
	"testing"

	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/worker"

	"github.com/donaldgifford/boop/internal/workflows"
)

// TestReplay_Histories replays every recorded history through today's
// workflow code, so a change that would break a running workflow fails
// here (IMPL-0004 OQ3). TestRecordReplayHistories records them.
func TestReplay_Histories(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob(filepath.Join("testdata", "replay", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no replay histories in testdata/replay")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			t.Parallel()
			r := worker.NewWorkflowReplayer()
			workflows.Register(r)
			logger := log.NewStructuredLogger(slog.New(slog.DiscardHandler))
			if err := r.ReplayWorkflowHistoryFromJSONFile(logger, f); err != nil {
				t.Errorf("replay %s: %v", f, err)
			}
		})
	}
}
