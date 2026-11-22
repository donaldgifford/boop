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
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/donaldgifford/boop/internal/platform/github"
	"github.com/donaldgifford/boop/internal/store"
	"github.com/donaldgifford/boop/internal/workflows"
)

const (
	// hookDeliveryWindow is how long GitHub keeps deliveries.
	hookDeliveryWindow = 3 * 24 * time.Hour
	// hookDeliveriesPerPage is GitHub's largest page.
	hookDeliveriesPerPage = 100
)

// ListHookDeliveries reads the App's webhook deliveries newer than
// in.After, newest first, until it reaches After or the end of GitHub's
// 3-day window (DESIGN-0003 § Delivery audit). Each event's attempts are
// sorted into failed (the last attempt was not 2xx: ingest answered 503,
// or GitHub could not connect or timed out), missed (2xx at GitHub, and
// ingest has no delivery.received for it), redelivered (a 2xx after a
// failed attempt, or a 2xx redelivery) and ok; failed and missed ones are
// logged. Missed needs the store: without it a delivery is never missed.
// With the store, each failed, missed and redelivered delivery is
// appended as delivery.failed, delivery.missed or delivery.redelivered,
// keyed on its GUID and newest attempt, so a pass that lost its cursor
// re-reads the same window and appends nothing new. Ingest records a
// delivery before its answer reaches GitHub, so GitHub cannot list a 2xx
// that ingest is still recording.
func (a *Activities) ListHookDeliveries(ctx context.Context, in *workflows.ListHookDeliveriesInput) (*workflows.DeliveryAudit, error) {
	app, err := a.app(in.App)
	if err != nil {
		return nil, err
	}
	api, err := a.gh.App(app)
	if err != nil {
		return nil, err
	}
	oldest := a.now().Add(-hookDeliveryWindow)
	out := &workflows.DeliveryAudit{Newest: in.After}
	var read []github.HookDelivery
	for cursor := ""; ; {
		page, next, err := api.ListHookDeliveries(ctx, cursor, hookDeliveriesPerPage)
		if err != nil {
			return nil, honourRetryAfter(fmt.Errorf("list hook deliveries of %s: %w", app.Name, err))
		}
		out.Pages++
		done := next == ""
		for i := range page {
			if page[i].ID <= in.After || page[i].DeliveredAt.Before(oldest) {
				done = true
				break
			}
			read = append(read, page[i])
		}
		if done {
			break
		}
		cursor = next
	}
	out.Read = len(read)
	if len(read) > 0 {
		out.Newest = max(out.Newest, slices.MaxFunc(read, func(x, y github.HookDelivery) int { return cmp.Compare(x.ID, y.ID) }).ID)
	}
	audited, err := a.auditDeliveries(ctx, app.Name, read)
	if err != nil {
		return nil, err
	}
	out.Counts = audited.counts
	if len(audited.events) > 0 {
		if err := a.store.Append(ctx, audited.events...); err != nil {
			return nil, fmt.Errorf("record delivery audit of %s: %w", app.Name, err)
		}
	}
	return out, nil
}

// audited is one pass's counts and, with the store, its events.
type audited struct {
	counts []workflows.DeliveryCounts
	events []store.Event
}

