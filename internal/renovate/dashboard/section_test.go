package dashboard_test

import (
	"testing"

	"github.com/donaldgifford/boop/internal/renovate/dashboard"
)

func TestSectionKey(t *testing.T) {
	t.Parallel()
	tests := []struct{ heading, want string }{
		{"Pending Approval", "pending-approval"},
		{"PR Creation Approval Required", "pending-approval"},
		{"Awaiting Schedule", "awaiting-schedule"},
		{"Rate-Limited", "rate-limited"},
		{"Pending Status Checks", "pending-status-checks"},
		{"Errored", "errored"},
		{"Open", "open"},
		{"Ignored or Blocked", "ignored"},
		{"Edited/Blocked", "ignored"},
		{"Config Migration Needed", "other"},
		{"", "other"},
	}
	for _, tt := range tests {
		if got := dashboard.SectionKey(tt.heading); got != tt.want {
			t.Errorf("SectionKey(%q) = %q, want %q", tt.heading, got, tt.want)
		}
	}
}
