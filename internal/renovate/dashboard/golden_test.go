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
	"os"
	"slices"
	"testing"

	"github.com/donaldgifford/boop/internal/renovate/dashboard"
)

const pin = "renovate/pin-dependencies"

func parseFile(t *testing.T, name string) dashboard.Snapshot {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := dashboard.Parse(string(b))
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return snap
}

func sections(s *dashboard.Snapshot) []string {
	var out []string
	for i := range s.Items {
		if !slices.Contains(out, s.Items[i].Section) {
			out = append(out, s.Items[i].Section)
		}
	}
	return out
}

func TestProxmox34_PinErroredBeforeTheFix(t *testing.T) {
	t.Parallel()
	snap := parseFile(t, "proxmox-34-errored.md")
	if got, want := sections(&snap), []string{"Errored", "Open"}; !slices.Equal(got, want) {
		t.Errorf("sections = %v, want %v", got, want)
	}
	errored := snap.Section("Errored")
	if len(errored) != 1 || errored[0].Marker != dashboard.MarkerRetry || errored[0].Branch != pin || errored[0].Ticked {
		t.Fatalf("Errored = %+v, want the unticked pin retry", errored)
	}
	if errored[0].Title != "chore(deps): pin dependencies" || len(errored[0].Deps) != 19 || errored[0].Deps[0] != "actions/checkout" {
		t.Errorf("pin item = %+v, want its title and 19 deps", errored[0])
	}
	if want := []string{"⚠️ WARN: Error updating branch: update failure"}; !slices.Equal(snap.Problems, want) {
		t.Errorf("Problems = %q, want %q", snap.Problems, want)
	}
	if _, ok := snap.Find("rebase-branch", pin); ok {
		t.Error("the pin branch is Open before the fix")
	}
	if open := snap.Section("Open"); len(open) != 12 || open[0].PR != 35 || open[11].Marker != dashboard.MarkerRebaseAll {
		t.Errorf("Open = %d items, first PR %d; want 11 PRs and rebase-all", len(open), open[0].PR)
	}
}

func TestProxmox34_PinOpenAfterTheFix(t *testing.T) {
	t.Parallel()
	snap := parseFile(t, "proxmox-34-open.md")
	if got := snap.Section("Errored"); len(got) != 0 {
		t.Errorf("Errored = %+v, want none", got)
	}
	it, ok := snap.Find("rebase-branch", pin)
	if !ok || it.Section != "Open" || it.PR != 46 || it.Title != "chore(deps): pin dependencies" || len(it.Deps) != 18 {
		t.Errorf("pin item = %+v, %v; want Open as #46 with 18 deps", it, ok)
	}
	if len(snap.Problems) != 0 {
		t.Errorf("Problems = %q, want none", snap.Problems)
	}
}

func TestProxmox34_HumanTickVersusBotRewrite(t *testing.T) {
	t.Parallel()
	before := parseFile(t, "proxmox-34-errored.md")
	ticked := parseFile(t, "proxmox-34-ticked.md")
	after := parseFile(t, "proxmox-34-open.md")

	want := []dashboard.Tick{
		{Marker: dashboard.MarkerRetry, Raw: "retry-branch", Branch: pin, Section: "Errored", Title: "chore(deps): pin dependencies"},
	}
	if got := dashboard.Diff(before, ticked); !slices.Equal(got, want) {
		t.Errorf("Diff(errored, ticked) = %+v, want %+v", got, want)
	}
	if got := dashboard.Diff(ticked, after); len(got) != 0 {
		t.Errorf("Diff(ticked, bot rewrite) = %+v, want none", got)
	}
}

func TestProxmox34_ScheduleAndRateLimit(t *testing.T) {
	t.Parallel()
	awaiting := parseFile(t, "proxmox-34-awaiting-schedule.md")
	items := awaiting.Section("Awaiting Schedule")
	if len(items) < 2 || len(items) != len(awaiting.Items) {
		t.Fatalf("Awaiting Schedule = %d of %d items", len(items), len(awaiting.Items))
	}
	last := len(items) - 1
	for _, it := range items[:last] {
		if it.Marker != dashboard.MarkerUnschedule || it.Branch == "" {
			t.Errorf("item %+v, want an unschedule branch", it)
		}
	}
	if items[0].Branch != pin || items[last].Marker != dashboard.MarkerCreateAllAwait {
		t.Errorf("first %+v, last %+v; want the pin branch first and create-all last", items[0], items[last])
	}

	limited := parseFile(t, "proxmox-34-rate-limited.md")
	rl := limited.Section("Rate-Limited")
	if len(rl) != 3 || rl[0].Marker != dashboard.MarkerUnlimit || rl[0].Branch != "renovate/go.yaml.in-yaml-v4-4.x" ||
		rl[2].Marker != dashboard.MarkerCreateAllLimited {
		t.Errorf("Rate-Limited = %+v, want two unlimit items and create-all", rl)
	}
}

func TestEverySection(t *testing.T) {
	t.Parallel()
	snap := parseFile(t, "every-section.md")
	want := []struct{ section, marker, branch string }{
		{"Config Migration Needed", dashboard.MarkerConfigMigration, ""},
		{"Pending Approval", dashboard.MarkerApprove, "renovate/major-golang"},
		{"Pending Approval", dashboard.MarkerApproveAll, ""},
		{"PR Creation Approval Required", dashboard.MarkerApprovePR, "renovate/actions-cache-5.x"},
		{"Awaiting Schedule", dashboard.MarkerUnschedule, "renovate/lock-file-maintenance"},
		{"Rate-Limited", dashboard.MarkerUnlimit, "renovate/go.yaml.in-yaml-v4-4.x"},
		{"Rate-Limited", dashboard.MarkerCreateAllLimited, ""},
		{"Errored", dashboard.MarkerRetry, pin},
		{"Edited/Blocked", dashboard.MarkerRebase, "renovate/edited"},
		{"Open", dashboard.MarkerRebase, "renovate/github.com-pkg-sftp-1.x"},
		{"Open", dashboard.MarkerRebaseAll, ""},
		{"Ignored or Blocked", dashboard.MarkerRecreate, "renovate/actions-labeler-7.x"},
		{"", dashboard.MarkerManualJob, ""},
	}
	if len(snap.Items) != len(want) {
		t.Fatalf("items = %d, want %d: %+v", len(snap.Items), len(want), snap.Items)
	}
	for i, w := range want {
		if it := snap.Items[i]; it.Section != w.section || it.Marker != w.marker || it.Branch != w.branch {
			t.Errorf("item %d = %q %q %q, want %q %q %q", i, it.Section, it.Marker, it.Branch, w.section, w.marker, w.branch)
		}
	}
	if snap.Unknown != 0 || len(snap.Problems) != 2 {
		t.Errorf("Unknown = %d, Problems = %q; want 0 and 2", snap.Unknown, snap.Problems)
	}
}
