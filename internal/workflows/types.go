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

package workflows

import "time"

// Payloads carry identifiers, counts, readings and timestamps, never
// repository content or tokens (DESIGN-0001 § Data Model).

// Priority is a Temporal task priority key. Lower runs first; 3 is the
// server's default. Priority orders tasks within the one task queue.
type Priority int

// Priorities by trigger (DESIGN-0001 § RepoWorkflow, Priority).
const (
	// PriorityHigh is a recheck a person asked for: ahead of every
	// scheduled run.
	PriorityHigh Priority = 1
	// PriorityNormal is a scheduled run.
	PriorityNormal Priority = 3
)

// The /rate_limit resources the budget tracks (DESIGN-0001 OQ6). The
// top-level `rate` object is deprecated, and the other resources
// (search, code_scanning_upload, ...) Renovate does not spend.
const (
	ResourceCore    = "core"
	ResourceGraphQL = "graphql"
)

// TrackedResources is the fixed order the budget walks its resources
// in, so workflow code never ranges over a map where order could show.
var TrackedResources = []string{ResourceCore, ResourceGraphQL}

// Reading is one resource's values from GET /rate_limit.
type Reading struct {
	Limit     int
	Remaining int
	Reset     time.Time
}

// Readings is one GET /rate_limit: the tracked resources and when the
// endpoint was read. ReadRateLimit returns it, and RunRenovate's result
// carries one from before and one from after the run.
type Readings struct {
	Resources  map[string]Reading
	ObservedAt time.Time
}

// ReadRateLimitInput is the ReadRateLimit activity input.
type ReadRateLimitInput struct {
	InstallationID int64
}

// AcquireInput is the AcquireBudget activity input. UpdateID makes a
// retried acquire return the first attempt's answer instead of taking a
// second lease.
type AcquireInput struct {
	InstallationID int64
	UpdateID       string
	Request        AcquireRequest
}
