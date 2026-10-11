package activities

import "testing"

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
