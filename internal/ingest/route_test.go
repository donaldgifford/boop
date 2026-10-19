package ingest

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/donaldgifford/boop/internal/workflows"
	"github.com/donaldgifford/boop/test/fakegithub"
)

// TestRoute walks DESIGN-0003's routing table, one case per row plus
// the own-bot filter, the author checks, the commit cap and a push off
// the default branch.
func TestRoute(t *testing.T) {
	t.Parallel()
	const repo, other = "repo/github/101", "repo/github/102"
	start := &workflows.RepoState{
		Platform: workflows.Platform, RepoID: 101, Slug: "acme/app", DefaultBranch: "main",
		InstallationID: 7, DiscoveryInterval: testConfig().Apps[0].Discovery.Every,
	}
	configChanged := Action{
		Kind: KindSignalWithStart, WorkflowID: repo, Signal: workflows.ChangedSignal,
		Arg: workflows.Changed{
			Reason: workflows.ChangedReasonConfig, Slug: "acme/app", DefaultBranch: "main",
			InstallationID: 7, DeliveryID: "d-1",
		},
		Start: start,
	}
	repositoryChanged := []Action{{
		Kind: KindSignal, WorkflowID: repo, Signal: workflows.ChangedSignal,
		Arg: workflows.Changed{
			Reason: workflows.ChangedReasonRepository, Slug: "acme/app", DefaultBranch: "main",
			InstallationID: 7, DeliveryID: "d-1",
		},
	}}
	event := func(reason string, prio workflows.Priority) []Action {
		return []Action{{
			Kind: KindSignal, WorkflowID: repo, Signal: workflows.EventSignal,
			Arg: workflows.Event{Reason: reason, Priority: prio, DeliveryID: "d-1"},
		}}
	}
	discovery := Action{
		Kind: KindStartDiscovery, WorkflowID: "discovery/boop-bot/installation/7",
		Arg: workflows.DiscoveryInput{App: "boop-bot", Installations: []int64{7}},
	}
	suspend := func(s bool) Action {
		return Action{
			Kind: KindSignal, WorkflowID: "installation/github/7", Signal: workflows.SuspendSignal,
			Arg: workflows.Suspend{Suspended: s},
		}
	}
	offboard := func(reason string) []Action {
		acts := make([]Action, 0, 2)
		for _, id := range []string{repo, other} {
			acts = append(acts, Action{
				Kind: KindSignal, WorkflowID: id, Signal: workflows.ChangedSignal,
				Arg: workflows.Changed{Reason: reason, InstallationID: 7, DeliveryID: "d-1"},
			})
		}
		return acts
	}

	tests := []struct {
		name  string
		event string
		file  string
		body  []byte // instead of file
		want  []Action
	}{
		{name: "ping", event: "ping", file: "ping"},
		{name: "own bot", event: "issues", file: "issues-edited-own"},
		{
			name:  "dashboard checkbox",
			event: "issues",
			file:  "issues-edited-dashboard",
			want:  event(workflows.EventReasonDashboard, workflows.PriorityHigh),
		},
		{name: "issue by a human", event: "issues", file: "issues-edited-human-issue"},
		{
			name:  "pr checkbox",
			event: "pull_request",
			file:  "pull-request-edited",
			want:  event(workflows.EventReasonPRCheckbox, workflows.PriorityHigh),
		},
		{name: "pr closed", event: "pull_request", file: "pull-request-closed", want: event(workflows.EventReasonPRClosed, workflows.PriorityNormal)},
		{name: "pr by a human", event: "pull_request", file: "pull-request-closed-human"},
		{name: "pr opened", event: "pull_request", file: "pull-request-opened"},
		{name: "push adds config", event: "push", file: "push-config-added", want: []Action{configChanged}},
		{name: "push removes config", event: "push", file: "push-config-removed", want: []Action{configChanged}},
		{name: "push at the commit cap", event: "push", body: atCap(t), want: []Action{configChanged}},
		{name: "push of other files", event: "push", file: "push-other-files"},
		{name: "push off the default branch", event: "push", file: "push-non-default-branch"},
		{name: "repository renamed", event: "repository", file: "repository-renamed", want: repositoryChanged},
		{name: "repository transferred", event: "repository", file: "repository-transferred", want: repositoryChanged},
		{name: "repository archived", event: "repository", file: "repository-archived", want: repositoryChanged},
		{name: "repository unarchived", event: "repository", file: "repository-unarchived", want: repositoryChanged},
		{name: "repository deleted", event: "repository", file: "repository-deleted", want: repositoryChanged},
		{name: "repository default branch", event: "repository", file: "repository-edited-branch", want: repositoryChanged},
		{name: "repository description", event: "repository", file: "repository-edited-other"},
		{name: "installation repos added", event: "installation_repositories", file: "installation-repos-added", want: []Action{discovery}},
		{
			name:  "installation repos removed",
			event: "installation_repositories",
			file:  "installation-repos-removed",
			want:  offboard(workflows.ChangedReasonRemoved),
		},
		{name: "installation created", event: "installation", file: "installation-created", want: []Action{discovery}},
		{name: "installation new permissions", event: "installation", file: "installation-new-permissions", want: []Action{discovery}},
		{name: "installation suspend", event: "installation", file: "installation-suspend", want: []Action{suspend(true)}},
		{name: "installation unsuspend", event: "installation", file: "installation-unsuspend", want: []Action{suspend(false), discovery}},
		{
			name:  "installation deleted",
			event: "installation",
			file:  "installation-deleted",
			want:  offboard(workflows.ChangedReasonInstallationDeleted),
		},
		{name: "anything else", event: "star", body: []byte(`{"action":"created","sender":{"login":"alice"}}`)},
	}
	app := testConfig().Apps[0]
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body := tt.body
			if body == nil {
				body = payload(t, tt.file)
			}
			p, err := Parse(tt.event, "d-1", body)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			got := Route(tt.event, p, app, testPaths)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("Route (-want +got):\n%s", diff)
			}
		})
	}
}

