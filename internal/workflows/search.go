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

import "go.temporal.io/sdk/temporal"

// Search attributes RepoWorkflow upserts (DESIGN-0001 § Observability,
// Runs in flight), so the Temporal UI and CLI can list and filter
// repositories without a boopd API. The worker registers them on the
// namespace at start.
var (
	SearchInstallationID = temporal.NewSearchAttributeKeyInt64("InstallationID")
	SearchProfile        = temporal.NewSearchAttributeKeyKeyword("Profile")
	SearchPhase          = temporal.NewSearchAttributeKeyKeyword("Phase")
	SearchLastOutcome    = temporal.NewSearchAttributeKeyKeyword("LastOutcome")
	SearchNextDue        = temporal.NewSearchAttributeKeyTime("NextDue")
)

// SearchAttributes is every key above, for registration.
var SearchAttributes = []temporal.SearchAttributeKey{
	SearchInstallationID, SearchProfile, SearchPhase, SearchLastOutcome, SearchNextDue,
}

// Phase values (the RepoWorkflow state diagram).
const (
	PhaseWaiting   = "waiting"
	PhaseChecking  = "checking"
	PhaseAcquiring = "acquiring"
	PhaseRunning   = "running"
	PhaseBackoff   = "backoff"
)
