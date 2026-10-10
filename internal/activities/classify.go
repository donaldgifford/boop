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

import (
	"fmt"
	"slices"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"

	"github.com/donaldgifford/boop/internal/kube"
	"github.com/donaldgifford/boop/internal/renovate"
	"github.com/donaldgifford/boop/internal/workflows"
)

// Repository results by classification row (DESIGN-0001 § RunRenovate
// activity, Classification).
var (
	// workflows.OffboardResults end the workflow; the workflow tells
	// them apart from the other skips by RepositoryResult.
	skipResults = []string{
		"fork", "fork-missing", "fork-mode-forked", "cannot-fork", "forbidden", "blocked", "disabled",
		"disabled-by-config", "disabled-closed-onboarding", "empty", "no-package-files", "uninitiated",
	}
	successResults = []string{"done", "automerged"}
	failedResults  = []string{"lockfile-error", "onboarding"}
	// infraResults are not the repository's fault.
	infraResults = []string{
		"temporary-error", "repository-changed", "external-host-error", "missing-api-credentials",
		"authentication-error", "bad-credentials", "integration-unauthorized", "platform-not-found",
		"platform-unknown-error", "gpg-failed", "disk-space", "out-of-memory", "unknown-error",
	}
)

// resultRateLimited is Renovate's primary-limit repository result.
const resultRateLimited = "rate-limit-exceeded"

// defaultRetryAfter is the installation's back-off when a limit carried
// no retry-after (DESIGN-0001 OQ5).
const defaultRetryAfter = time.Minute

// exitClasses are exitCodeForErrors's codes.
var exitClasses = map[int]string{
	3: "system", 4: "platform", 5: "config", 6: "temporary", 7: "external host, lockfile or credentials", 8: "unknown",
}

// runEnd is everything classification reads about one finished run.
type runEnd struct {
	Scan *renovate.Result
	Exit kube.Exit
	// Exited is false when the run was stopped before the container
	// ended on its own.
	Exited bool
	// SoftDeadline is true when the activity stopped the run at its soft
	// deadline.
	SoftDeadline bool
}

// classification is one run's verdict: an outcome, or an activity error
// of type errType.
type classification struct {
	Outcome workflows.Outcome
	ErrType string
	Reason  string
	RetryAt time.Time
	// Alert marks results that mean the profile is undersized, or that
	// should not occur at all.
	Alert bool
}

// Failed reports whether the verdict is an activity error.
func (c *classification) Failed() bool { return c.ErrType != "" }

// classify applies the design's table, first matching row from the top.
// A secondary limit in the log only turns a run into a rate-limited
// error when the run did not otherwise succeed, skip or fail on its own
// account: those results stand, and the next run's admission sees the
// spend in the readings.
func classify(end *runEnd, now time.Time) classification {
	scan := end.Scan
	retryAt := func() time.Time {
		d := scan.RetryAfter
		if d <= 0 {
			d = defaultRetryAfter
		}
		return now.Add(d)
	}
	if scan.Finished == nil {
		switch {
		case end.SoftDeadline:
			return classification{Outcome: workflows.OutcomeTimedOut, Reason: "stopped at the soft deadline"}
		case scan.SecondaryLimit:
			return classification{ErrType: workflows.ErrTypeRateLimited, Reason: "secondary rate limit", RetryAt: retryAt()}
		default:
			return noFinishedLine(end)
		}
	}
	result := scan.Finished.Result
	switch {
	case slices.Contains(workflows.OffboardResults, result), slices.Contains(skipResults, result):
		return crossCheck(&classification{Outcome: workflows.OutcomeSkipped, Reason: result}, end.Exit)
	case slices.Contains(successResults, result):
		return crossCheck(&classification{Outcome: workflows.OutcomeSucceeded, Reason: result}, end.Exit)
	case result == resultRateLimited:
		return classification{ErrType: workflows.ErrTypeRateLimited, Reason: result, RetryAt: retryAt()}
	case strings.HasPrefix(result, "config-"), slices.Contains(failedResults, result):
		return classification{Outcome: workflows.OutcomeFailed, Reason: result, Alert: result == "onboarding"}
	case scan.SecondaryLimit:
		return classification{ErrType: workflows.ErrTypeRateLimited, Reason: result + " after a secondary rate limit", RetryAt: retryAt()}
	case slices.Contains(infraResults, result):
		return classification{
			ErrType: workflows.ErrTypeInfrastructure, Reason: result,
			Alert: result == "disk-space" || result == "out-of-memory",
		}
	default:
		return classification{ErrType: workflows.ErrTypeInfrastructure, Reason: "unrecognised repository result " + result}
	}
}

// noFinishedLine is the container ending, or being stopped, without a
// "Repository finished" line and not at the soft deadline.
func noFinishedLine(end *runEnd) classification {
	c := classification{ErrType: workflows.ErrTypeInfrastructure}
	switch {
	case !end.Exited:
		c.Reason = "run stopped before Renovate finished"
	case end.Exit.Reason == batchv1.JobReasonDeadlineExceeded:
		c.Reason = "job hit activeDeadlineSeconds"
	case end.Exit.Reason == "OOMKilled":
		c.Reason, c.Alert = "container OOMKilled", true
	default:
		c.Reason = fmt.Sprintf("container exited %d (%s) without a Repository finished line", end.Exit.Code, exitClass(end.Exit.Code))
	}
	return c
}

// crossCheck downgrades a Succeeded or Skipped verdict whose exit code is
// one of exitCodeForErrors's error classes: the result line and the
// process disagree, so the run failed in a way the result did not
// record.
func crossCheck(c *classification, exit kube.Exit) classification {
	if class, ok := exitClasses[exit.Code]; ok {
		return classification{
			Outcome: workflows.OutcomeFailed,
			Reason:  fmt.Sprintf("result %s but exit code %d (%s error)", c.Reason, exit.Code, class),
		}
	}
	return *c
}

func exitClass(code int) string {
	if class, ok := exitClasses[code]; ok {
		return class
	}
	return "unclassified"
}
