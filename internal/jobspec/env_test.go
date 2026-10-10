/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package jobspec_test

import (
	"encoding/json"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/donaldgifford/boop/internal/jobspec"
)

const (
	testImage   = "ghcr.io/renovatebot/renovate:44-full@sha256:0000000000000000000000000000000000000000000000000000000000000000"
	testPreset  = "github>boop-bot/renovate-config:default.json"
	testSlug    = "donaldgifford/boop"
	testSecret  = "run-101-0"
	testRedis   = "redis://:pw@boopd-redis:6379/0"
	testAuthor  = "boop-bot <boop-bot[bot]@users.noreply.github.com>"
	envConfig   = "RENOVATE_CONFIG"
	envEndpoint = "RENOVATE_ENDPOINT"
)

func baseInput() *jobspec.BuildInput {
	return &jobspec.BuildInput{
		Namespace: "boopd",
		Image:     testImage,
		App: jobspec.App{
			Endpoint:     "https://api.github.com",
			SharedPreset: testPreset,
			Global:       map[string]any{"prHourlyLimit": float64(2), "automerge": false},
			LogLevel:     "info",
			RedisURL:     testRedis,
			GitAuthor:    testAuthor,
		},
		Repo:        jobspec.Repo{ID: 101, Slug: testSlug, DefaultBranch: "main", InstallationID: 55},
		Profile:     jobspec.Profile{Name: "baseline"},
		TokenSecret: testSecret,
		WorkflowID:  "repo/github/101",
		RunID:       "0199c0de-0000-7000-8000-000000000000",
	}
}

