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

// Package activities holds boopd's Temporal activities: everything that
// reaches GitHub, Kubernetes or the Temporal client (DESIGN-0001 §
// Topology and packages). Workflows execute them by the names in the
// workflows package, so neither side imports the other's
// implementation.
//
// Activities (New, Register) holds ListInstallations,
// DiscoverInstallation, CheckRepo, ReadRateLimit and RunRenovate; Budget
// holds AcquireBudget. GitHub is reached through the consumer-side
// GitHub, AppAPI and InstallationAPI interfaces, with cached real
// clients by default (NewGitHub). RunRenovate drives one Kubernetes Job
// per run through internal/kube and classifies the run with the
// design's table (classify.go).
package activities

import "go.temporal.io/sdk/activity"

// Registry is the part of worker.Worker activities register on.
type Registry interface {
	RegisterActivityWithOptions(a any, options activity.RegisterOptions)
}
