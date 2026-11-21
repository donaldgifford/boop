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

package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/donaldgifford/boop/internal/store/db"
)

// applyDashboardUpdated replaces the repository's items with the
// rewrite's, keeping first_seen_at for those that persist. A rewrite
// older than the one applied is ignored.
func applyDashboardUpdated(ctx context.Context, q *db.Queries, e *Event) error {
	d, err := payload[*DashboardUpdated](e)
	if err != nil {
		return err
	}
	_, err = q.AdvanceDashboard(ctx, db.AdvanceDashboardParams{RepoID: e.RepoID, UpdatedAt: e.OccurredAt})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, item := range d.Items {
		deps, err := jsonValue(item.Deps, "[]")
		if err != nil {
			return err
		}
		if err := q.UpsertDashboardItem(ctx, db.UpsertDashboardItemParams{
			RepoID:  e.RepoID,
			Marker:  item.Marker,
			Branch:  item.Branch,
			Section: item.Section,
			Title:   item.Title,
			Deps:    deps,
			SeenAt:  e.OccurredAt,
		}); err != nil {
			return err
		}
	}
	return q.DeleteStaleDashboardItems(ctx, db.DeleteStaleDashboardItemsParams{RepoID: e.RepoID, SeenAt: e.OccurredAt})
}

// applyDashboardTicked records who ticked an item. A tick older than the
// item's last rewrite was already consumed by it and is ignored.
func applyDashboardTicked(ctx context.Context, q *db.Queries, e *Event) error {
	d, err := payload[*DashboardTicked](e)
	if err != nil {
		return err
	}
	return q.TickDashboardItem(ctx, db.TickDashboardItemParams{
		TickedAt: e.OccurredAt,
		TickedBy: e.Actor.Login,
		RepoID:   e.RepoID,
		Marker:   d.Marker,
		Branch:   d.Branch,
	})
}
