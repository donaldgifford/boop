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
		{name: "version", args: []string{"version"}, wantCode: 0, wantStdout: "boop dev"},
		{name: "usage", args: []string{"config"}, wantCode: 2, wantStderr: "usage:"},
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
