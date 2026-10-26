package config_test

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/platform"
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
	const redisRef = "redis_secret_ref {\n    name = \"boopd-redis\"\n    key  = \"url\"\n  }"

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
		{
			name:    "config_paths absolute",
			old:     "  shared_preset =",
			new:     "  config_paths = [\"/renovate.json\"]\n  shared_preset =",
			summary: "Invalid config_paths",
			at:      `["/renovate.json"]`,
		},
		{
			name:    "config_paths escapes",
			old:     "  shared_preset =",
			new:     "  config_paths = [\"../renovate.json\"]\n  shared_preset =",
			summary: "Invalid config_paths",
			at:      `["../renovate.json"]`,
		},
		{
			name:    "config_paths unclean",
			old:     "  shared_preset =",
			new:     "  config_paths = [\"a//renovate.json\"]\n  shared_preset =",
			summary: "Invalid config_paths",
			at:      `["a//renovate.json"]`,
		},
		{
			name:    "config_paths duplicate",
			old:     "  shared_preset =",
			new:     "  config_paths = [\"renovate.json5\", \"renovate.json5\"]\n  shared_preset =",
			summary: "Invalid config_paths",
			at:      `["renovate.json5", "renovate.json5"]`,
		},
		{
			name:    "redis_url with redis_secret_ref",
			old:     "  redis_secret_ref {",
			new:     "  redis_url = \"redis://both:6379\"\n  redis_secret_ref {",
			summary: "Conflicting Redis settings",
			at:      `"redis://both:6379"`,
		},
		{
			name:    "redis_url password",
			old:     redisRef,
			new:     `redis_url = "redis://u:pw@h:6379"`,
			summary: "Password in redis_url",
			at:      `"redis://u:pw@h:6379"`,
		},
		{name: "redis_url scheme", old: redisRef, new: `redis_url = "http://h:6379"`, summary: "Invalid redis_url", at: `"http://h:6379"`},
		{name: "redis_url host", old: redisRef, new: `redis_url = "redis:///0"`, summary: "Invalid redis_url", at: `"redis:///0"`},
		{
			name:    "redis password without redis_url",
			old:     redisRef,
			new:     "redis_password_secret_ref { # alone\n    name = \"p\"\n    key  = \"k\"\n  }",
			summary: "Missing redis_url",
			at:      "{ # alone",
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
			old:     `inherit = "baseline" # scripts are already ignored by default`,
			new:     `inherit = "baseline" # scripts are already ignored by default` + "\n  renovate {\n    allowScripts = true\n  }",
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
			old:     `inherit = "baseline" # scripts are already ignored by default`,
			new:     `inherit = "baseline" # scripts are already ignored by default` + "\n  renovate {\n    customEnvVariables = { UV_NO_BUILD = 1 }\n  }",
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
		"  shared_preset =", "  config_paths = [\"/abs\"]\n  shared_preset =",
	).Replace(string(example))
	_, diags := config.Parse("boopd.hcl", []byte(src))
	want := []string{"Invalid reserve_fraction", "Invalid app_id", "Invalid config_paths"}
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
	if !slices.Equal(cfg.Renovate.ConfigPaths, platform.DefaultConfigPaths) {
		t.Errorf("default config_paths = %q, want %q", cfg.Renovate.ConfigPaths, platform.DefaultConfigPaths)
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

// minimalApp is a config with one App; tests append blocks to it.
const minimalApp = `
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
`

func TestIngest_Defaults(t *testing.T) {
	t.Parallel()
	src := minimalApp + `
app "boop-bot" {
  app_id = 1
  private_key_secret_ref {
    name = "a"
    key  = "k"
  }
  webhook {
    secret_ref {
      name = "boop-bot-webhook"
      key  = "secret"
    }
  }
}
app "other" {
  app_id = 2
  private_key_secret_ref {
    name = "b"
    key  = "k"
  }
}
`
	cfg, diags := config.Parse("ingest.hcl", []byte(src))
	if diags.HasErrors() {
		var b strings.Builder
		_, _ = diags.WriteTo(&b)
		t.Fatalf("Parse diagnostics:\n%s", b.String())
	}
	want := config.Ingest{
		Path: config.DefaultIngestPath, Settle: config.DefaultSettle,
		MinInterval: config.DefaultMinInterval, AuditEvery: config.DefaultAuditEvery,
	}
	if cfg.Ingest != want {
		t.Errorf("ingest = %+v, want the defaults %+v", cfg.Ingest, want)
	}
	w := cfg.Apps[0].Webhook
	if w == nil || w.Bot != "boop-bot[bot]" || w.SecretRef != (config.SecretRef{Name: "boop-bot-webhook", Key: "secret"}) {
		t.Errorf("webhook = %+v, want the bot defaulted from the App label", w)
	}
	if cfg.Apps[1].Webhook != nil {
		t.Errorf("app without a webhook block has %+v", cfg.Apps[1].Webhook)
	}
	if got := cfg.WebhookApps(); len(got) != 1 || got[0].Name != "boop-bot" {
		t.Errorf("WebhookApps() = %v, want boop-bot only", got)
	}
}

func TestIngest_Set(t *testing.T) {
	t.Parallel()
	src := minimalApp + `
ingest {
  path         = "/hooks"
  settle       = "0s"
  min_interval = "0s"
  audit_every  = "1m"
}
app "a" {
  app_id = 1
  private_key_secret_ref {
    name = "a"
    key  = "k"
  }
  webhook {
    bot = "renovate-bot[bot]"
    secret_ref {
      name = "w"
      key  = "s"
    }
  }
}
`
	cfg, diags := config.Parse("ingest.hcl", []byte(src))
	if diags.HasErrors() {
		var b strings.Builder
		_, _ = diags.WriteTo(&b)
		t.Fatalf("Parse diagnostics:\n%s", b.String())
	}
	if want := (config.Ingest{Path: "/hooks", AuditEvery: time.Minute}); cfg.Ingest != want {
		t.Errorf("ingest = %+v, want %+v (zero settle and min_interval allowed)", cfg.Ingest, want)
	}
	if bot := cfg.Apps[0].Webhook.Bot; bot != "renovate-bot[bot]" {
		t.Errorf("bot = %q", bot)
	}
}

// Each bad ingest or webhook setting gets a diagnostic on its own line.
func TestIngest_Validation(t *testing.T) {
	t.Parallel()
	app := func(webhook string) string {
		return "app \"a\" {\n  app_id = 1\n  private_key_secret_ref {\n    name = \"a\"\n    key  = \"k\"\n  }\n" + webhook + "}\n"
	}
	webhook := func(bot, name string) string {
		return "  webhook {\n" + bot + "    secret_ref {\n      name = \"" + name + "\"\n      key  = \"s\"\n    }\n  }\n"
	}
	for _, tt := range []struct {
		name, src, summary, at string
	}{
		{"relative path", "ingest {\n  path = \"webhooks\"\n}\n" + app(""), "Invalid ingest path", `"webhooks"`},
		{"unclean path", "ingest {\n  path = \"/a/../b\"\n}\n" + app(""), "Invalid ingest path", `"/a/../b"`},
		{"negative settle", "ingest {\n  settle = \"-1s\"\n}\n" + app(""), "Invalid duration", `"-1s"`},
		{"negative min_interval", "ingest {\n  min_interval = \"-5m\"\n}\n" + app(""), "Invalid duration", `"-5m"`},
		{"audit too often", "ingest {\n  audit_every = \"30s\"\n}\n" + app(""), "Invalid duration", `"30s"`},
		{"empty bot", app(webhook("    bot = \" \"\n", "w")), "Invalid webhook bot", `" "`},
		{"bad secret name", app(webhook("", "Bad_Name")), "Invalid secret ref", `"Bad_Name"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			src := minimalApp + tt.src
			idx := strings.Index(src, tt.at)
			if idx < 0 {
				t.Fatalf("source lacks %q", tt.at)
			}
			line := strings.Count(src[:idx], "\n") + 1
			cfg, diags := config.Parse("ingest.hcl", []byte(src))
			if cfg != nil {
				t.Fatalf("Parse() accepted %s", tt.name)
			}
			for _, d := range diags.Diagnostics {
				if d.Subject != nil && d.Subject.Start.Line == line && d.Summary == tt.summary {
					return
				}
			}
			var b strings.Builder
			_, _ = diags.WriteTo(&b)
			t.Errorf("no %q at line %d; got:\n%s", tt.summary, line, b.String())
		})
	}
}
