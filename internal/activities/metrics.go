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
	// RunCompleted is boopd_runs_total{outcome,result,profile} and the
	// run's boopd_run_duration_seconds, boopd_run_pod_start_seconds and
	// boopd_run_overhead_seconds; a zero duration is not recorded.
	RunCompleted(outcome, result, profile string, duration, podStart, overhead time.Duration)
	// PendingTimeout is boopd_run_pending_timeouts_total.
	PendingTimeout()
	// TokenMint is boopd_token_mints_total{outcome}.
	TokenMint(outcome string)
	// TokenRevocation is boopd_token_revocations_total{outcome}.
	TokenRevocation(outcome string)
	// ProbeCost adds one probe batch's spend to
	// boopd_discovery_probe_cost{installation,resource}.
	ProbeCost(installationID int64, resource string, cost int)
	// ConfigProbed counts one probed repository in
	// boopd_discovery_repos_total{installation,config}, config being
	// ConfigPresent, ConfigAbsent or ConfigUnparsed.
	ConfigProbed(installationID int64, config string)
}

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
func (NopMetrics) RunCompleted(string, string, string, time.Duration, time.Duration, time.Duration) {}

// PendingTimeout implements Metrics.
func (NopMetrics) PendingTimeout() {}

// TokenMint implements Metrics.
func (NopMetrics) TokenMint(string) {}

// TokenRevocation implements Metrics.
func (NopMetrics) TokenRevocation(string) {}

// ProbeCost implements Metrics.
func (NopMetrics) ProbeCost(int64, string, int) {}

// ConfigProbed implements Metrics.
func (NopMetrics) ConfigProbed(int64, string) {}
