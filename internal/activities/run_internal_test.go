package activities

import (
	"testing"
	"time"
)

func TestPresetSlug(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"github>boop-bot/renovate-config:default.json": "boop-bot/renovate-config",
		"github>boop-bot/renovate-config//sub:x.json":  "boop-bot/renovate-config",
		"local>acme/presets#v1":                        "acme/presets",
		"github>acme/presets":                          "acme/presets",
		"config:recommended":                           "",
		"npm>some-package":                             "",
		"":                                             "",
		"github>nobody":                                "",
	}
	for in, want := range tests {
		if got := presetSlug(in); got != want {
			t.Errorf("presetSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRunOverhead(t *testing.T) {
	t.Parallel()
	started := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name                        string
		started, running, firstLine time.Time
		want                        time.Duration
	}{
		{
			// Task 7.9: Renovate logged before the worker saw Running, which
			// read as zero overhead against the sighting.
			name:    "first line before the worker saw Running",
			started: started, running: started.Add(3 * time.Second), firstLine: started.Add(2 * time.Second),
			want: 2 * time.Second,
		},
		{name: "no container start falls back to the sighting", running: started, firstLine: started.Add(time.Second), want: time.Second},
		{name: "no log line", started: started, running: started},
		{name: "never ran", firstLine: started},
		{name: "line stamped before the start", started: started, firstLine: started.Add(-time.Second)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := runOverhead(tt.started, tt.running, tt.firstLine); got != tt.want {
				t.Errorf("runOverhead() = %v, want %v", got, tt.want)
			}
		})
	}
}
