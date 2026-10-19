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
	"time"

	"github.com/donaldgifford/boop/internal/platform/github"
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
// or GitHub could not connect or timed out), redelivered (a 2xx after a
// failed attempt, or a 2xx redelivery) and ok; failed ones are logged.
// A delivery GitHub sent that ingest never recorded ("missed") needs
// ingest's store to tell, which IMPL-0005 adds; until then a connection
// failure shows here as failed. The answer depends only on GitHub's
// list, so a pass that lost its cursor reads the same window again.
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
	out.Counts = a.classifyDeliveries(app.Name, read)
	return out, nil
}

// classifyDeliveries groups attempts by GUID and counts each event's
// deliveries as failed, redelivered or ok, sorted by event.
func (a *Activities) classifyDeliveries(app string, read []github.HookDelivery) []workflows.DeliveryCounts {
	byGUID := make(map[string][]github.HookDelivery)
	var guids []string
	for i := range read {
		guid := read[i].GUID
		if _, ok := byGUID[guid]; !ok {
			guids = append(guids, guid)
		}
		byGUID[guid] = append(byGUID[guid], read[i])
	}
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
		failedBefore := slices.ContainsFunc(attempts, func(d github.HookDelivery) bool { return !d.OK() })
		switch {
		case !last.OK():
			c.Failed++
			a.log.Warn("webhook delivery failed", "app", app, "guid", guid, "event", last.Event, "action", last.Action,
				"status_code", last.StatusCode, "status", last.Status, "delivered_at", last.DeliveredAt,
				"installation", last.InstallationID, "repo_id", last.RepositoryID)
		case failedBefore || last.Redelivery:
			c.Redelivered++
		default:
			c.OK++
		}
	}
	out := make([]workflows.DeliveryCounts, 0, len(counts))
	for _, c := range counts {
		out = append(out, *c)
	}
	slices.SortFunc(out, func(x, y workflows.DeliveryCounts) int { return cmp.Compare(x.Event, y.Event) })
	return out
}
