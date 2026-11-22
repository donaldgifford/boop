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

	"github.com/donaldgifford/boop/internal/store/db"
)

// applyPullRequest records the PR as GitHub last described it. The guard
// is GitHub's updated_at, not the event time, so a reconcile (pr.synced)
// and a webhook agree on which is newer.
func applyPullRequest(ctx context.Context, q *db.Queries, e *Event) error {
	d, err := payload[*PullRequest](e)
	if err != nil {
		return err
	}
	labels := d.Labels
	if labels == nil {
		labels = []string{}
	}
	updated := d.UpdatedAt
	if updated.IsZero() {
		updated = e.OccurredAt
	}
	number := d.Number
	if number == 0 {
		number = e.PRNumber
	}
	return q.UpsertPullRequest(ctx, db.UpsertPullRequestParams{
		RepoID:      e.RepoID,
		Number:      i32(number),
		Branch:      d.Branch,
		State:       d.State,
		Title:       d.Title,
		Labels:      labels,
		Automerge:   d.Automerge,
		Url:         d.URL,
		OpenedAt:    d.OpenedAt,
		MergedAt:    d.MergedAt,
		ClosedAt:    d.ClosedAt,
		GhUpdatedAt: updated,
	})
}
