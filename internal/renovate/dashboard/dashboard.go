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

// Package dashboard parses Renovate's Dependency Dashboard issue body
// (DESIGN-0004 § Dependency dashboard). Items are keyed on Renovate's
// machine marker, such as <!-- approve-branch=renovate/x -->, not on the
// heading text, which Renovate may reword. Diff finds what a human
// ticked between two bodies.
package dashboard

import (
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Branch markers: checkboxes that act on one branch.
const (
	MarkerApprove    = "approve-branch"
	MarkerApprovePR  = "approvePr-branch"
	MarkerUnschedule = "unschedule-branch"
	MarkerUnlimit    = "unlimit-branch"
	MarkerRetry      = "retry-branch"
	MarkerRebase     = "rebase-branch"
	MarkerRecreate   = "recreate-branch"
	// MarkerOther stands for a marker this package does not know; the
	// item's Raw keeps the original.
	MarkerOther = "other"
)

// Global markers: checkboxes that act on the whole repository.
const (
	MarkerApproveAll       = "approve-all-pending-prs"
	MarkerCreateAllLimited = "create-all-rate-limited-prs"
	MarkerCreateAllAwait   = "create-all-awaiting-schedule-prs"
	MarkerRebaseAll        = "rebase-all-open-prs"
	MarkerManualJob        = "manual job"
	MarkerConfigMigration  = "create-config-migration-pr"
)

var known = []string{
	MarkerApprove, MarkerApprovePR, MarkerUnschedule, MarkerUnlimit, MarkerRetry, MarkerRebase, MarkerRecreate,
	MarkerApproveAll, MarkerCreateAllLimited, MarkerCreateAllAwait, MarkerRebaseAll, MarkerManualJob, MarkerConfigMigration,
}

// ErrNotDashboard is returned for a body with neither the dashboard's
// introduction nor any checkbox marker.
var ErrNotDashboard = errors.New("not a Renovate dependency dashboard")

// Item is one checkbox.
type Item struct {
	// Marker is a known marker, or MarkerOther.
	Marker string
	// Raw is the marker as written, which differs from Marker only for
	// MarkerOther.
	Raw string
	// Branch is the branch a branch marker acts on; empty for a global
	// one.
	Branch string
	// Section is the heading the item is under, such as "Errored".
	Section string
	Title   string
	// PR is the pull request the title links to, or zero.
	PR     int
	Deps   []string
	Ticked bool
}

// Key identifies an item across bodies: its raw marker and branch.
func (i *Item) Key() string { return i.Raw + "=" + i.Branch }

// Snapshot is one parsed body.
type Snapshot struct {
	Items []Item
	// Problems are the "Repository Problems" lines.
	Problems []string
	// Unknown is how many items had a marker this package does not know.
	Unknown int
}

// Section returns the items under the heading, in body order.
func (s *Snapshot) Section(heading string) []Item {
	var out []Item
	for i := range s.Items {
		if s.Items[i].Section == heading {
			out = append(out, s.Items[i])
		}
	}
	return out
}

// Find returns the item with the raw marker and branch.
func (s *Snapshot) Find(raw, branch string) (Item, bool) {
	for i := range s.Items {
		if s.Items[i].Raw == raw && s.Items[i].Branch == branch {
			return s.Items[i], true
		}
	}
	return Item{}, false
}

const (
	intro           = "docs.renovatebot.com/key-concepts/dashboard"
	problemsHeading = "Repository Problems"
	detectedHeading = "Detected Dependencies"
)

var (
	// checkbox is " - [ ] <!-- marker=branch -->rest".
	checkbox = regexp.MustCompile(`^\s*[-*]\s+\[([ xX])\]\s*<!--\s*([^=>]+?)(?:=(.*?))?\s*-->\s*(.*)$`)
	// link is "[title](../pull/N)" at the start of the rest.
	link = regexp.MustCompile(`^\[(.*)\]\((?:[^)]*/)?pull/(\d+)\)\s*(.*)$`)
	// deps is the trailing "(`a`, `b`)".
	deps = regexp.MustCompile("\\s*\\(((?:`[^`]+`(?:,\\s*)?)+)\\)\\s*$")
	dep  = regexp.MustCompile("`([^`]+)`")
)

// Parse reads a dashboard body.
func Parse(body string) (Snapshot, error) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	var (
		snap    Snapshot
		section string
		marked  bool
	)
	for line := range strings.SplitSeq(body, "\n") {
		if h, ok := strings.CutPrefix(line, "## "); ok {
			section = strings.TrimSpace(h)
			continue
		}
		if strings.TrimSpace(line) == "---" {
			section = ""
			continue
		}
		if m := checkbox.FindStringSubmatch(line); m != nil {
			marked = true
			snap.Items = append(snap.Items, item(section, m))
			if snap.Items[len(snap.Items)-1].Marker == MarkerOther {
				snap.Unknown++
			}
			continue
		}
		if section == problemsHeading {
			if p, ok := strings.CutPrefix(strings.TrimSpace(line), "- "); ok {
				snap.Problems = append(snap.Problems, strings.TrimSpace(p))
			}
		}
	}
	if !marked && !strings.Contains(body, intro) {
		return Snapshot{}, ErrNotDashboard
	}
	return snap, nil
}

// item builds an Item from a checkbox match.
func item(section string, m []string) Item {
	it := Item{Section: section, Raw: strings.TrimSpace(m[2]), Branch: strings.TrimSpace(m[3]), Ticked: m[1] != " "}
	it.Marker = MarkerOther
	if slices.Contains(known, it.Raw) {
		it.Marker = it.Raw
	}
	rest := strings.TrimSpace(m[4])
	if l := link.FindStringSubmatch(rest); l != nil {
		it.PR, _ = strconv.Atoi(l[2]) //nolint:errcheck // the pattern admits digits only
		rest = l[1] + " " + l[3]
	}
	if d := deps.FindStringSubmatchIndex(rest); d != nil {
		for _, name := range dep.FindAllStringSubmatch(rest[d[2]:d[3]], -1) {
			it.Deps = append(it.Deps, name[1])
		}
		rest = rest[:d[0]]
	}
	it.Title = strings.TrimSpace(strings.ReplaceAll(rest, "**", ""))
	return it
}

// Tick is an item a human ticked.
type Tick struct {
	Marker  string
	Raw     string
	Branch  string
	Section string
	Title   string
}

// Diff returns the items ticked in after that were not ticked, or not
// present, in before. Renovate writes every box unticked, so a bot
// rewrite yields none.
func Diff(before, after Snapshot) []Tick {
	was := make(map[string]bool, len(before.Items))
	for i := range before.Items {
		was[before.Items[i].Key()] = before.Items[i].Ticked
	}
	var out []Tick
	for i := range after.Items {
		it := &after.Items[i]
		if it.Ticked && !was[it.Key()] {
			out = append(out, Tick{Marker: it.Marker, Raw: it.Raw, Branch: it.Branch, Section: it.Section, Title: it.Title})
		}
	}
	return out
}