func TestBuildEnv_TableRows(t *testing.T) {
	t.Parallel()
	env, err := jobspec.BuildEnv(baseInput())
	if err != nil {
		t.Fatalf("BuildEnv err = %v", err)
	}

	want := map[string]string{
		"RENOVATE_PLATFORM":             "github",
		"LOG_LEVEL":                     "info",
		"LOG_FORMAT":                    "json",
		"RENOVATE_AUTODISCOVER":         "false",
		"RENOVATE_REPOSITORIES":         testSlug,
		"RENOVATE_REQUIRE_CONFIG":       "required",
		"RENOVATE_ONBOARDING":           "false",
		"RENOVATE_BASE_DIR":             "/work/base",
		"RENOVATE_CACHE_DIR":            "/work/cache",
		"RENOVATE_BINARY_SOURCE":        "global",
		"RENOVATE_REDIS_URL":            testRedis,
		"RENOVATE_REPORT_TYPE":          "logging",
		"RENOVATE_EXIT_CODE_FOR_ERRORS": "true",
		"RENOVATE_GIT_AUTHOR":           testAuthor,
		"HOME":                          "/work/home",
		"TMPDIR":                        "/tmp",
	}
	for name, value := range want {
		if got := envValue(env, name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}

	token := envEntry(env, "RENOVATE_TOKEN")
	if token == nil || token.ValueFrom == nil || token.ValueFrom.SecretKeyRef == nil {
		t.Fatalf("RENOVATE_TOKEN missing or not a SecretKeyRef: %+v", token)
	}
	if token.ValueFrom.SecretKeyRef.Name != testSecret || token.ValueFrom.SecretKeyRef.Key != jobspec.TokenKey {
		t.Errorf("RENOVATE_TOKEN SecretKeyRef = %+v", token.ValueFrom.SecretKeyRef)
	}
	if envEntry(env, envEndpoint) != nil {
		t.Error("RENOVATE_ENDPOINT must be unset for api.github.com")
	}
}

func TestBuildEnv_Order(t *testing.T) {
	t.Parallel()
	env, err := jobspec.BuildEnv(baseInput())
	if err != nil {
		t.Fatalf("BuildEnv err = %v", err)
	}
	want := []string{
		"RENOVATE_PLATFORM", "LOG_LEVEL", "LOG_FORMAT",
		"RENOVATE_TOKEN",
		"RENOVATE_AUTODISCOVER", "RENOVATE_REPOSITORIES", "RENOVATE_REQUIRE_CONFIG", "RENOVATE_ONBOARDING",
		"RENOVATE_BASE_DIR", "RENOVATE_CACHE_DIR", "RENOVATE_BINARY_SOURCE", "RENOVATE_REDIS_URL",
		"RENOVATE_REPORT_TYPE", "RENOVATE_EXIT_CODE_FOR_ERRORS", "RENOVATE_GIT_AUTHOR",
		envConfig, "HOME", "TMPDIR",
	}
	if got := envNames(env); !equalStrings(got, want) {
		t.Errorf("env order:\n got = %v\nwant = %v", got, want)
	}
}

func TestBuildEnv_Endpoint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		endpoint string
		want     string
	}{
		{"empty", "", ""},
		{"github.com", "https://api.github.com", ""},
		{"github.com trailing slash", "https://api.github.com/", ""},
		{"GHES", "https://github.example.com/api/v3", "https://github.example.com/api/v3"},
		{"GHES trailing slash", "https://github.example.com/api/v3/", "https://github.example.com/api/v3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := baseInput()
			in.App.Endpoint = tt.endpoint
			env, err := jobspec.BuildEnv(in)
			if err != nil {
				t.Fatalf("BuildEnv err = %v", err)
			}
			if got := envValue(env, envEndpoint); got != tt.want {
				t.Errorf("RENOVATE_ENDPOINT = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildEnv_RenovateConfigMerge(t *testing.T) {
	t.Parallel()
	in := baseInput()
	in.App.Global = map[string]any{
		"extends":       []any{"config:recommended"},
		"prHourlyLimit": float64(2),
		"automerge":     false, // explicit false must survive (renovate-operator INV-0005)
		"dryRun":        "lookup",
	}
	in.Profile = jobspec.Profile{
		Name: "python",
		Renovate: map[string]any{
			"prHourlyLimit":      float64(1), // profile wins over global
			"customEnvVariables": map[string]any{"PIP_ONLY_BINARY": ":all:", "UV_NO_BUILD": "1"},
		},
	}
	in.DryRun = "full" // run input wins over global dryRun

	cfg := parsedConfig(t, in)

	extends, _ := cfg["extends"].([]any)
	if len(extends) != 2 || extends[0] != testPreset || extends[1] != "config:recommended" {
		t.Errorf("extends = %v, want preset prepended", extends)
	}
	if got := cfg["automerge"]; got != false {
		t.Errorf("automerge = %v, want explicit false", got)
	}
	if got := cfg["prHourlyLimit"]; got != float64(1) {
		t.Errorf("prHourlyLimit = %v, want 1 (profile override merged last)", got)
	}
	if got := cfg["dryRun"]; got != "full" {
		t.Errorf("dryRun = %v, want full", got)
	}
	vars, _ := cfg["customEnvVariables"].(map[string]any)
	if vars["PIP_ONLY_BINARY"] != ":all:" || vars["UV_NO_BUILD"] != "1" {
		t.Errorf("customEnvVariables = %v", vars)
	}
}

func TestBuildEnv_ConfigOmittedWhenEmpty(t *testing.T) {
	t.Parallel()
	in := baseInput()
	in.App.SharedPreset = ""
	in.App.Global = nil
	env, err := jobspec.BuildEnv(in)
	if err != nil {
		t.Fatalf("BuildEnv err = %v", err)
	}
	if envEntry(env, envConfig) != nil {
		t.Error("RENOVATE_CONFIG should be omitted when there is nothing to pass")
	}
}

func TestBuildEnv_LogLevelDefaultsToInfo(t *testing.T) {
	t.Parallel()
	in := baseInput()
	in.App.LogLevel = ""
	env, err := jobspec.BuildEnv(in)
	if err != nil {
		t.Fatalf("BuildEnv err = %v", err)
	}
	if got := envValue(env, "LOG_LEVEL"); got != "info" {
		t.Errorf("LOG_LEVEL = %q, want info", got)
	}
}

func TestBuildEnv_OptionalValuesOmitted(t *testing.T) {
	t.Parallel()
	in := baseInput()
	in.App.RedisURL = ""
	in.App.GitAuthor = ""
	env, err := jobspec.BuildEnv(in)
	if err != nil {
		t.Fatalf("BuildEnv err = %v", err)
	}
	for _, name := range []string{"RENOVATE_REDIS_URL", "RENOVATE_GIT_AUTHOR"} {
		if envEntry(env, name) != nil {
			t.Errorf("%s should be omitted when unset", name)
		}
	}
}

func TestBuildEnv_Preset(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		preset  string
		wantErr bool
	}{
		{"default file", "github>boop-bot/renovate-config", false},
		{"json file", "github>boop-bot/renovate-config:default.json", false},
		{"json file with ref", "github>boop-bot/renovate-config:python.json#v1", false},
		{"subpreset", "github>boop-bot/renovate-config:python.json/strict", true},
		{"json5", "github>boop-bot/renovate-config:default.json5", true},
		{"json5 with ref", "github>boop-bot/renovate-config:default.json5#main", true},
		{"non-github preset untouched", "local>boop-bot/renovate-config:default.json5", true},
		{"builtin preset", "config:recommended", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := baseInput()
			in.App.SharedPreset = tt.preset
			_, err := jobspec.BuildEnv(in)
			if tt.wantErr && !errors.Is(err, jobspec.ErrInvalidPreset) {
				t.Errorf("err = %v, want ErrInvalidPreset", err)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("err = %v, want nil", err)
			}
		})
	}
}

func TestBuildEnv_RejectsForbiddenOptions(t *testing.T) {
	t.Parallel()
	forbidden := []string{
		"exposeAllEnv", "allowScripts", "allowedCommands", "logLevel",
		"platform", "endpoint", "token", "autodiscover", "repositories",
		"requireConfig", "onboarding", "baseDir", "cacheDir", "binarySource",
		"redisUrl", "reportType", "reportPath", "exitCodeForErrors", "gitAuthor",
	}
	for _, key := range forbidden {
		t.Run("global."+key, func(t *testing.T) {
			t.Parallel()
			in := baseInput()
			in.App.Global = map[string]any{key: false}
			if _, err := jobspec.BuildEnv(in); !errors.Is(err, jobspec.ErrForbiddenOption) {
				t.Errorf("err = %v, want ErrForbiddenOption", err)
			}
		})
		t.Run("profile."+key, func(t *testing.T) {
			t.Parallel()
			in := baseInput()
			in.Profile.Renovate = map[string]any{key: false}
			if _, err := jobspec.BuildEnv(in); !errors.Is(err, jobspec.ErrForbiddenOption) {
				t.Errorf("err = %v, want ErrForbiddenOption", err)
			}
		})
	}
}

func TestBuildEnv_CustomEnvVariablesShape(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value any
	}{
		{"not a map", "PIP_ONLY_BINARY=:all:"},
		{"non-string value", map[string]any{"UV_NO_BUILD": true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := baseInput()
			in.Profile.Renovate = map[string]any{"customEnvVariables": tt.value}
			if _, err := jobspec.BuildEnv(in); !errors.Is(err, jobspec.ErrInvalidOption) {
				t.Errorf("err = %v, want ErrInvalidOption", err)
			}
		})
	}
}

// helpers

func parsedConfig(t *testing.T, in *jobspec.BuildInput) map[string]any {
	t.Helper()
	env, err := jobspec.BuildEnv(in)
	if err != nil {
		t.Fatalf("BuildEnv err = %v", err)
	}
	raw := envValue(env, envConfig)
	var cfg map[string]any
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("RENOVATE_CONFIG not JSON: %v (%q)", err, raw)
	}
	return cfg
}

func envNames(env []corev1.EnvVar) []string {
	out := make([]string, len(env))
	for i, e := range env {
		out[i] = e.Name
	}
	return out
}

func envValue(env []corev1.EnvVar, name string) string {
	for _, e := range env {
		if e.Name == name {
			return e.Value
		}
	}
	return ""
}

func envEntry(env []corev1.EnvVar, name string) *corev1.EnvVar {
	for i := range env {
		if env[i].Name == name {
			return &env[i]
		}
	}
	return nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
