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

// applyDeliveryReceived records the delivery ingest answered; a newer
// receipt of the same attempt wins.
func applyDeliveryReceived(ctx context.Context, q *db.Queries, e *Event) error {
	d, err := payload[*DeliveryReceived](e)
	if err != nil {
		return err
	}
	return q.UpsertDeliveryReceived(ctx, db.UpsertDeliveryReceivedParams{
		DeliveryID:     d.DeliveryID,
		Attempt:        i32(max(d.Attempt, 1)),
		App:            d.App,
		Event:          d.Event,
		Action:         d.Action,
		Outcome:        d.Outcome,
		InstallationID: nullInt64(e.InstallationID),
		RepoID:         nullInt64(e.RepoID),
		ReceivedAt:     e.OccurredAt,
		Payload:        d.Payload,
	})
}

// applyDeliveryAudited records what the delivery audit read from GitHub;
// a newer audit of the same attempt wins.
func applyDeliveryAudited(ctx context.Context, q *db.Queries, e *Event) error {
	d, err := payload[*DeliveryAudited](e)
	if err != nil {
		return err
	}
	return q.UpsertDeliveryAudited(ctx, db.UpsertDeliveryAuditedParams{
		DeliveryID:     d.DeliveryID,
		Attempt:        i32(max(d.Attempt, 1)),
		App:            d.App,
		Event:          d.Event,
		Action:         d.Action,
		GithubStatus:   i32(d.GitHubStatus),
		Missed:         d.Missed || e.Kind == KindDeliveryMissed,
		InstallationID: nullInt64(e.InstallationID),
		RepoID:         nullInt64(e.RepoID),
		AuditedAt:      e.OccurredAt,
	})
}