// atCap is a push to the default branch at GitHub's commit cap, none of
// whose commits touch a config path.
func atCap(tb testing.TB) []byte {
	tb.Helper()
	repo := &fakegithub.Repo{ID: 101, Slug: "acme/app", DefaultBranch: "main"}
	b, err := json.Marshal(fakegithub.PushAtCap(repo, 7, "refs/heads/main", "alice"))
	if err != nil {
		tb.Fatal(err)
	}
	return b
}

func TestOwn_RecordsTheSender(t *testing.T) {
	t.Parallel()
	p, err := Parse("issues", "d-1", payload(t, "issues-edited-own"))
	if err != nil {
		t.Fatal(err)
	}
	if !Own(p, testConfig().Apps[0]) {
		t.Error("Own = false for boop-bot[bot]'s edit")
	}
	if p.SenderType != "Bot" || p.Repo.Slug != "acme/app" || p.InstallationID != 7 || p.subject() != "#1" {
		t.Errorf("payload fields = %+v", p)
	}
}

func TestParse_UnknownEvent(t *testing.T) {
	t.Parallel()
	if _, err := Parse("not_an_event", "d-1", []byte(`{}`)); !errors.Is(err, ErrUnknownEvent) {
		t.Errorf("Parse = %v, want ErrUnknownEvent", err)
	}
	if _, err := Parse("issues", "d-1", []byte(`{`)); err == nil || errors.Is(err, ErrUnknownEvent) {
		t.Errorf("Parse of a broken body = %v, want a decode error", err)
	}
}

func TestAppFor(t *testing.T) {
	t.Parallel()
	cfg := testConfig()
	if app, err := appFor(cfg, "1"); err != nil || app.Name != "boop-bot" {
		t.Errorf("appFor(1) = %v, %v", app, err)
	}
	for _, id := range []string{"", "x", "2", "3"} {
		if _, err := appFor(cfg, id); !errors.Is(err, ErrUnknownApp) {
			t.Errorf("appFor(%q) = %v, want ErrUnknownApp", id, err)
		}
	}
}
