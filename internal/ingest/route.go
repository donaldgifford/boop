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

package ingest

import (
	"slices"

	"github.com/google/go-github/v62/github"

	"github.com/donaldgifford/boop/internal/config"
	"github.com/donaldgifford/boop/internal/workflows"
)

const actionEdited = "edited"

// PushCommitCap is the most commits GitHub lists in a push payload. A
// push at the cap may hide a config change, so it counts as one.
const PushCommitCap = 2048

// Kind is what an Action does in Temporal.
type Kind string

// Action kinds.
const (
	// KindSignal signals a running workflow; NotFound means the
	// repository or installation is not onboarded.
	KindSignal Kind = "signal"
	// KindSignalWithStart signals RepoWorkflow, starting it from Start
	// when it is not running.
	KindSignalWithStart Kind = "signalWithStart"
	// KindStartDiscovery starts a targeted DiscoveryWorkflow, or joins
	// the one already running under the same ID.
	KindStartDiscovery Kind = "startDiscovery"
)

// Action is one Temporal call a delivery asks for.
type Action struct {
	Kind       Kind
	WorkflowID string
	// Signal and Arg are the signal for KindSignal and
	// KindSignalWithStart; Arg is the DiscoveryInput for
	// KindStartDiscovery.
	Signal string
	Arg    any
	// Start is the RepoWorkflow input for KindSignalWithStart. The
	// handler sets LastSeen.
	Start *workflows.RepoState
}

// Own reports whether p was sent by app's own bot: such an event is
// never routed (DESIGN-0003 § Our own events).
func Own(p *Payload, app *config.App) bool {
	return p.SenderLogin == app.Webhook.Bot
}

// Route is DESIGN-0003's routing table: the Temporal actions a verified
// delivery asks for, rows top to bottom, first match wins. paths are
// renovate.config_paths. It is pure; no action is "nothing to do".
func Route(event string, p *Payload, app *config.App, paths []string) []Action {
	if event == "ping" || Own(p, app) {
		return nil
	}
	bot := app.Webhook.Bot
	switch e := p.Typed.(type) {
	case *github.IssuesEvent:
		if p.Action == actionEdited && e.GetIssue().GetUser().GetLogin() == bot {
			return repoEvent(p, workflows.EventReasonDashboard, workflows.PriorityHigh)
		}
	case *github.PullRequestEvent:
		return routePR(e, p, bot)
	case *github.PushEvent:
		return routePush(e, p, app, paths)
	case *github.RepositoryEvent:
		return routeRepository(e, p)
	case *github.InstallationRepositoriesEvent:
		switch p.Action {
		case "added":
			return discovery(app, p.InstallationID)
		case "removed":
			return changedEach(e.RepositoriesRemoved, p, workflows.ChangedReasonRemoved)
		}
	case *github.InstallationEvent:
		return routeInstallation(e, p, app)
	}
	return nil
}

// repoEvent signals the payload's repository with event.
func repoEvent(p *Payload, reason string, prio workflows.Priority) []Action {
	if p.Repo == nil {
		return nil
	}
	return []Action{{
		Kind:       KindSignal,
		WorkflowID: workflows.RepoWorkflowID(p.Repo.ID),
		Signal:     workflows.EventSignal,
		Arg:        workflows.Event{Reason: reason, Priority: prio, DeliveryID: p.DeliveryID},
	}}
}

// routePR is the pull_request rows: the bot's PRs only.
func routePR(e *github.PullRequestEvent, p *Payload, bot string) []Action {
	if e.GetPullRequest().GetUser().GetLogin() != bot {
		return nil
	}
	switch p.Action {
	case actionEdited:
		return repoEvent(p, workflows.EventReasonPRCheckbox, workflows.PriorityHigh)
	case "closed":
		return repoEvent(p, workflows.EventReasonPRClosed, workflows.PriorityNormal)
	}
	return nil
}

