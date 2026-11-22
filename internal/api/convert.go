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

package api

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/donaldgifford/boop/internal/api/gen"
	"github.com/donaldgifford/boop/internal/store"
	"github.com/donaldgifford/boop/internal/store/db"
)

// The db rows to the contract's types. Optional strings the store keeps
// as '' stay strings; NULL columns become absent fields.

func ptrInt[T ~int32 | ~int64](v *T) *int {
	if v == nil {
		return nil
	}
	n := int(*v)
	return &n
}

func ptrString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func repository(r *db.ListRepositoriesRow) gen.Repository {
	return gen.Repository{
		Id: r.ID, InstallationId: r.InstallationID, Owner: r.Owner, Slug: r.Slug, State: r.State, Profile: r.Profile,
		OnboardedAt: r.OnboardedAt, OffboardedAt: r.OffboardedAt, NextDue: r.NextDue,
		LastRunId: r.LastRunID, LastRunAt: r.LastRunAt, LastOutcome: r.LastOutcome,
		OpenPrs: int(r.OpenPrs), Waiting: int(r.Waiting),
	}
}

func installation(r *db.ListInstallationsRow) (gen.Installation, error) {
	out := gen.Installation{
		Id: r.ID, App: r.App, Owner: r.AccountLogin, AccountType: ptrString(r.AccountType),
		Suspended: r.Suspended, RemovedAt: r.RemovedAt, Repositories: int(r.Repositories),
	}
	if len(r.LastSpend) == 0 || r.LastFinishedAt == nil {
		return out, nil
	}
	var spend map[string]store.Spend
	if err := json.Unmarshal(r.LastSpend, &spend); err != nil {
		return out, fmt.Errorf("installation %d spend: %w", r.ID, err)
	}
	budget := func(res string) *gen.Budget {
		s, ok := spend[res]
		if !ok {
			return nil
		}
		used := s.Before - s.After
		return &gen.Budget{Remaining: s.After, UsedSinceRunStart: &used, ObservedAt: *r.LastFinishedAt}
	}
	out.Core, out.Graphql = budget("core"), budget("graphql")
	return out, nil
}

func event(e *db.Event) (gen.Event, error) {
	data := map[string]any{}
	if len(e.Data) > 0 {
		if err := json.Unmarshal(e.Data, &data); err != nil {
			return gen.Event{}, fmt.Errorf("event %d data: %w", e.ID, err)
		}
	}
	return gen.Event{
		Id: e.ID, Key: e.Key, OccurredAt: e.OccurredAt, RecordedAt: e.RecordedAt, Kind: e.Kind, Source: e.Source,
		InstallationId: e.InstallationID, RepoId: e.RepoID, PrNumber: ptrInt(e.PrNumber),
		ActorLogin: e.ActorLogin, ActorType: e.ActorType, CauseKey: e.CauseKey, Summary: e.Summary, Data: data,
	}, nil
}

