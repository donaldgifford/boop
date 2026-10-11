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

// Package renovate reads a Renovate run from its JSON log (DESIGN-0001
// § Process builder, OQ8): a Scanner fed line by line counts branch and
// PR events, records the "Repository finished" fields and parses the
// "Printing report" line into update tuples, managers and problems.
// When the report is missing or does not parse, tuples are rebuilt from
// the branch and PR events and the result is marked ReportMissing.
//
// The scanner matches msg exactly; the strings are pinned by fixtures
// under testdata, so a Renovate change fails a test rather than a run.
package renovate

import (
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Messages the scanner matches (DESIGN-0001 § Process builder).
const (
	msgBranchCreated = "Branch created"
	msgBranchUpdated = "Branch updated"
	msgPRCreated     = "PR created"
	msgPRUpdated     = "PR updated"
	msgFinished      = "Repository finished"
	msgReport        = "Printing report"
	msgStarted       = "Renovate started"

	dryRunCommitPrefix   = "DRY-RUN: Would commit files to branch "
	dryRunCreatePRPrefix = "DRY-RUN: Would create PR: "
	dryRunUpdatePRPrefix = "DRY-RUN: Would update PR #"
)

// Progress counts what an attempt changed, live or in dryRun: full.
type Progress struct {
	BranchesChanged int
	PRsChanged      int
}

// Finished is the "Repository finished" line.
type Finished struct {
	Result     string
	Status     string
	ExitCode   int
	DurationMs int64
	Cloned     bool
}

// UpdateTuple is one upgrade from the report's branches[].upgrades[].
// Manager is recovered from packageFiles, which the report keys by
// manager.
type UpdateTuple struct {
	BranchName     string
	BranchResult   string
	PRNumber       int
	Manager        string
	Datasource     string
	DepName        string
	PackageName    string
	PackageFile    string
	UpdateType     string
	CurrentVersion string
	NewVersion     string
}

// Problem is a repository-level problem from the report.
type Problem struct {
	Level   int
	Message string
}

// Result is what the scanner learned from one run's log.
type Result struct {
	RenovateVersion string
	Progress        Progress
	// Finished is nil when no "Repository finished" line appeared.
	Finished *Finished
	Tuples   []UpdateTuple
	Managers []string
	Problems []Problem
	// ReportMissing is true when the report line was absent or did not
	// parse; Tuples then come from branch and PR events.
	ReportMissing bool
	// SecondaryLimit is true when a request in the run hit a GitHub
	// secondary rate limit (a 429, or a 403 naming the rate limit);
	// RetryAfter is the largest retry-after it carried, zero when none
	// did (DESIGN-0001 OQ5).
	SecondaryLimit bool
	RetryAfter     time.Duration
}

// Scanner consumes a run's log lines. It is not safe for concurrent use;
// feed it from the one goroutine following the log.
type Scanner struct {
	slug       string
	progress   Progress
	finished   *Finished
	version    string
	report     *report
	secondary  bool
	retryAfter time.Duration
	// events, in order of first sight, for rebuilding tuples.
	branches []string
	prs      map[string]int
	results  map[string]string
}

// NewScanner returns a Scanner for the run's repository slug.
func NewScanner(slug string) *Scanner {
	return &Scanner{slug: slug, prs: make(map[string]int), results: make(map[string]string)}
}

// line is the subset of a bunyan line the scanner reads.
type line struct {
	Msg             string          `json:"msg"`
	Repository      string          `json:"repository"`
	Branch          string          `json:"branch"`
	PR              json.RawMessage `json:"pr"`
	PRTitle         string          `json:"prTitle"`
	Result          string          `json:"result"`
	Status          string          `json:"status"`
	ExitCode        *int            `json:"exitCode"`
	DurationMs      int64           `json:"durationMs"`
	Cloned          bool            `json:"cloned"`
	RenovateVersion string          `json:"renovateVersion"`
	Report          json.RawMessage `json:"report"`
}

// Feed scans one log line. Lines that are not JSON, or not about the
// run's repository when they name one, are ignored.
func (s *Scanner) Feed(text string) {
	if !strings.HasPrefix(strings.TrimSpace(text), "{") {
		return
	}
	var l line
	if err := json.Unmarshal([]byte(text), &l); err != nil {
		return
	}
	if l.Repository != "" && s.slug != "" && l.Repository != s.slug {
		return
	}
	s.rateLimit(text)
	switch l.Msg {
	case msgStarted:
		s.version = l.RenovateVersion
	case msgFinished:
		f := &Finished{Result: l.Result, Status: l.Status, DurationMs: l.DurationMs, Cloned: l.Cloned}
		if l.ExitCode != nil {
			f.ExitCode = *l.ExitCode
		}
		s.finished = f
	case msgReport:
		var r report
		if err := json.Unmarshal(l.Report, &r); err == nil && r.Repositories != nil {
			s.report = &r
		}
	default:
		s.change(&l)
	}
}

// retryAfterRe finds a retry-after header value in an error line.
var retryAfterRe = regexp.MustCompile(`"retry-after":\s*"?(\d+)`)

// rateLimit records a secondary-limit response. Renovate logs the
// failed request's error with its statusCode and headers; only lines
// that carry a statusCode are examined.
func (s *Scanner) rateLimit(text string) {
	if !strings.Contains(text, `"statusCode"`) {
		return
	}
	lower := strings.ToLower(text)
	hit := strings.Contains(lower, `"statuscode":429`) ||
		(strings.Contains(lower, `"statuscode":403`) && strings.Contains(lower, "rate limit"))
	if !hit {
		return
	}
	s.secondary = true
	if m := retryAfterRe.FindStringSubmatch(lower); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			s.retryAfter = max(s.retryAfter, time.Duration(n)*time.Second)
		}
	}
}