// routePush is the push rows: on the default branch, a commit touching
// a config path or a commit list at the cap SignalWithStarts
// changed{config}; anything else is nothing (OQ3).
func routePush(e *github.PushEvent, p *Payload, app *config.App, paths []string) []Action {
	if p.Repo == nil || p.Repo.DefaultBranch == "" || e.GetRef() != "refs/heads/"+p.Repo.DefaultBranch {
		return nil
	}
	if len(e.Commits) < PushCommitCap && !touchesConfig(e.Commits, paths) {
		return nil
	}
	return []Action{{
		Kind:       KindSignalWithStart,
		WorkflowID: workflows.RepoWorkflowID(p.Repo.ID),
		Signal:     workflows.ChangedSignal,
		Arg: workflows.Changed{
			Reason:         workflows.ChangedReasonConfig,
			Slug:           p.Repo.Slug,
			DefaultBranch:  p.Repo.DefaultBranch,
			InstallationID: p.InstallationID,
			DeliveryID:     p.DeliveryID,
		},
		Start: &workflows.RepoState{
			Platform:          workflows.Platform,
			RepoID:            p.Repo.ID,
			Slug:              p.Repo.Slug,
			DefaultBranch:     p.Repo.DefaultBranch,
			InstallationID:    p.InstallationID,
			DiscoveryInterval: app.Discovery.Every,
		},
	}}
}

// touchesConfig reports whether a commit adds, modifies or removes one
// of paths.
func touchesConfig(commits []*github.HeadCommit, paths []string) bool {
	for _, c := range commits {
		for _, files := range [][]string{c.Added, c.Modified, c.Removed} {
			for _, f := range files {
				if slices.Contains(paths, f) {
					return true
				}
			}
		}
	}
	return false
}

// repositoryActions are the repository actions that change a slug,
// branch or lifecycle; edited counts only with changes.default_branch.
var repositoryActions = []string{"renamed", "transferred", "archived", "unarchived", "deleted"}

// routeRepository is the repository row.
func routeRepository(e *github.RepositoryEvent, p *Payload) []Action {
	if !slices.Contains(repositoryActions, p.Action) &&
		(p.Action != actionEdited || e.GetChanges().GetDefaultBranch() == nil) {
		return nil
	}
	if p.Repo == nil {
		return nil
	}
	return []Action{changed(p.Repo.ID, workflows.Changed{
		Reason:         workflows.ChangedReasonRepository,
		Slug:           p.Repo.Slug,
		DefaultBranch:  p.Repo.DefaultBranch,
		InstallationID: p.InstallationID,
		DeliveryID:     p.DeliveryID,
	})}
}

// routeInstallation is the installation rows.
func routeInstallation(e *github.InstallationEvent, p *Payload, app *config.App) []Action {
	switch p.Action {
	case "created", "new_permissions_accepted":
		return discovery(app, p.InstallationID)
	case "suspend", "unsuspend":
		acts := []Action{{
			Kind:       KindSignal,
			WorkflowID: workflows.InstallationWorkflowID(p.InstallationID),
			Signal:     workflows.SuspendSignal,
			Arg:        workflows.Suspend{Suspended: p.Action == "suspend"},
		}}
		if p.Action == "unsuspend" {
			acts = append(acts, discovery(app, p.InstallationID)...)
		}
		return acts
	case "deleted":
		return changedEach(e.Repositories, p, workflows.ChangedReasonInstallationDeleted)
	}
	return nil
}

// discovery starts DiscoveryWorkflow for one installation.
func discovery(app *config.App, installationID int64) []Action {
	if installationID == 0 {
		return nil
	}
	return []Action{{
		Kind:       KindStartDiscovery,
		WorkflowID: workflows.TargetedDiscoveryID(app.Name, installationID),
		Arg:        workflows.DiscoveryInput{App: app.Name, Installations: []int64{installationID}},
	}}
}

// changedEach signals changed{reason} to each listed repository.
func changedEach(repos []*github.Repository, p *Payload, reason string) []Action {
	acts := make([]Action, 0, len(repos))
	for _, r := range repos {
		acts = append(acts, changed(r.GetID(), workflows.Changed{
			Reason:         reason,
			InstallationID: p.InstallationID,
			DeliveryID:     p.DeliveryID,
		}))
	}
	return acts
}

func changed(repoID int64, c workflows.Changed) Action {
	return Action{
		Kind:       KindSignal,
		WorkflowID: workflows.RepoWorkflowID(repoID),
		Signal:     workflows.ChangedSignal,
		Arg:        c,
	}
}
