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

// Only Apps with a webhook block get a delivery-audit Schedule.
func TestDeliveryAuditSchedules(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Ingest: config.Ingest{AuditEvery: 15 * time.Minute},
		Apps: []*config.App{
			{Name: "boop-bot", Webhook: &config.Webhook{Bot: "boop-bot[bot]"}},
			{Name: "quiet"},
		},
	}
	got := worker.DeliveryAuditSchedules(cfg, "boopd")
	if len(got) != 1 {
		t.Fatalf("schedules = %d, want one for the webhook app", len(got))
	}
	s := got[0]
	if s.ID != "delivery-audit/boop-bot" || s.Every != 15*time.Minute || s.Workflow != workflows.DeliveryAuditWorkflowName || s.TaskQueue != "boopd" {
		t.Errorf("schedule = %+v", s)
	}
	if in, ok := s.Args[0].(*workflows.DeliveryAuditInput); !ok || *in != (workflows.DeliveryAuditInput{App: "boop-bot", Every: 15 * time.Minute}) {
		t.Errorf("args = %+v, want the app's DeliveryAuditInput with the interval", s.Args)
	}
	if none := worker.DeliveryAuditSchedules(&config.Config{Apps: []*config.App{{Name: "quiet"}}}, "boopd"); len(none) != 0 {
		t.Errorf("schedules without a webhook app = %v", none)
	}
}