// change counts a branch or PR event, live or dry-run.
func (s *Scanner) change(l *line) {
	switch {
	case l.Msg == msgBranchCreated, l.Msg == msgBranchUpdated:
		s.progress.BranchesChanged++
		s.branch(l.Branch, branchResult(l.Msg))
	case strings.HasPrefix(l.Msg, dryRunCommitPrefix):
		s.progress.BranchesChanged++
		s.branch(firstNonEmpty(l.Branch, strings.TrimPrefix(l.Msg, dryRunCommitPrefix)), "dry-run-commit")
	case l.Msg == msgPRCreated, l.Msg == msgPRUpdated:
		s.progress.PRsChanged++
		s.pr(l.Branch, prNumber(l.PR), prResult(l.Msg))
	case strings.HasPrefix(l.Msg, dryRunCreatePRPrefix):
		s.progress.PRsChanged++
		s.pr(l.Branch, 0, "dry-run-pr-create")
	case strings.HasPrefix(l.Msg, dryRunUpdatePRPrefix):
		s.progress.PRsChanged++
		n, err := strconv.Atoi(strings.TrimPrefix(l.Msg, dryRunUpdatePRPrefix))
		if err != nil {
			n = 0
		}
		s.pr(l.Branch, n, "dry-run-pr-update")
	}
}

func (s *Scanner) branch(name, result string) {
	if name == "" {
		return
	}
	if !slices.Contains(s.branches, name) {
		s.branches = append(s.branches, name)
	}
	if _, ok := s.results[name]; !ok || !strings.Contains(s.results[name], "pr") {
		s.results[name] = result
	}
}

func (s *Scanner) pr(branch string, n int, result string) {
	if branch == "" {
		return
	}
	s.branch(branch, result)
	s.results[branch] = result
	if n > 0 {
		s.prs[branch] = n
	}
}

// Result returns what the scanner has seen so far.
func (s *Scanner) Result() *Result {
	out := &Result{
		RenovateVersion: s.version, Progress: s.progress, Finished: s.finished,
		SecondaryLimit: s.secondary, RetryAfter: s.retryAfter,
	}
	if rep := s.report.repository(s.slug); rep != nil {
		out.Tuples, out.Managers, out.Problems = rep.tuples(), rep.managers(), rep.problemList()
		return out
	}
	out.ReportMissing = true
	for _, b := range s.branches {
		out.Tuples = append(out.Tuples, UpdateTuple{BranchName: b, BranchResult: s.results[b], PRNumber: s.prs[b]})
	}
	return out
}

func branchResult(msg string) string {
	if msg == msgBranchCreated {
		return "branch-created"
	}
	return "branch-updated"
}

func prResult(msg string) string {
	if msg == msgPRCreated {
		return "pr-created"
	}
	return "pr-updated"
}

// prNumber reads the pr field, a number on live lines.
func prNumber(raw json.RawMessage) int {
	var n int
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	return 0
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
