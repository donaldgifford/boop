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

package dashboard_test

import (
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/donaldgifford/boop/internal/renovate/dashboard"
)

// small reads testdata/unknown-marker.md: an approval, a ticked global
// box, an open PR, a marker this package does not know, and the manual
// job box after the closing rule.
func small(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/unknown-marker.md")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParse(t *testing.T) {
	t.Parallel()
	snap, err := dashboard.Parse(small(t))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []dashboard.Item{
		{
			Marker: dashboard.MarkerApprove, Raw: "approve-branch", Branch: "renovate/major-z", Section: "Pending Approval",
			Title: "chore(deps): update z to v2", Deps: []string{"z", "z-cli"},
		},
		{
			Marker: dashboard.MarkerApproveAll, Raw: "approve-all-pending-prs", Section: "Pending Approval",
			Title: "🔐 Create all pending approval PRs at once 🔐", Ticked: true,
		},
		{
			Marker: dashboard.MarkerRebase, Raw: "rebase-branch", Branch: "renovate/x", Section: "Open",
			Title: "fix(deps): update module x to v1.2.0", PR: 35,
		},
		{Marker: dashboard.MarkerOther, Raw: "frobnicate-branch", Branch: "renovate/y", Section: "Open", Title: "something new"},
		{
			Marker: dashboard.MarkerManualJob,
			Raw:    "manual job",
			Title:  "Check this box to trigger a request for Renovate to run again on this repository",
		},
	}
	if len(snap.Items) != len(want) {
		t.Fatalf("Parse() items = %+v, want %d", snap.Items, len(want))
	}
	for i := range want {
		got := snap.Items[i]
		if got.Marker != want[i].Marker || got.Raw != want[i].Raw || got.Branch != want[i].Branch || got.Section != want[i].Section ||
			got.Title != want[i].Title || got.PR != want[i].PR || got.Ticked != want[i].Ticked || !slices.Equal(got.Deps, want[i].Deps) {
			t.Errorf("item %d = %+v, want %+v", i, got, want[i])
		}
	}
	if snap.Unknown != 1 {
		t.Errorf("Unknown = %d, want 1", snap.Unknown)
	}
}

func TestParse_NotADashboard(t *testing.T) {
	t.Parallel()
	if _, err := dashboard.Parse("Just an issue.\n\n## Steps\n\n- [ ] a task\n"); !errors.Is(err, dashboard.ErrNotDashboard) {
		t.Errorf("Parse(issue) = %v, want ErrNotDashboard", err)
	}
	if _, err := dashboard.Parse("Read the [Dependency Dashboard](https://docs.renovatebot.com/key-concepts/dashboard/) docs.\r\n"); err != nil {
		t.Errorf("Parse(empty dashboard) = %v, want nil", err)
	}
}

func TestDiff(t *testing.T) {
	t.Parallel()
	before, err := dashboard.Parse(small(t))
	if err != nil {
		t.Fatal(err)
	}
	after := before
	after.Items = slices.Clone(before.Items)
	after.Items[0].Ticked = true // a human ticks approve
	after.Items = append(after.Items, dashboard.Item{Marker: dashboard.MarkerRetry, Raw: "retry-branch", Branch: "renovate/new", Ticked: true})

	got := dashboard.Diff(before, after)
	want := []dashboard.Tick{
		{
			Marker:  dashboard.MarkerApprove,
			Raw:     "approve-branch",
			Branch:  "renovate/major-z",
			Section: "Pending Approval",
			Title:   "chore(deps): update z to v2",
		},
		{Marker: dashboard.MarkerRetry, Raw: "retry-branch", Branch: "renovate/new"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Diff() = %+v, want %+v", got, want)
	}
	// The global box was already ticked: no tick. Unticking is none either.
	if got := dashboard.Diff(after, before); len(got) != 0 {
		t.Errorf("Diff(after, before) = %+v, want none", got)
	}
}
