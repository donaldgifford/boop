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
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/go-github/v62/github"

	"github.com/donaldgifford/boop/internal/renovate/dashboard"
	"github.com/donaldgifford/boop/internal/store"
)

// StoreAppendTimeout bounds one delivery's append, which runs after the
// answer is decided.
const StoreAppendTimeout = 5 * time.Second

// Appender appends events to the store. *store.Store implements it.
type Appender interface {
	Append(ctx context.Context, events ...store.Event) error
}

// DashboardMetrics counts dependency dashboard parses.
// observability.Metrics implements it.
type DashboardMetrics interface {
	DashboardParse(outcome string)
}

// StoreSink records each delivery in the event store (DESIGN-0004 §
// Event catalogue): delivery.received with a decoded payload subset, and
// the events derived from the same delivery, appended in one call. A
// failure is logged, and counted by the store's append metric; it never
// changes the answer, which is already decided.
type StoreSink struct {
	Store   Appender
	Logger  *slog.Logger
	Metrics DashboardMetrics
	// Log, when set, also receives every record (the delivery log line).
	Log Sink
}

// Record implements Sink.
func (s *StoreSink) Record(ctx context.Context, r *Record) {
	if s.Log != nil {
		s.Log.Record(ctx, r)
	}
	if !store.IsUUID(r.DeliveryID) {
		// Not GitHub's: nothing to key it on.
		return
	}
	events, err := s.events(r)
	if err != nil {
		s.Logger.WarnContext(ctx, "delivery not recorded", "delivery_id", r.DeliveryID, "error", err)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, StoreAppendTimeout)
	defer cancel()
	if err := s.Store.Append(ctx, events...); err != nil {
		s.Logger.WarnContext(ctx, "delivery not recorded", "delivery_id", r.DeliveryID, "events", len(events), "error", err)
	}
}

// events builds delivery.received and the events derived from it.
func (s *StoreSink) events(r *Record) ([]store.Event, error) {
	base := store.Event{
		OccurredAt: r.ReceivedAt, Source: store.SourceWebhook, InstallationID: r.InstallationID, RepoID: r.RepoID,
		Actor: actor(r.SenderLogin, r.SenderType),
	}
	received := base
	received.Key = fmt.Sprintf("delivery:%s:received:%s", r.DeliveryID, r.ReceivedAt.UTC().Format(time.RFC3339Nano))
	received.Kind = store.KindDeliveryReceived
	received.Summary = summary(r)
	sub, err := subset(r.Payload)
	if err != nil {
		return nil, err
	}
	received.Data = &store.DeliveryReceived{
		V: 1, DeliveryID: r.DeliveryID, Attempt: 1, App: r.App, Event: r.Event, Action: r.Action,
		Outcome: string(r.Outcome), Payload: sub,
	}
	out := []store.Event{received}
	if r.Payload == nil || r.Outcome == OutcomeRejected {
		return out, nil
	}
	cause := received.Key
	for _, d := range s.derived(r) {
		e := base
		e.Key = fmt.Sprintf("delivery:%s:%s", r.DeliveryID, d.suffix)
		e.Kind, e.Summary, e.Data, e.CauseKey = d.kind, d.summary, d.data, cause
		if d.at != nil && !d.at.IsZero() {
			e.OccurredAt = *d.at
		}
		if d.pr != 0 {
			e.PRNumber = d.pr
		}
		out = append(out, e)
	}
	return out, nil
}

// derivedEvent is one event a delivery implies.
type derivedEvent struct {
	suffix  string
	kind    store.Kind
	summary string
	data    store.Data
	at      *time.Time
	pr      int
}

func (s *StoreSink) derived(r *Record) []derivedEvent {
	switch e := r.Payload.Typed.(type) {
	case *github.PullRequestEvent:
		return pullRequest(e, r)
	case *github.IssuesEvent:
		return s.dashboardEvents(e, r)
	case *github.InstallationEvent:
		return installation(e, r)
	}
	return nil
}

// prKinds maps a pull_request action to its kind.
var prKinds = map[string]store.Kind{
	"opened":      store.KindPROpened,
	"edited":      store.KindPREdited,
	"labeled":     store.KindPREdited,
	"unlabeled":   store.KindPREdited,
	"closed":      store.KindPRClosed,
	"reopened":    store.KindPRReopened,
	"synchronize": store.KindPRSynchronized,
}

// pullRequest is pr.* for a PR the bot opened.
func pullRequest(e *github.PullRequestEvent, r *Record) []derivedEvent {
	pr := e.GetPullRequest()
	kind, ok := prKinds[r.Action]
	if !ok || pr.GetUser().GetLogin() != r.Bot || r.Bot == "" {
		return nil
	}
	state := pr.GetState()
	if pr.GetMerged() {
		state = store.PRStateMerged
		if kind == store.KindPRClosed {
			kind = store.KindPRMerged
		}
	}
	labels := make([]string, 0, len(pr.Labels))
	for _, l := range pr.Labels {
		labels = append(labels, l.GetName())
	}
	updated := pr.GetUpdatedAt().Time
	return []derivedEvent{{
		suffix: string(kind), kind: kind, pr: pr.GetNumber(), at: &updated,
		summary: fmt.Sprintf("#%d %s", pr.GetNumber(), r.Action),
		data: &store.PullRequest{
			V: 1, Number: pr.GetNumber(), Branch: pr.GetHead().GetRef(), State: state, Title: pr.GetTitle(),
			Labels: labels, Automerge: pr.GetAutoMerge() != nil, URL: pr.GetHTMLURL(),
			OpenedAt: timePtr(pr.CreatedAt), MergedAt: timePtr(pr.MergedAt), ClosedAt: timePtr(pr.ClosedAt), UpdatedAt: updated,
		},
	}}
}

