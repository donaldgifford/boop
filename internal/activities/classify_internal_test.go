package activities

import (
	"testing"
	"time"

	"github.com/donaldgifford/boop/internal/kube"
	"github.com/donaldgifford/boop/internal/renovate"
	"github.com/donaldgifford/boop/internal/workflows"
)

func finished(result string) *renovate.Result {
	return &renovate.Result{Finished: &renovate.Finished{Result: result}}
}

// TestClassify has one case per row of DESIGN-0001's classification
// table, in table order, plus the exit-code cross-check.
func TestClassify(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_000_000, 0)
	exit0 := kube.Exit{Code: 0, Reason: "Completed"}
	tests := []struct {
		name string
		end  runEnd
		want classification
	}{
		// Row 1: offboarding skips.
		{
			"disabled-no-config",
			runEnd{Scan: finished("disabled-no-config"), Exit: exit0, Exited: true},
			classification{Outcome: workflows.OutcomeSkipped, Reason: "disabled-no-config"},
		},
		{
			"archived",
			runEnd{Scan: finished("archived"), Exit: exit0, Exited: true},
			classification{Outcome: workflows.OutcomeSkipped, Reason: "archived"},
		},
		{
			"not-found",
			runEnd{Scan: finished("not-found"), Exit: exit0, Exited: true},
			classification{Outcome: workflows.OutcomeSkipped, Reason: "not-found"},
		},
		{
			"renamed",
			runEnd{Scan: finished("renamed"), Exit: exit0, Exited: true},
			classification{Outcome: workflows.OutcomeSkipped, Reason: "renamed"},
		},
		{
			"pending-deletion",
			runEnd{Scan: finished("pending-deletion"), Exit: exit0, Exited: true},
			classification{Outcome: workflows.OutcomeSkipped, Reason: "pending-deletion"},
		},
		{
			"mirror",
			runEnd{Scan: finished("mirror"), Exit: exit0, Exited: true},
			classification{Outcome: workflows.OutcomeSkipped, Reason: "mirror"},
		},
		// Row 2: other skips.
		{
			"fork",
			runEnd{Scan: finished("fork"), Exit: exit0, Exited: true},
			classification{Outcome: workflows.OutcomeSkipped, Reason: "fork"},
		},
		{
			"disabled-by-config",
			runEnd{Scan: finished("disabled-by-config"), Exit: exit0, Exited: true},
			classification{Outcome: workflows.OutcomeSkipped, Reason: "disabled-by-config"},
		},
		{
			"no-package-files",
			runEnd{Scan: finished("no-package-files"), Exit: exit0, Exited: true},
			classification{Outcome: workflows.OutcomeSkipped, Reason: "no-package-files"},
		},
		{
			"uninitiated",
			runEnd{Scan: finished("uninitiated"), Exit: exit0, Exited: true},
			classification{Outcome: workflows.OutcomeSkipped, Reason: "uninitiated"},
		},
		// Row 3: success.
		{
			"done",
			runEnd{Scan: finished("done"), Exit: exit0, Exited: true},
			classification{Outcome: workflows.OutcomeSucceeded, Reason: "done"},
		},
		{
			"automerged",
			runEnd{Scan: finished("automerged"), Exit: exit0, Exited: true},
			classification{Outcome: workflows.OutcomeSucceeded, Reason: "automerged"},
		},
		// Row 4: primary limit, with and without retry-after.
		{
			"rate-limit-exceeded",
			runEnd{Scan: finished("rate-limit-exceeded"), Exit: kube.Exit{Code: 6}, Exited: true},
			classification{ErrType: workflows.ErrTypeRateLimited, Reason: "rate-limit-exceeded", RetryAt: now.Add(time.Minute)},
		},
		{"rate-limit-exceeded with retry-after", runEnd{
			Scan: &renovate.Result{Finished: &renovate.Finished{Result: "rate-limit-exceeded"}, SecondaryLimit: true, RetryAfter: 2 * time.Minute},
			Exit: kube.Exit{Code: 6}, Exited: true,
		}, classification{ErrType: workflows.ErrTypeRateLimited, Reason: "rate-limit-exceeded", RetryAt: now.Add(2 * time.Minute)}},
		// Row 5: config and lockfile failures.
		{
			"config-validation",
			runEnd{Scan: finished("config-validation"), Exit: kube.Exit{Code: 5}, Exited: true},
			classification{Outcome: workflows.OutcomeFailed, Reason: "config-validation"},
		},
		{
			"lockfile-error",
			runEnd{Scan: finished("lockfile-error"), Exit: kube.Exit{Code: 7}, Exited: true},
			classification{Outcome: workflows.OutcomeFailed, Reason: "lockfile-error"},
		},
		// Row 6: onboarding, which alerts.
		{
			"onboarding",
			runEnd{Scan: finished("onboarding"), Exit: exit0, Exited: true},
			classification{Outcome: workflows.OutcomeFailed, Reason: "onboarding", Alert: true},
		},
		// Row 7: soft deadline without a finished line.
		{
			"soft deadline",
			runEnd{Scan: &renovate.Result{Progress: renovate.Progress{BranchesChanged: 2}}, SoftDeadline: true},
			classification{Outcome: workflows.OutcomeTimedOut, Reason: "stopped at the soft deadline"},
		},
		// Row 9: infrastructure results.
		{
			"temporary-error",
			runEnd{Scan: finished("temporary-error"), Exit: kube.Exit{Code: 6}, Exited: true},
			classification{ErrType: workflows.ErrTypeInfrastructure, Reason: "temporary-error"},
		},
		{
			"external-host-error",
			runEnd{Scan: finished("external-host-error"), Exit: kube.Exit{Code: 7}, Exited: true},
			classification{ErrType: workflows.ErrTypeInfrastructure, Reason: "external-host-error"},
		},
		{
			"bad-credentials",
			runEnd{Scan: finished("bad-credentials"), Exit: kube.Exit{Code: 7}, Exited: true},
			classification{ErrType: workflows.ErrTypeInfrastructure, Reason: "bad-credentials"},
		},
		{
			"disk-space alerts",
			runEnd{Scan: finished("disk-space"), Exit: kube.Exit{Code: 3}, Exited: true},
			classification{ErrType: workflows.ErrTypeInfrastructure, Reason: "disk-space", Alert: true},
		},
		{
			"out-of-memory alerts",
			runEnd{Scan: finished("out-of-memory"), Exit: kube.Exit{Code: 3}, Exited: true},
			classification{ErrType: workflows.ErrTypeInfrastructure, Reason: "out-of-memory", Alert: true},
		},
		{
			"unknown-error",
			runEnd{Scan: finished("unknown-error"), Exit: kube.Exit{Code: 8}, Exited: true},
			classification{ErrType: workflows.ErrTypeInfrastructure, Reason: "unknown-error"},
		},
		{
			"exited without a finished line",
			runEnd{Scan: &renovate.Result{}, Exit: kube.Exit{Code: 1, Reason: "Error"}, Exited: true},
			classification{ErrType: workflows.ErrTypeInfrastructure, Reason: "container exited 1 (unclassified) without a Repository finished line"},
		},
		{
			"OOMKilled alerts",
			runEnd{Scan: &renovate.Result{}, Exit: kube.Exit{Code: 137, Reason: "OOMKilled"}, Exited: true},
			classification{ErrType: workflows.ErrTypeInfrastructure, Reason: "container OOMKilled", Alert: true},
		},
		{
			"DeadlineExceeded",
			runEnd{Scan: &renovate.Result{}, Exit: kube.Exit{Code: -1, Reason: "DeadlineExceeded"}, Exited: true},
			classification{ErrType: workflows.ErrTypeInfrastructure, Reason: "job hit activeDeadlineSeconds"},
		},
		{
			"unrecognised result",
			runEnd{Scan: finished("something-new"), Exit: exit0, Exited: true},
			classification{ErrType: workflows.ErrTypeInfrastructure, Reason: "unrecognised repository result something-new"},
		},
		// Secondary limits (OQ5).
		{"secondary limit, no finished line", runEnd{
			Scan: &renovate.Result{SecondaryLimit: true, RetryAfter: 30 * time.Second}, Exit: kube.Exit{Code: 1}, Exited: true,
		}, classification{ErrType: workflows.ErrTypeRateLimited, Reason: "secondary rate limit", RetryAt: now.Add(30 * time.Second)}},
		{"secondary limit turns an infrastructure result", runEnd{
			Scan: &renovate.Result{Finished: &renovate.Finished{Result: "external-host-error"}, SecondaryLimit: true},
			Exit: kube.Exit{Code: 7}, Exited: true,
		}, classification{ErrType: workflows.ErrTypeRateLimited, Reason: "external-host-error after a secondary rate limit", RetryAt: now.Add(time.Minute)}},
		{"secondary limit does not undo a success", runEnd{
			Scan: &renovate.Result{Finished: &renovate.Finished{Result: "done"}, SecondaryLimit: true}, Exit: exit0, Exited: true,
		}, classification{Outcome: workflows.OutcomeSucceeded, Reason: "done"}},
		// Exit-code cross-check.
		{
			"done with a platform exit code",
			runEnd{Scan: finished("done"), Exit: kube.Exit{Code: 4}, Exited: true},
			classification{Outcome: workflows.OutcomeFailed, Reason: "result done but exit code 4 (platform error)"},
		},
		{
			"skip with a config exit code",
			runEnd{Scan: finished("fork"), Exit: kube.Exit{Code: 5}, Exited: true},
			classification{Outcome: workflows.OutcomeFailed, Reason: "result fork but exit code 5 (config error)"},
		},
		{
			"done with exit 1 stands",
			runEnd{Scan: finished("done"), Exit: kube.Exit{Code: 1}, Exited: true},
			classification{Outcome: workflows.OutcomeSucceeded, Reason: "done"},
		},
		{
			"stopped without a finished line",
			runEnd{Scan: &renovate.Result{}},
			classification{ErrType: workflows.ErrTypeInfrastructure, Reason: "run stopped before Renovate finished"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := classify(&tt.end, now); got != tt.want {
				t.Errorf("classify = %+v\n          want %+v", got, tt.want)
			}
		})
	}
}
