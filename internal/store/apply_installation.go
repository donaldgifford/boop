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

// applyInstallation applies every installation.* kind; a newer change
// wins. Removal keeps the row with removed_at set.
func applyInstallation(ctx context.Context, q *db.Queries, e *Event) error {
	d, err := payload[*Installation](e)
	if err != nil {
		return err
	}
	arg := db.UpsertInstallationParams{
		ID:           e.InstallationID,
		App:          d.App,
		AccountLogin: d.AccountLogin,
		AccountType:  d.AccountType,
		Suspended:    e.Kind == KindInstallationSusp,
		UpdatedAt:    e.OccurredAt,
	}
	if e.Kind == KindInstallationRemove {
		at := e.OccurredAt
		arg.RemovedAt = &at
	}
	return q.UpsertInstallation(ctx, arg)
}

// applyDiscoveryFinished records one discovery pass.
func applyDiscoveryFinished(ctx context.Context, q *db.Queries, e *Event) error {
	d, err := payload[*DiscoveryFinished](e)
	if err != nil {
		return err
	}
	finished := orTime(d.FinishedAt, e.OccurredAt)
	return q.UpsertDiscoveryPass(ctx, db.UpsertDiscoveryPassParams{
		App:        d.App,
		StartedAt:  orTime(d.StartedAt, finished),
		FinishedAt: finished,
		Seen:       i32(d.Seen),
		Onboarded:  i32(d.Onboarded),
		Failed:     i32(d.Failed),
		ProbeCost:  i32(d.ProbeCost),
	})
}