// auditDeliveries groups attempts by GUID and counts each event's
// deliveries as failed, missed, redelivered or ok, sorted by event. With
// the store it asks which GUIDs ingest recorded, and builds the events.
func (a *Activities) auditDeliveries(ctx context.Context, app string, read []github.HookDelivery) (*audited, error) {
	guids, byGUID := groupByGUID(read)
	var received map[string]bool
	if a.store != nil && len(guids) > 0 {
		var err error
		if received, err = a.store.ReceivedDeliveries(ctx, guids); err != nil {
			return nil, fmt.Errorf("delivery audit of %s: %w", app, err)
		}
	}
	out := &audited{}
	counts := make(map[string]*workflows.DeliveryCounts)
	for _, guid := range guids {
		attempts := byGUID[guid]
		slices.SortFunc(attempts, func(x, y github.HookDelivery) int { return cmp.Compare(x.ID, y.ID) })
		last := attempts[len(attempts)-1]
		c := counts[last.Event]
		if c == nil {
			c = &workflows.DeliveryCounts{Event: last.Event}
			counts[last.Event] = c
		}
		kind := auditKind(attempts, received)
		tally(c, kind)
		if kind == store.KindDeliveryFailed || kind == store.KindDeliveryMissed {
			a.log.Warn("webhook delivery "+strings.TrimPrefix(string(kind), "delivery."), "app", app, "guid", guid,
				"event", last.Event, "action", last.Action, "status_code", last.StatusCode, "status", last.Status,
				"delivered_at", last.DeliveredAt, "installation", last.InstallationID, "repo_id", last.RepositoryID)
		}
		if kind != "" && a.store != nil && store.IsUUID(guid) {
			out.events = append(out.events, auditEvent(app, guid, kind, &last))
		}
	}
	out.counts = make([]workflows.DeliveryCounts, 0, len(counts))
	for _, c := range counts {
		out.counts = append(out.counts, *c)
	}
	slices.SortFunc(out.counts, func(x, y workflows.DeliveryCounts) int { return cmp.Compare(x.Event, y.Event) })
	return out, nil
}

// groupByGUID groups attempts by GUID, keeping the order GUIDs were
// first read in.
func groupByGUID(read []github.HookDelivery) (guids []string, byGUID map[string][]github.HookDelivery) {
	byGUID = make(map[string][]github.HookDelivery)
	for i := range read {
		guid := read[i].GUID
		if _, ok := byGUID[guid]; !ok {
			guids = append(guids, guid)
		}
		byGUID[guid] = append(byGUID[guid], read[i])
	}
	return guids, byGUID
}

// tally counts one delivery of kind ("" is ok).
func tally(c *workflows.DeliveryCounts, kind store.Kind) {
	switch kind {
	case store.KindDeliveryFailed:
		c.Failed++
	case store.KindDeliveryMissed:
		c.Missed++
	case store.KindDeliveryRedelivered:
		c.Redelivered++
	default:
		c.OK++
	}
}

// auditKind is a GUID's outcome over its attempts, sorted oldest first:
// failed when the last attempt was not 2xx, missed when received is set
// (the store is on) and has no record of it, redelivered when a 2xx
// followed a failure or was itself a redelivery, and "" when ok.
func auditKind(attempts []github.HookDelivery, received map[string]bool) store.Kind {
	last := &attempts[len(attempts)-1]
	switch {
	case !last.OK():
		return store.KindDeliveryFailed
	case received != nil && store.IsUUID(last.GUID) && !received[last.GUID]:
		return store.KindDeliveryMissed
	case last.Redelivery || slices.ContainsFunc(attempts, func(d github.HookDelivery) bool { return !d.OK() }):
		return store.KindDeliveryRedelivered
	default:
		return ""
	}
}

// auditEvent is a delivery's audit result. It is attempt 1, the row
// ingest's delivery.received writes, so the two join on delivery_id;
// the key names GitHub's newest attempt, so a later attempt is a new
// event and a re-read is a duplicate.
func auditEvent(app, guid string, kind store.Kind, last *github.HookDelivery) store.Event {
	name := last.Event
	if last.Action != "" {
		name += "." + last.Action
	}
	return store.Event{
		Key:            fmt.Sprintf("audit:%s:%d:%s", guid, last.ID, kind),
		OccurredAt:     last.DeliveredAt,
		Kind:           kind,
		Source:         store.SourceAudit,
		InstallationID: last.InstallationID,
		RepoID:         last.RepositoryID,
		Summary:        fmt.Sprintf("%s %s %s (GitHub status %d)", app, name, strings.TrimPrefix(string(kind), "delivery."), last.StatusCode),
		Data: &store.DeliveryAudited{
			V: 1, DeliveryID: guid, Attempt: 1, App: app, Event: last.Event, Action: last.Action,
			GitHubStatus: last.StatusCode, Missed: kind == store.KindDeliveryMissed,
		},
	}
}
