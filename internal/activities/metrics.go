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

package activities

import "time"

// Metrics is what the activities report (DESIGN-0001 § Observability).
// internal/observability implements it over OpenTelemetry; NopMetrics
// drops everything.
type Metrics interface {
	// RunStarted and RunEnded move boopd_runs_active{profile}.
	RunStarted(profile string)
	RunEnded(profile string)
	// RunCompleted is boopd_runs_total{outcome,result,profile,sandboxed}
	// and the run's boopd_run_duration_seconds{profile,sandboxed},
	// boopd_run_pod_start_seconds{sandboxed} and
	// boopd_run_overhead_seconds{sandboxed}; a zero duration is not
	// recorded.
	RunCompleted(outcome, result, profile string, sandboxed bool, duration, podStart, overhead time.Duration)
	// PendingTimeout is boopd_run_pending_timeouts_total.
	PendingTimeout()
	// TokenMint is boopd_token_mints_total{outcome}.
	TokenMint(outcome string)
	// TokenRevocation is boopd_token_revocations_total{outcome}.
	TokenRevocation(outcome string)
	// ProbeCost adds one probe batch's spend to
	// boopd_discovery_probe_cost{installation,resource}.
	ProbeCost(installationID int64, resource string, cost int)
	// ProbeSplits adds n timed-out GraphQL slices that were halved to
	// boopd_discovery_probe_splits_total{installation}.
	ProbeSplits(installationID int64, n int)
	// ProbeFallbacks adds n repositories the GraphQL probe checked over
	// REST to boopd_discovery_probe_fallbacks_total{installation}.
	ProbeFallbacks(installationID int64, n int)
	// ConfigProbed counts one probed repository in
	// boopd_discovery_repos_total{installation,config}, config being
	// ConfigPresent, ConfigAbsent or ConfigUnparsed.
	ConfigProbed(installationID int64, config string)
	// FloorDetectionError is boopd_floor_detection_errors_total: a tree
	// listing for floor detection that failed.
	FloorDetectionError()
	// NeedsSandbox is boopd_runs_needs_sandbox_total{app}: a sandboxed
	// run that failed closed because the sandbox was missing.
	NeedsSandbox(app string)
	// FloorRaised is boopd_floor_raised_total{app,manager,mode}: one
	// plan the floor raised, or would have in report mode, counted once
	// per reason. manager is a floor manager, FloorUnknownManagers or
	// FloorTruncatedTree.
	FloorRaised(app, manager, mode string)
}

// Values of boopd_floor_raised_total's manager attribute when no floor
// manager was seen.
const (
	// FloorUnknownManagers: the repository's managers are not known yet
	// (its first run), and unknown counts as on the floor.
	FloorUnknownManagers = "unknown"
	// FloorTruncatedTree: the tree listing was truncated.
	FloorTruncatedTree = "truncated"
)

// Values of boopd_discovery_repos_total's config attribute.
const (
	// ConfigPresent: a config file was found and parsed; onboarded.
	ConfigPresent = "present"
	// ConfigAbsent: none of the config files is there; skipped.
	ConfigAbsent = "absent"
	// ConfigUnparsed: a config file was found but its extends could not
	// be read; onboarded without them.
	ConfigUnparsed = "unparsed"
)

// NopMetrics is a Metrics that records nothing.
type NopMetrics struct{}

// RunStarted implements Metrics.
func (NopMetrics) RunStarted(string) {}

// RunEnded implements Metrics.
func (NopMetrics) RunEnded(string) {}

// RunCompleted implements Metrics.
func (NopMetrics) RunCompleted(string, string, string, bool, time.Duration, time.Duration, time.Duration) {
}

// PendingTimeout implements Metrics.
func (NopMetrics) PendingTimeout() {}

// TokenMint implements Metrics.
func (NopMetrics) TokenMint(string) {}

// TokenRevocation implements Metrics.
func (NopMetrics) TokenRevocation(string) {}

// ProbeCost implements Metrics.
func (NopMetrics) ProbeCost(int64, string, int) {}

// ProbeSplits implements Metrics.
func (NopMetrics) ProbeSplits(int64, int) {}

// ProbeFallbacks implements Metrics.
func (NopMetrics) ProbeFallbacks(int64, int) {}

// ConfigProbed implements Metrics.
func (NopMetrics) ConfigProbed(int64, string) {}

// FloorDetectionError implements Metrics.
func (NopMetrics) FloorDetectionError() {}

// NeedsSandbox implements Metrics.
func (NopMetrics) NeedsSandbox(string) {}

// FloorRaised implements Metrics.
func (NopMetrics) FloorRaised(string, string, string) {}
