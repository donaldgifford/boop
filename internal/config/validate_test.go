package config_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/donaldgifford/boop/internal/config"
)

// TestValidation breaks the example in one place per case and asserts
// the diagnostic's summary and the line it points at. at is a substring
// of the broken source; the diagnostic must name its line.
func TestValidation(t *testing.T) {
	t.Parallel()
	example, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatal(err)
	}
	const digest = "@sha256:0f5b1a9e4c3d2b1a0f9e8d7c6b5a4938271605f4e3d2c1b0a9f8e7d6c5b4a392"

	tests := []struct {
		name    string
		old     string
		new     string
		summary string
		at      string
	}{
		{name: "unknown attribute", old: `log_level     = "info"`, new: `log_lvl = "info"`, summary: "Unsupported argument", at: "log_lvl"},
		{name: "unknown block", old: "runs {", new: "runz {}\nruns {", summary: "Unsupported block type", at: "runz"},
		{name: "image not pinned", old: digest, new: "", summary: "Image not pinned", at: "44-full\""},
		{name: "config_path absolute", old: `"renovate.json" #`, new: `"/renovate.json" #`, summary: "Invalid config_path", at: `"/renovate.json"`},
		{
			name:    "config_path escapes",
			old:     `"renovate.json" #`,
			new:     `"../renovate.json" #`,
			summary: "Invalid config_path",
			at:      `"../renovate.json"`,
		},
		{
			name:    "config_path unclean",
			old:     `"renovate.json" #`,
			new:     `"a//renovate.json" #`,
			summary: "Invalid config_path",
			at:      `"a//renovate.json"`,
		},
		{name: "shared_preset json5", old: "default.json\"", new: "default.json5\"", summary: "Invalid shared_preset", at: "default.json5"},
		{name: "dry_run enum", old: `dry_run       = "full"`, new: `dry_run = "everything"`, summary: "Invalid value", at: `"everything"`},
		{name: "probe enum", old: `probe         = "graphql"`, new: `probe = "soap"`, summary: "Invalid value", at: `"soap"`},
		{name: "bad duration", old: `pending_timeout = "10m"`, new: `pending_timeout = "ten"`, summary: "Invalid duration", at: `"ten"`},
		{name: "zero duration", old: `cadence       = "24h"`, new: `cadence = "0s"`, summary: "Invalid duration", at: `"0s"`},
		{name: "unknown default_profile", old: `default_profile = "baseline"`, new: `default_profile = "base"`, summary: "", at: `"base"`},
		{name: "unknown unknown_profile", old: `unknown_profile = "strict"`, new: `unknown_profile = "strictest"`, summary: "", at: `"strictest"`},
		{name: "unknown rule profile", old: `profile  = "node"`, new: `profile  = "nodejs"`, summary: "", at: `"nodejs"`},
		{name: "unknown inherit", old: `inherit = "python" #`, new: `inherit = "pithon" #`, summary: "", at: `"pithon"`},
		{name: "unknown in order", old: `"node", "python", "strict"]`, new: `"node", "python", "strict", "extra"]`, summary: "", at: `"extra"`},
		{
			name:    "order duplicate",
			old:     `["baseline", "node", "python", "strict"]`,
			new:     `["baseline", "node", "python", "strict", "node"]`,
			summary: "Duplicate profile in order",
			at:      `"strict", "node"]`,
		},
		{
			name:    "order missing",
			old:     `["baseline", "node", "python", "strict"]`,
			new:     `["baseline", "node", "python"]`,
			summary: "Profile missing from order",
			at:      `["baseline", "node", "python"]`,
		},
		{
			name:    "inherit cycle",
			old:     `profile "baseline" {`,
			new:     "profile \"baseline\" {\n  inherit = \"strict\"",
			summary: "Profile inheritance cycle",
			at:      "inherit = \"baseline\"\n  pod {",
		},
		{
			name:    "duplicate profile",
			old:     `profile "node" {`,
			new:     "profile \"node\" {}\nprofile \"node\" {",
			summary: "Duplicate profile",
			at:      "profile \"node\" {\n",
		},
		{
			name:    "forbidden profile option",
			old:     "customEnvVariables = {",
			new:     "allowScripts = true\n    customEnvVariables = {",
			summary: "Forbidden Renovate option",
			at:      "allowScripts",
		},
		{
			name:    "forbidden global option",
			old:     "prHourlyLimit = 0",
			new:     "exposeAllEnv = true",
			summary: "Forbidden Renovate option",
			at:      "exposeAllEnv",
		},
		{
			name:    "customEnvVariables not strings",
			old:     `UV_NO_BUILD     = "1"`,
			new:     `UV_NO_BUILD = 1`,
			summary: "Forbidden Renovate option",
			at:      "customEnvVariables",
		},
		{
			name:    "reserve_fraction too high",
			old:     "reserve_fraction    = 0.10",
			new:     "reserve_fraction = 0.6",
			summary: "Invalid reserve_fraction",
			at:      "0.6",
		},
		{
			name:    "reserve_fraction negative",
			old:     "reserve_fraction    = 0.10",
			new:     "reserve_fraction = -0.1",
			summary: "Invalid reserve_fraction",
			at:      "-0.1",
		},
		{
			name:    "max_concurrent_runs negative",
			old:     "max_concurrent_runs = 10",
			new:     "max_concurrent_runs = -1",
			summary: "Invalid max_concurrent_runs",
			at:      "= -1",
		},
		{
			name:    "unknown estimate resource",
			old:     "{ core = 300, graphql = 150 }",
			new:     "{ core = 300, search = 1 }",
			summary: "Invalid default_estimate",
			at:      "search",
		},
		{name: "secret ref empty name", old: `name = "boop-bot-app"`, new: `name = ""`, summary: "Invalid secret ref", at: `name = ""`},
		{name: "secret ref empty key", old: `key  = "url"`, new: `key = ""`, summary: "Invalid secret ref", at: `key = ""`},
		{
			name:    "secret ref missing key",
			old:     `key  = "private-key.pem"`,
			new:     ``,
			summary: "Missing required argument",
			at:      "private_key_secret_ref {",
		},
		{name: "app id zero", old: "app_id        = 123456", new: "app_id = 0", summary: "Invalid app_id", at: "app_id = 0"},
		{name: "app id missing", old: "app_id        = 123456", new: "", summary: "Missing required argument", at: `app "boop-bot" {`},
		{
			name:    "duplicate app id",
			old:     `app "boop-bot" {`,
			new:     "app \"other\" {\n  app_id = 123456\n  private_key_secret_ref {\n    name = \"o\"\n    key = \"k\"\n  }\n}\napp \"boop-bot\" {",
			summary: "",
			at:      "app_id        = 123456",
		},
		{name: "allowlist not positive", old: "installations = []", new: "installations = [12, 0]", summary: "Invalid installation id", at: "0]"},
		{name: "allowlist not integer", old: "installations = []", new: "installations = [1.5]", summary: "Invalid installation id", at: "1.5"},
		{
			name:    "endpoint not https",
			old:     `"https://api.github.com"`,
			new:     `"http://api.github.com"`,
			summary: "Invalid endpoint",
			at:      `"http://api.github.com"`,
		},
		{name: "namespace invalid", old: `namespace       = "boopd"`, new: `namespace = "Boop_D"`, summary: "Invalid namespace", at: `"Boop_D"`},
		{name: "bad quantity", old: `memory = "6Gi"`, new: `memory = "lots"`, summary: "Invalid quantity", at: `limits = { memory = "lots" }`},
		{
			name:    "empty profile rule",
			old:     "managers = [\"npm\", \"bun\"]\n  profile  = \"node\"",
			new:     "profile = \"node\"",
			summary: "Empty profile rule",
			at:      "profile_rule {\n  extends  = [\"github>boop-bot/renovate-config:node\"]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if !strings.Contains(string(example), tt.old) {
				t.Fatalf("example lacks %q", tt.old)
			}
			src := strings.Replace(string(example), tt.old, tt.new, 1)
			if tt.name == "empty profile rule" {
				src = strings.Replace(src, "  extends  = [\"github>boop-bot/renovate-config:node\"]\n", "", 1)
				tt.at = "profile_rule {\n  profile = \"node\""
			}
			idx := strings.Index(src, tt.at)
			if idx < 0 {
				t.Fatalf("broken source lacks %q", tt.at)
			}
			line := strings.Count(src[:idx], "\n") + 1

			cfg, diags := config.Parse("boopd.hcl", []byte(src))
			if !diags.HasErrors() || cfg != nil {
				t.Fatalf("Parse() = %v, no error; want %q at line %d", cfg != nil, tt.summary, line)
			}
			var out strings.Builder
			_, _ = diags.WriteTo(&out)
			wantPos := fmt.Sprintf("boopd.hcl:%d:", line)
			found := false
			for _, d := range diags.Diagnostics {
				if d.Subject == nil || d.Subject.Start.Line != line {
					continue
				}
				if strings.HasPrefix(d.Summary, tt.summary) {
					found = true
				}
			}
			if !found {
				t.Errorf("no %q diagnostic at %s; got:\n%s", tt.summary, wantPos, out.String())
			}
		})
	}
}

