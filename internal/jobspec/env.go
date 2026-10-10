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

package jobspec

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// Renovate env var names (DESIGN-0001 § Process builder).
const (
	envPlatform          = "RENOVATE_PLATFORM"
	envEndpoint          = "RENOVATE_ENDPOINT"
	envToken             = "RENOVATE_TOKEN"
	envAutodiscover      = "RENOVATE_AUTODISCOVER"
	envRepositories      = "RENOVATE_REPOSITORIES"
	envRequireConfig     = "RENOVATE_REQUIRE_CONFIG"
	envOnboarding        = "RENOVATE_ONBOARDING"
	envBaseDir           = "RENOVATE_BASE_DIR"
	envCacheDir          = "RENOVATE_CACHE_DIR"
	envBinarySource      = "RENOVATE_BINARY_SOURCE"
	envRedisURL          = "RENOVATE_REDIS_URL"
	envReportType        = "RENOVATE_REPORT_TYPE"
	envExitCodeForErrors = "RENOVATE_EXIT_CODE_FOR_ERRORS"
	envGitAuthor         = "RENOVATE_GIT_AUTHOR"
	envConfig            = "RENOVATE_CONFIG"
	envLogLevel          = "LOG_LEVEL"
	envLogFormat         = "LOG_FORMAT"
	envHome              = "HOME"
	envTmpDir            = "TMPDIR"

	platformGitHub   = "github"
	gitHubAPI        = "https://api.github.com"
	defaultLogLevel  = "info"
	logFormatJSON    = "json"
	workDir          = "/work"
	tmpDir           = "/tmp"
	baseDir          = workDir + "/base"
	cacheDir         = workDir + "/cache"
	homeDir          = workDir + "/home"
	reportTypeLog    = "logging"
	binarySourceGlob = "global"

	optExtends            = "extends"
	optDryRun             = "dryRun"
	optCustomEnvVariables = "customEnvVariables"
	presetGitHubPrefix    = "github>"
	presetJSONSuffix      = ".json"
	presetJSON5Suffix     = ".json5"
)

// forbiddenOptions are rejected in global config and in profile overrides:
// Renovate's script and env controls (ADR-0003 rules, carried over), and
// options the builder owns through the environment, so there is one source
// for each.
var forbiddenOptions = []string{
	"exposeAllEnv", "allowScripts", "allowedCommands", "logLevel",
	"platform", "endpoint", "token", "autodiscover", "repositories",
	"requireConfig", "onboarding", "baseDir", "cacheDir", "binarySource",
	"redisUrl", "reportType", "reportPath", "exitCodeForErrors", "gitAuthor",
}

// BuildEnv assembles the container's environment in the order of the table
// in DESIGN-0001 § Process builder. Nothing from the worker's own
// environment is consulted; the pod gets exactly this list.
func BuildEnv(in *BuildInput) ([]corev1.EnvVar, error) {
	cfg, err := renovateConfig(in)
	if err != nil {
		return nil, err
	}

	logLevel := in.App.LogLevel
	if logLevel == "" {
		logLevel = defaultLogLevel
	}

	out := make([]corev1.EnvVar, 0, 20)
	out = append(out,
		corev1.EnvVar{Name: envPlatform, Value: platformGitHub},
		corev1.EnvVar{Name: envLogLevel, Value: logLevel},
		corev1.EnvVar{Name: envLogFormat, Value: logFormatJSON},
	)
	if ep := endpointFor(in.App.Endpoint); ep != "" {
		out = append(out, corev1.EnvVar{Name: envEndpoint, Value: ep})
	}
	out = append(out,
		corev1.EnvVar{
			Name: envToken,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: in.TokenSecret},
					Key:                  TokenKey,
				},
			},
		},
		corev1.EnvVar{Name: envAutodiscover, Value: "false"},
		corev1.EnvVar{Name: envRepositories, Value: in.Repo.Slug},
		corev1.EnvVar{Name: envRequireConfig, Value: "required"},
		corev1.EnvVar{Name: envOnboarding, Value: "false"},
		corev1.EnvVar{Name: envBaseDir, Value: baseDir},
		corev1.EnvVar{Name: envCacheDir, Value: cacheDir},
		corev1.EnvVar{Name: envBinarySource, Value: binarySourceGlob},
	)
	if in.App.RedisURL != "" {
		out = append(out, corev1.EnvVar{Name: envRedisURL, Value: in.App.RedisURL})
	}
	out = append(out,
		corev1.EnvVar{Name: envReportType, Value: reportTypeLog},
		corev1.EnvVar{Name: envExitCodeForErrors, Value: "true"},
	)
	if in.App.GitAuthor != "" {
		out = append(out, corev1.EnvVar{Name: envGitAuthor, Value: in.App.GitAuthor})
	}
	if cfg != "" {
		out = append(out, corev1.EnvVar{Name: envConfig, Value: cfg})
	}
	out = append(out,
		corev1.EnvVar{Name: envHome, Value: homeDir},
		corev1.EnvVar{Name: envTmpDir, Value: tmpDir},
	)
	return out, nil
}

