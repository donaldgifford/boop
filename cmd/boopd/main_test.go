package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const example = "../../examples/boopd.hcl"

func TestRun_ConfigValidate(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile(example)
	if err != nil {
		t.Fatal(err)
	}
	misspelled := filepath.Join(t.TempDir(), "boopd.hcl")
	bad := strings.Replace(string(src), "skip_forks    = true", "skip_fork = true", 1)
	if err := os.WriteFile(misspelled, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	idx := strings.Index(bad, "skip_fork = true")
	if idx < 0 {
		t.Fatal("example lacks skip_forks")
	}
	line := strings.Count(bad[:idx], "\n") + 1

	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "example", args: []string{"config", "validate", example}, wantCode: 0, wantStdout: "ok"},
		{name: "misspelled", args: []string{"config", "validate", misspelled}, wantCode: 1, wantStderr: misspelled + ":" + strconv.Itoa(line) + ":"},
		{name: "missing file", args: []string{"config", "validate", "/nonexistent.hcl"}, wantCode: 1, wantStderr: "Cannot read config file"},
		{name: "version", args: []string{"version"}, wantCode: 0, wantStdout: "boopd dev"},
		{name: "usage", args: []string{"config"}, wantCode: 2, wantStderr: "usage:"},
		{name: "worker bad flag", args: []string{"worker", "--nope"}, wantCode: 2, wantStderr: "flag provided but not defined"},
		{name: "worker missing config", args: []string{"worker", "--config", "/nonexistent.hcl"}, wantCode: 1, wantStderr: "Cannot read config file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr strings.Builder
			code := run(tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("run(%q) = %d, want %d; stderr:\n%s", tt.args, code, tt.wantCode, stderr.String())
			}
			if !strings.Contains(stdout.String(), tt.wantStdout) {
				t.Errorf("run(%q) stdout = %q, want %q", tt.args, stdout.String(), tt.wantStdout)
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("run(%q) stderr = %q, want %q", tt.args, stderr.String(), tt.wantStderr)
			}
		})
	}
}

// exampleWith writes the example config with edit applied and returns
// its path.
func exampleWith(t *testing.T, edit func(string) string) string {
	t.Helper()
	src, err := os.ReadFile(example)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "boopd.hcl")
	if err := os.WriteFile(path, []byte(edit(string(src))), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// withoutWebhook drops the example's webhook block.
func withoutWebhook(src string) string {
	start := strings.Index(src, "  webhook {")
	end := strings.Index(src[start:], "\n  }\n")
	return src[:start] + src[start+end+len("\n  }\n"):]
}

func TestRun_Ingest(t *testing.T) {
	t.Parallel()
	noWebhook := exampleWith(t, withoutWebhook)
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStderr string
	}{
		{name: "usage", args: []string{"ingest", "extra"}, wantCode: 2},
		{name: "bad flag", args: []string{"ingest", "--nope"}, wantCode: 2, wantStderr: "flag provided but not defined"},
		{name: "missing config", args: []string{"ingest", "--config", "/nonexistent.hcl"}, wantCode: 1, wantStderr: "Cannot read config file"},
		{name: "no webhook app", args: []string{"ingest", "--config", noWebhook}, wantCode: 1, wantStderr: "no app has a webhook block"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr strings.Builder
			if code := run(tt.args, &stdout, &stderr); code != tt.wantCode {
				t.Errorf("run(%q) = %d, want %d; stderr:\n%s", tt.args, code, tt.wantCode, stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.wantStderr) {
				t.Errorf("run(%q) stderr = %q, want %q", tt.args, stderr.String(), tt.wantStderr)
			}
		})
	}
}
