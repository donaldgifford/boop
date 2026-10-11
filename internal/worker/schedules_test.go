package worker_test

import (
	"testing"
	"time"

	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/worker"
	"github.com/donaldgifford/boop/internal/workflows"
)

func TestDiscoverySchedules(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Apps: []*config.App{
		{Name: "boop-bot", Discovery: config.Discovery{Every: 6 * time.Hour}},
		{Name: "other", Discovery: config.Discovery{Every: time.Hour}},
	}}
	got := worker.DiscoverySchedules(cfg, "boopd")
	if len(got) != 2 {
		t.Fatalf("schedules = %d, want one per app", len(got))
	}
	s := got[0]
	if s.ID != "discovery/boop-bot" || s.Every != 6*time.Hour || s.Workflow != workflows.DiscoveryWorkflowName || s.TaskQueue != "boopd" {
		t.Errorf("schedule = %+v", s)
	}
	if in, ok := s.Args[0].(*workflows.DiscoveryInput); !ok || in.App != "boop-bot" {
		t.Errorf("args = %+v, want the app's DiscoveryInput", s.Args)
	}
	if got[1].ID != "discovery/other" || got[1].Every != time.Hour {
		t.Errorf("second schedule = %+v", got[1])
	}
}
