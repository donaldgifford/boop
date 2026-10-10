package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRun_ReplaysWithSlugAndPadding(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	cfg := &config{fixture: "live", hangAfter: -1, slug: "o/r", reportBytes: 1 << 20}
	if err := run(cfg, &out); err != nil {
		t.Fatalf("run() = %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	last := lines[len(lines)-1]
	if len(last) < 1<<20 {
		t.Errorf("report line = %d bytes, want >= 1 MiB", len(last))
	}
	var l map[string]any
	if err := json.Unmarshal([]byte(last), &l); err != nil {
		t.Errorf("padded report line is not JSON: %v", err)
	}
	if strings.Contains(out.String(), fixtureSlug) || !strings.Contains(out.String(), `"repository":"o/r"`) {
		t.Error("run() did not replace the fixture slug")
	}
}

func TestMarkers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var first, second strings.Builder
	for _, out := range []*strings.Builder{&first, &second} {
		if err := run(&config{fixture: "dryrun", hangAfter: -1, markerDirs: []string{dir}}, out); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(first.String(), `"found":null`) {
		t.Errorf("first run saw markers: %s", strings.SplitN(first.String(), "\n", 2)[0])
	}
	if !strings.Contains(second.String(), `"found":["stub-marker-`) {
		t.Errorf("second run in the same dir saw none: %s", strings.SplitN(second.String(), "\n", 2)[0])
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Parallel()
	env := map[string]string{"STUB_DELAY": "10ms", "STUB_EXIT_CODE": "3", "STUB_HANG_AFTER": "2", "STUB_MARKER_DIRS": "/work,/tmp"}
	cfg, err := configFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.exitCode != 3 || cfg.hangAfter != 2 || cfg.delay.Milliseconds() != 10 || len(cfg.markerDirs) != 2 {
		t.Errorf("configFromEnv() = %+v", cfg)
	}
	if _, err := configFromEnv(func(k string) string { return map[string]string{"STUB_EXIT_CODE": "x"}[k] }); err == nil {
		t.Error("configFromEnv(bad exit code) err = nil")
	}
}

func TestLookup_CustomEnvVariables(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		"STUB_EXIT_CODE":  "4",
		"RENOVATE_CONFIG": `{"extends":["x"],"customEnvVariables":{"STUB_EXIT_CODE":"9","STUB_HANG_AFTER":"6","STUB_DROP_REPORT":"true"}}`,
	}
	cfg, err := configFromEnv(lookup(func(k string) string { return env[k] }))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.exitCode != 4 || cfg.hangAfter != 6 || !cfg.dropReport {
		t.Errorf("configFromEnv() = %+v, want the environment first, then customEnvVariables", cfg)
	}
}

func TestRun_DropReport(t *testing.T) {
	t.Parallel()
	var out strings.Builder
	if err := run(&config{fixture: "live", hangAfter: -1, dropReport: true}, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Printing report") || !strings.Contains(out.String(), "Repository finished") {
		t.Errorf("output kept the report or lost the finished line:\n%s", out.String())
	}
}