// TestValidation_ReportsEveryError checks that several independent
// errors all come back, not just the first.
func TestValidation_ReportsEveryError(t *testing.T) {
	t.Parallel()
	example, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatal(err)
	}
	src := strings.NewReplacer(
		"reserve_fraction    = 0.10", "reserve_fraction = 0.9",
		"app_id        = 123456", "app_id = -4",
		`"renovate.json" #`, `"/abs" #`,
	).Replace(string(example))
	_, diags := config.Parse("boopd.hcl", []byte(src))
	want := []string{"Invalid reserve_fraction", "Invalid app_id", "Invalid config_path"}
	for _, w := range want {
		found := false
		for _, d := range diags.Diagnostics {
			found = found || d.Summary == w
		}
		if !found {
			t.Errorf("Parse() lacks %q among %d diagnostics", w, len(diags.Diagnostics))
		}
	}
}

func TestDefaults(t *testing.T) {
	t.Parallel()
	src := []byte(`
renovate {
  image = "ghcr.io/renovatebot/renovate:44@sha256:0f5b1a9e4c3d2b1a0f9e8d7c6b5a4938271605f4e3d2c1b0a9f8e7d6c5b4a392"
}
runs {
  namespace = "boopd"
}
profile "baseline" {}
order           = ["baseline"]
default_profile = "baseline"
unknown_profile = "baseline"
app "a" {
  app_id = 1
  private_key_secret_ref {
    name = "a"
    key  = "k"
  }
}
`)
	cfg, diags := config.Parse("min.hcl", src)
	if diags.HasErrors() {
		var b strings.Builder
		_, _ = diags.WriteTo(&b)
		t.Fatalf("Parse(minimal) diagnostics:\n%s", b.String())
	}
	app := cfg.Apps[0]
	checks := []struct {
		name      string
		got, want any
	}{
		{"secrets_dir", cfg.SecretsDir, config.DefaultSecretsDir},
		{"config_path", cfg.Renovate.ConfigPath, "renovate.json"},
		{"log_level", cfg.Renovate.LogLevel, "info"},
		{"dry_run", cfg.Renovate.DryRun, ""},
		{"pending_timeout", cfg.Runs.PendingTimeout, config.DefaultPendingTimeout},
		{"max_concurrent", cfg.Runs.MaxConcurrent, 0},
		{"endpoint", app.Endpoint, config.DefaultEndpoint},
		{"cadence", app.Cadence, config.DefaultCadence},
		{"discovery.every", app.Discovery.Every, config.DefaultDiscoveryEvery},
		{"discovery.probe", app.Discovery.Probe, config.ProbeGraphQL},
		{"skip_forks", app.Discovery.SkipForks, true},
		{"skip_archived", app.Discovery.SkipArchived, true},
		{"reserve_fraction", app.Budget.ReserveFraction, 0.10},
		{"max_concurrent_runs", app.Budget.MaxConcurrentRuns, 10},
		{"estimate core", app.Budget.DefaultEstimate["core"], 300},
		{"estimate graphql", app.Budget.DefaultEstimate["graphql"], 150},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("default %s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestEnvFunction(t *testing.T) {
	t.Parallel()
	example, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatal(err)
	}
	src := strings.Replace(string(example), `namespace       = "boopd"`, `namespace = env("POD_NAMESPACE")`, 1)
	getenv := func(k string) string {
		if k == "POD_NAMESPACE" {
			return "renovate"
		}
		return ""
	}
	cfg, diags := config.Parse("boopd.hcl", []byte(src), config.WithGetenv(getenv))
	if diags.HasErrors() {
		t.Fatalf("Parse() with env(): %v", diags.Error())
	}
	if cfg.Runs.Namespace != "renovate" {
		t.Errorf("runs.namespace = %q, want renovate from env()", cfg.Runs.Namespace)
	}
	if _, diags := config.Parse("boopd.hcl", []byte(src)); !diags.HasErrors() {
		t.Error("Parse() with env() unset: no error, want the empty namespace rejected")
	}
}