func events(rows []db.Event) ([]gen.Event, error) {
	out := make([]gen.Event, 0, len(rows))
	for i := range rows {
		e, err := event(&rows[i])
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func run(r *db.ListRunsRow) (gen.Run, error) {
	out := gen.Run{
		Id: r.ID, RepoId: r.RepoID, Owner: r.Owner, Slug: r.Slug, Cause: r.Cause, CauseKey: r.CauseKey,
		Outcome: r.Outcome, RepositoryResult: ptrString(r.RepositoryResult), StartedAt: r.StartedAt,
		FinishedAt: r.FinishedAt, QueuedMs: r.QueuedMs, DurationMs: r.DurationMs,
		RenovateVersion: ptrString(r.RenovateVersion), Updates: int(r.Updates),
	}
	var spend map[string]gen.Spend
	if len(r.Spend) > 0 {
		if err := json.Unmarshal(r.Spend, &spend); err != nil {
			return out, fmt.Errorf("run %s spend: %w", r.ID, err)
		}
	}
	if len(spend) > 0 {
		out.Spend = &spend
	}
	var problems []string
	if len(r.Problems) > 0 {
		if err := json.Unmarshal(r.Problems, &problems); err != nil {
			return out, fmt.Errorf("run %s problems: %w", r.ID, err)
		}
	}
	if len(problems) > 0 {
		out.Problems = &problems
	}
	return out, nil
}

func dependency(d *db.ListDependenciesRow) gen.Dependency {
	return gen.Dependency{
		RepoId: d.RepoID, Owner: d.Owner, Slug: d.Slug, Manager: d.Manager, PackageFile: d.PackageFile,
		PackageName: d.PackageName, DepName: ptrString(d.DepName), Datasource: d.Datasource,
		CurrentValue: d.CurrentValue, CurrentVersion: d.CurrentVersion, Purl: d.Purl,
	}
}

// update converts both update row shapes, which share their columns.
func update[R db.ListUpdatesRow | db.BranchUpdatesRow](row *R) gen.Update {
	u := db.ListUpdatesRow(*row)
	return gen.Update{
		RepoId: u.RepoID, Owner: u.Owner, Slug: u.Slug, Branch: u.Branch, PrNumber: ptrInt(u.PrNumber),
		PackageFile: u.PackageFile, PackageName: u.PackageName, DepName: ptrString(u.DepName), Manager: u.Manager,
		Datasource: u.Datasource, UpdateType: u.UpdateType, FromVersion: u.FromVersion, ToVersion: u.ToVersion,
		PurlFrom: u.PurlFrom, PurlTo: u.PurlTo, RunId: ptrString(u.RunID),
	}
}

func pullRequest(p *db.ListPullRequestsRow, updates []gen.Update) gen.PullRequest {
	labels := p.Labels
	if labels == nil {
		labels = []string{}
	}
	if updates == nil {
		updates = []gen.Update{}
	}
	return gen.PullRequest{
		RepoId: p.RepoID, Owner: p.Owner, Slug: p.Slug, Number: int(p.Number), Branch: p.Branch, State: p.State,
		Title: p.Title, Labels: labels, Automerge: p.Automerge, Url: p.Url, OpenedAt: p.OpenedAt,
		MergedAt: p.MergedAt, ClosedAt: p.ClosedAt, UpdatedAt: p.GhUpdatedAt, Updates: updates,
	}
}

func dashboardItem(d *db.ListDashboardItemsRow) (gen.DashboardItem, error) {
	deps := []string{}
	if len(d.Deps) > 0 {
		if err := json.Unmarshal(d.Deps, &deps); err != nil {
			return gen.DashboardItem{}, fmt.Errorf("dashboard item %d/%s deps: %w", d.RepoID, d.Marker, err)
		}
	}
	if deps == nil {
		deps = []string{}
	}
	return gen.DashboardItem{
		RepoId: d.RepoID, Owner: d.Owner, Slug: d.Slug, Marker: d.Marker, Branch: d.Branch, Section: d.Section,
		Title: d.Title, Deps: deps, FirstSeenAt: d.FirstSeenAt, LastSeenAt: d.LastSeenAt,
		TickedAt: d.TickedAt, TickedBy: d.TickedBy,
	}, nil
}

func pkg(p *db.ListPackagesRow) gen.Package {
	orEmpty := func(s []string) []string {
		if s == nil {
			return []string{}
		}
		return s
	}
	return gen.Package{
		Purl: p.Purl, Repositories: int(p.Repositories), Updates: int(p.Updates),
		FromVersions: orEmpty(p.FromVersions), ToVersions: orEmpty(p.ToVersions), Managers: orEmpty(p.Managers),
	}
}

func delivery(d *db.ListDeliveriesRow) (gen.Delivery, error) {
	id, err := uuid.Parse(d.DeliveryID)
	if err != nil {
		return gen.Delivery{}, fmt.Errorf("delivery %s: %w", d.DeliveryID, err)
	}
	return gen.Delivery{
		DeliveryId: id, Attempt: int(d.Attempt), App: d.App, Event: d.Event, Action: d.Action, Outcome: d.Outcome,
		GithubStatus: ptrInt(d.GithubStatus), Missed: d.Missed, InstallationId: d.InstallationID, RepoId: d.RepoID,
		ReceivedAt: d.ReceivedAt, AuditedAt: d.AuditedAt,
	}, nil
}
