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
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/donaldgifford/boop/internal/store/db"
)

// PartitionsAhead is how many months of events partitions maintenance
// keeps created beyond the current one.
const PartitionsAhead = 2

// Retention is how long the store keeps what (DESIGN-0004 § Retention).
// A zero or negative duration keeps everything for that rule.
type Retention struct {
	// Events is for events (a month partition at a time) and runs.
	Events time.Duration
	// DeliveryPayloads is for webhook_deliveries.payload.
	DeliveryPayloads time.Duration
	// Offboarded is for an offboarded repository's projections.
	Offboarded time.Duration
}

// MaintenanceRule is one retention rule StoreMaintenanceWorkflow runs.
type MaintenanceRule string

// The maintenance rules, in the order the workflow runs them.
const (
	// RulePartitions creates partitions PartitionsAhead months ahead and
	// drops those past Retention.Events.
	RulePartitions MaintenanceRule = "partitions"
	// RuleDeliveryPayloads nulls payloads past Retention.DeliveryPayloads.
	RuleDeliveryPayloads MaintenanceRule = "delivery-payloads"
	// RuleOffboarded deletes the projections of repositories offboarded
	// before Retention.Offboarded.
	RuleOffboarded MaintenanceRule = "offboarded"
	// RuleRuns deletes runs that started before Retention.Events.
	RuleRuns MaintenanceRule = "runs"
)

// MaintenanceRules is every rule, in order.
func MaintenanceRules() []MaintenanceRule {
	return []MaintenanceRule{RulePartitions, RuleDeliveryPayloads, RuleOffboarded, RuleRuns}
}

// Maintain runs one retention rule as of now and returns how many
// partitions or rows it removed. The cutoff is now minus the rule's
// retention; with a zero retention the rule keeps everything (only
// partitions are still created). For events the cutoff is rounded down
// to its month: a partition, the default partition's rows and the
// idempotency keys all go a whole month at a time.
func (s *Store) Maintain(ctx context.Context, rule MaintenanceRule, now time.Time, r Retention) (int64, error) {
	q := db.New(s.pool)
	switch rule {
	case RulePartitions:
		if _, err := q.EnsurePartitions(ctx, PartitionsAhead); err != nil {
			return 0, fmt.Errorf("ensure partitions: %w", err)
		}
		if r.Events <= 0 {
			return 0, nil
		}
		n, err := q.DropPartitions(ctx, monthStart(now.Add(-r.Events)))
		if err != nil {
			return 0, fmt.Errorf("drop partitions: %w", err)
		}
		return int64(n), nil
	case RuleDeliveryPayloads:
		return keep(r.DeliveryPayloads, func() (int64, error) { return q.NullDeliveryPayloads(ctx, now.Add(-r.DeliveryPayloads)) })
	case RuleOffboarded:
		return keep(r.Offboarded, func() (int64, error) { return s.deleteOffboarded(ctx, now.Add(-r.Offboarded)) })
	case RuleRuns:
		return keep(r.Events, func() (int64, error) { return q.DeleteRunsBefore(ctx, now.Add(-r.Events)) })
	default:
		return 0, fmt.Errorf("unknown maintenance rule %q", rule)
	}
}

// monthStart is the start of t's month in UTC. Partitions drop a month at
// a time, so the default partition's rows and the idempotency keys use
// the same boundary: a kept partition never loses its events' keys.
func monthStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// keep runs fn unless the retention keeps everything.
func keep(d time.Duration, fn func() (int64, error)) (int64, error) {
	if d <= 0 {
		return 0, nil
	}
	return fn()
}

// deleteOffboarded deletes, in one transaction, every projection row of
// the repositories offboarded before cutoff, the repositories last. A
// repository onboarded again has no offboarded_at, so it is kept. Its
// events and deliveries stay until their own retention.
func (s *Store) deleteOffboarded(ctx context.Context, cutoff time.Time) (int64, error) {
	var total int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		ids, err := q.OffboardedBefore(ctx, cutoff)
		if err != nil || len(ids) == 0 {
			return err
		}
		for _, del := range []func(context.Context, []int64) (int64, error){
			q.DeleteRunsOf, q.DeleteBranchUpdatesOf, q.DeleteDependenciesOf, q.DeletePullRequestsOf,
			q.DeleteDashboardItemsOf, q.DeleteDashboardsOf, q.DeleteRepositories,
		} {
			n, err := del(ctx, ids)
			if err != nil {
				return err
			}
			total += n
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("delete offboarded projections: %w", err)
	}
	return total, nil
}
