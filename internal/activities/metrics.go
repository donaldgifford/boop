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

// Metrics is what the activities report. Phase 6's registry implements
// it with the boopd_* instruments; NopMetrics drops everything.
type Metrics interface {
	// RunCompleted is boopd_runs_total{outcome} and the run's duration,
	// pod-start and overhead histograms.
	RunCompleted(outcome string, duration, podStart, overhead time.Duration)
	// PendingTimeout is boopd_run_pending_timeouts_total.
	PendingTimeout()
	// TokenMint is boopd_token_mints_total{result}.
	TokenMint(result string)
	// TokenRevocation is boopd_token_revocations_total{result}.
	TokenRevocation(result string)
	// ProbeCost records boopd_discovery_probe_cost for one query batch.
	ProbeCost(app string, cost int)
}

// NopMetrics is a Metrics that records nothing.
type NopMetrics struct{}

// RunCompleted implements Metrics.
func (NopMetrics) RunCompleted(string, time.Duration, time.Duration, time.Duration) {}

// PendingTimeout implements Metrics.
func (NopMetrics) PendingTimeout() {}

// TokenMint implements Metrics.
func (NopMetrics) TokenMint(string) {}

// TokenRevocation implements Metrics.
func (NopMetrics) TokenRevocation(string) {}

// ProbeCost implements Metrics.
func (NopMetrics) ProbeCost(string, int) {}