// dashboardEvents is dashboard.updated for the bot's rewrite of its
// dashboard, and dashboard.ticked for each box a human ticked.
func (s *StoreSink) dashboardEvents(e *github.IssuesEvent, r *Record) []derivedEvent {
	issue := e.GetIssue()
	if r.Action != actionEdited || r.Bot == "" || issue.GetUser().GetLogin() != r.Bot {
		return nil
	}
	after, err := dashboard.Parse(issue.GetBody())
	s.countParse(&after, err)
	if err != nil {
		return nil
	}
	// GitHub's time for the edit orders rewrites and ticks.
	at := issue.GetUpdatedAt().Time
	if r.Own {
		return []derivedEvent{{
			at: &at, suffix: string(store.KindDashboardUpdated), kind: store.KindDashboardUpdated,
			summary: fmt.Sprintf("dashboard #%d rewritten: %d items", issue.GetNumber(), len(after.Items)),
			data:    &store.DashboardUpdated{V: 1, Items: dashboardItems(&after), URL: issue.GetHTMLURL()},
		}}
	}
	from := e.GetChanges().GetBody().GetFrom()
	if from == "" {
		return nil
	}
	before, err := dashboard.Parse(from)
	s.countParse(&before, err)
	if err != nil {
		return nil
	}
	var out []derivedEvent
	for i, t := range dashboard.Diff(before, after) {
		out = append(out, derivedEvent{
			at: &at, suffix: fmt.Sprintf("%s:%d", store.KindDashboardTicked, i), kind: store.KindDashboardTicked,
			summary: fmt.Sprintf("%s ticked %s %s", r.SenderLogin, t.Raw, t.Branch),
			data:    &store.DashboardTicked{V: 1, Marker: t.Raw, Branch: t.Branch},
		})
	}
	return out
}

func (s *StoreSink) countParse(snap *dashboard.Snapshot, err error) {
	if s.Metrics != nil {
		s.Metrics.DashboardParse(dashboard.Outcome(snap, err))
	}
}

func dashboardItems(snap *dashboard.Snapshot) []store.DashboardItem {
	out := make([]store.DashboardItem, 0, len(snap.Items))
	for i := range snap.Items {
		it := &snap.Items[i]
		out = append(
			out,
			store.DashboardItem{Marker: it.Raw, Branch: it.Branch, Section: dashboard.SectionKey(it.Section), Title: it.Title, Deps: it.Deps},
		)
	}
	return out
}

// installationKinds maps an installation action to its kind.
var installationKinds = map[string]store.Kind{
	"created":       store.KindInstallationAdded,
	actionDeleted:   store.KindInstallationRemove,
	actionSuspend:   store.KindInstallationSusp,
	actionUnsuspend: store.KindInstallationUnsusp,
}

// installation is installation.* for the App's own installation events.
func installation(e *github.InstallationEvent, r *Record) []derivedEvent {
	kind := installationKinds[r.Action]
	if kind == "" {
		return nil
	}
	acct := e.GetInstallation().GetAccount()
	return []derivedEvent{{
		suffix: string(kind), kind: kind,
		summary: fmt.Sprintf("%s %s on %s", r.App, r.Action, acct.GetLogin()),
		data:    &store.Installation{V: 1, App: r.App, AccountLogin: acct.GetLogin(), AccountType: acct.GetType()},
	}}
}

// subset is the decoded part of a payload delivery.received keeps
// (DESIGN-0004 OQ7): the subject and the fields the timeline shows,
// never the whole body.
func subset(p *Payload) (json.RawMessage, error) {
	if p == nil {
		return nil, nil
	}
	m := map[string]any{"action": p.Action, "sender": p.SenderLogin}
	if p.Repo != nil {
		m["repository"] = p.Repo.Slug
	}
	switch e := p.Typed.(type) {
	case *github.PullRequestEvent:
		pr := e.GetPullRequest()
		m["number"], m["title"], m["head_ref"], m["author"] = pr.GetNumber(), pr.GetTitle(), pr.GetHead().GetRef(), pr.GetUser().GetLogin()
		m["merged"] = pr.GetMerged()
	case *github.IssuesEvent:
		m["number"], m["title"], m["author"] = e.GetIssue().GetNumber(), e.GetIssue().GetTitle(), e.GetIssue().GetUser().GetLogin()
	case *github.PushEvent:
		m["ref"], m["before"], m["after"], m["commits"] = e.GetRef(), e.GetBefore(), e.GetAfter(), len(e.Commits)
	case *github.InstallationEvent:
		m["account"] = e.GetInstallation().GetAccount().GetLogin()
	case *github.InstallationRepositoriesEvent:
		m["added"], m["removed"] = repoNames(e.RepositoriesAdded), repoNames(e.RepositoriesRemoved)
	case *github.RepositoryEvent:
		m["full_name"] = e.GetRepo().GetFullName()
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("encode payload subset: %w", err)
	}
	return b, nil
}

func repoNames(rs []*github.Repository) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.GetFullName())
	}
	return out
}

func summary(r *Record) string {
	s := r.Event
	if r.Action != "" {
		s += " " + r.Action
	}
	if r.Subject != "" {
		s += " " + r.Subject
	}
	return s + ": " + string(r.Outcome)
}

func actor(login, typ string) store.Actor {
	if login == "" {
		return store.Actor{}
	}
	if typ == "Bot" {
		return store.Actor{Login: login, Type: store.ActorBot}
	}
	return store.Actor{Login: login, Type: store.ActorUser}
}

func timePtr(t *github.Timestamp) *time.Time {
	if t == nil || t.IsZero() {
		return nil
	}
	v := t.Time
	return &v
}
