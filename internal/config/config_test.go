package config_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/platform"
)

const examplePath = "../../examples/boopd.hcl"

func TestLoad_Example(t *testing.T) {
	t.Parallel()
	cfg, diags := config.Load(examplePath)
	if diags.HasErrors() {
		var b strings.Builder
		_, _ = diags.WriteTo(&b)
		t.Fatalf("Load(%s) diagnostics:\n%s", examplePath, b.String())
	}
	if cfg.Renovate.DryRun != "full" || !slices.Equal(cfg.Renovate.ConfigPaths, platform.DefaultConfigPaths) {
		t.Errorf("renovate = %+v, want dry_run full and the default config paths", cfg.Renovate)
	}
	if cfg.Runs.PendingTimeout != 10*time.Minute || cfg.Runs.Namespace != "boopd" || cfg.Runs.MaxConcurrent != 20 {
		t.Errorf("runs = %+v, want boopd/10m/20", cfg.Runs)
	}
	if len(cfg.Apps) != 1 {
		t.Fatalf("apps = %d, want 1", len(cfg.Apps))
	}
	app := cfg.Apps[0]
	if app.Name != "boop-bot" || app.AppID != 123456 || app.Cadence != 24*time.Hour ||
		app.Discovery.Every != 6*time.Hour || app.Discovery.Probe != config.ProbeGraphQL {
		t.Errorf("app = %+v, want the example's boop-bot", app)
	}
	if got := cfg.Profiles["python"]; got == nil || *got.Pod.RuntimeClassName != "gvisor" {
		t.Errorf("profile python = %+v, want gvisor", got)
	}
}

func TestParse_DiagnosticsCarryPosition(t *testing.T) {
	t.Parallel()
	src := []byte(`renovate {
  image = "x"
  imagee = "typo"
}
`)
	_, diags := config.Parse("bad.hcl", src)
	if !diags.HasErrors() {
		t.Fatal("Parse() with an unknown attribute: no error")
	}
	var b strings.Builder
	_, _ = diags.WriteTo(&b)
	if !strings.Contains(b.String(), "bad.hcl:3:") {
		t.Errorf("diagnostics lack bad.hcl:3:\n%s", b.String())
	}
}

func TestResolver_FromExample(t *testing.T) {
	t.Parallel()
	cfg, diags := config.Load(examplePath)
	if diags.HasErrors() {
		t.Fatal(diags.Error())
	}
	r, err := cfg.Resolver()
	if err != nil {
		t.Fatalf("Resolver() = %v", err)
	}
	if got := r.Resolve(nil, []string{"poetry"}); got != "strict" {
		t.Errorf("Resolve(nil, poetry) = %q, want strict", got)
	}
	if got := r.Resolve([]string{"github>boop-bot/renovate-config:python"}, nil); got != "python" {
		t.Errorf("Resolve(python preset) = %q, want python", got)
	}
}