// endpointFor returns the RENOVATE_ENDPOINT value, or "" when the endpoint
// is github.com, where Renovate's default is right and an explicit value
// breaks the API prefix (renovate-operator INV-0004 Observation 5).
func endpointFor(endpoint string) string {
	ep := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if ep == "" || ep == gitHubAPI {
		return ""
	}
	return ep
}

// renovateConfig merges, in order, the App's global config, the profile's
// overrides and the run's dryRun, then prepends the shared preset to
// extends. Later entries win. Returns "" when there is nothing to pass.
func renovateConfig(in *BuildInput) (string, error) {
	if err := ValidatePreset(in.App.SharedPreset); err != nil {
		return "", err
	}
	if err := ValidateOptions("global", in.App.Global); err != nil {
		return "", err
	}
	if err := ValidateOptions("profile "+in.Profile.Name, in.Profile.Renovate); err != nil {
		return "", err
	}

	merged := make(map[string]any, len(in.App.Global)+len(in.Profile.Renovate)+2)
	maps.Copy(merged, in.App.Global)
	maps.Copy(merged, in.Profile.Renovate)
	if in.DryRun != "" {
		merged[optDryRun] = in.DryRun
	}
	if in.App.SharedPreset != "" {
		var extends []any
		if existing, ok := merged[optExtends].([]any); ok {
			extends = existing
		}
		merged[optExtends] = append([]any{in.App.SharedPreset}, extends...)
	}
	if len(merged) == 0 {
		return "", nil
	}

	out, err := json.Marshal(merged)
	if err != nil {
		return "", fmt.Errorf("jobspec: marshal %s: %w", envConfig, err)
	}
	return string(out), nil
}

// ValidatePreset enforces the .json rule for GitHub-hosted presets: Renovate
// fetches "github>owner/repo:file" as file.json, and a .json5 name fails at
// run time with a misleading "preset not found". The config package calls
// it at load time; BuildEnv calls it again at build time.
func ValidatePreset(preset string) error {
	if preset == "" {
		return nil
	}
	if strings.HasSuffix(preset, presetJSON5Suffix) || strings.Contains(preset, presetJSON5Suffix+"#") {
		return fmt.Errorf("%w: %q", ErrInvalidPreset, preset)
	}
	if !strings.HasPrefix(preset, presetGitHubPrefix) {
		return nil
	}
	ref := strings.TrimPrefix(preset, presetGitHubPrefix)
	if i := strings.IndexByte(ref, '#'); i >= 0 {
		ref = ref[:i]
	}
	_, file, hasFile := strings.Cut(ref, ":")
	if hasFile && !strings.HasSuffix(file, presetJSONSuffix) {
		return fmt.Errorf("%w: %q", ErrInvalidPreset, preset)
	}
	return nil
}

// ValidateOptions rejects options the builder owns or Renovate's script and
// env controls, and checks the shape of customEnvVariables. where names
// the options' origin in the error ("global", "profile python"). The
// config package calls it at load time; BuildEnv calls it again.
func ValidateOptions(where string, opts map[string]any) error {
	for _, key := range forbiddenOptions {
		if _, ok := opts[key]; ok {
			return fmt.Errorf("%w: %s.%s", ErrForbiddenOption, where, key)
		}
	}
	raw, ok := opts[optCustomEnvVariables]
	if !ok {
		return nil
	}
	vars, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("%w: %s.%s must be a map of strings", ErrInvalidOption, where, optCustomEnvVariables)
	}
	for name, value := range vars {
		if _, isString := value.(string); !isString {
			return fmt.Errorf("%w: %s.%s.%s must be a string", ErrInvalidOption, where, optCustomEnvVariables, name)
		}
	}
	return nil
}
