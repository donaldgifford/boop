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

// Package unsafe is boopd's record of which Renovate managers run
// repository code (DESIGN-0005, IMPL-0006 Phase 2).
//
// Floor is policy, kept by hand: the managers that execute repository
// code with no Renovate gate, so a repository with one runs only in the
// sandbox. Gated, the manager list and each manager's default
// managerFilePatterns are facts about the chart's pinned Renovate image,
// generated into zz_generated.go by `just generate-unsafe`. The drift
// test (build tag renovate) fails when the pinned image disagrees with
// the generated file, so a Renovate upgrade that adds an executing
// manager or gate is a decision, not a surprise.
package unsafe

import "slices"

// floor is the ungated managers that execute repository code
// (DESIGN-0005 § Background, the exec table; upstream tracking in
// DESIGN-0005 § Keeping up with Renovate).
var floor = []string{
	"bundler",
	"cocoapods",
	"conan",
	"maven-wrapper",
	"mix",
	"nuget",
	"pep621",
	"pip-compile",
	"pipenv",
	"poetry",
}

// Floor returns the floor managers, sorted.
func Floor() []string { return slices.Clone(floor) }

// Gated returns Renovate's allowedUnsafeExecutions values, sorted.
func Gated() []string { return slices.Clone(gated) }

// IsGated reports whether v is an allowedUnsafeExecutions value.
func IsGated(v string) bool { return slices.Contains(gated, v) }

// IsManager reports whether name is a Renovate manager.
func IsManager(name string) bool { return slices.Contains(managers, name) }

// ExecManagers returns the managers whose artifact code can run a
// command, sorted. Every floor manager is one; the rest are gated or
// run only tools boopd's image provides.
func ExecManagers() []string { return slices.Clone(execManagers) }

// DefaultFilePatterns returns manager's default managerFilePatterns.
// ok is false when Renovate gives it none; an empty list with ok true is
// a manager that matches nothing until configured (pip-compile).
func DefaultFilePatterns(manager string) (patterns []string, ok bool) {
	p, ok := defaultFilePatterns[manager]
	return slices.Clone(p), ok
}
